// The workspace-level owner of every project's index: where the database comes
// from, cache lifetime, freshness policy, and the locking contract.
//
// # Reads never wait on a reindex
//
// A query reads two things: the rows PostgreSQL has committed, and an
// immutable view of the project's graph order and titles (indexView) that the
// build side publishes through an atomic pointer. Neither takes a lock, so a
// query against a project that is reindexing answers from the last committed
// index instead of queueing behind it. The only query that waits is the first
// one for a project with no index at all.
//
// Keeping the index current is background work: the store reports its own
// writes (Notify), the watcher reports outside edits (NotifyNode), opening a
// project builds its index (Warm), and a query that finds the view older than
// revalidateAfter asks for a walk. Each of those only records what changed and
// makes sure one worker per project is on it; the worker waits notifyDebounce
// so a burst collapses into one pass, refreshes just the nodes named (or
// re-reads the graph, or walks), and publishes a new view. A change is
// searchable within notifyDebounce plus one pass.
//
// # Locking contract
//
// Three levels, never taken in the wrong order:
//
//   - Manager.mu guards only the entries map, the lazily opened store and the
//     worker count. It is taken for the length of a map lookup or an eviction
//     sweep and is always released before any index work begins. No disk I/O,
//     no tokenisation, no query ever runs under it.
//   - entry.mu is one project's build lock. Opening, reconciling and
//     refreshing hold it, for that project only. A query takes it only to open
//     a project that has no view yet.
//   - entry.pendMu guards the pending changes. It is a leaf, held for a few
//     assignments and never across anything else, so the store can report a
//     write while holding its own lock.
//
// Manager.mu may be taken while no entry.mu is held, and never the other way
// round; in practice no code path holds both, so a cold project rebuilding
// cannot block a query against any other project. entry.size is atomic
// precisely so the eviction sweep can read a project's footprint without
// waiting for its lock.
//
// PostgreSQL takes it from there: writes go through the database's small
// search pool and each node is one upsert, so two projects indexing at once
// interleave, and two writers indexing the same node leave one row.

package searchindex

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/safego"
)

const (
	// maxProjects and memoryBudget bound what a long-lived server keeps in RAM.
	// Eviction is free of consequence: what is cached is only the bookkeeping
	// that lets a project be reopened without reading its files, and the rows
	// themselves are in the database.
	maxProjects  = 8
	memoryBudget = int64(64 << 20)

	// backgroundPassTimeout bounds one background pass, so a database that
	// stops answering cannot hold a worker forever.
	backgroundPassTimeout = 2 * time.Minute
)

// revalidateAfter is how long a query trusts the view before it asks for a
// walk of the project (see projectIndex.reconcile for why a walk cannot be
// skipped entirely). The walk runs in the background; the query that asked
// answers from the view it has. It is a var only so tests can pin it.
var revalidateAfter = 250 * time.Millisecond

// notifyDebounce is how long a worker waits after a change is reported before
// it starts a pass, so a burst of saves (an auto-saving editor, a drag of many
// cards) costs one pass instead of one each. It plus one pass is the whole
// delay between a write and its being searchable. A var only so tests can pin
// it.
var notifyDebounce = 100 * time.Millisecond

// ErrNoIndex is a Manager without a database: there is nowhere to keep an
// index, and the caller falls back to its direct scan.
var ErrNoIndex = errors.New("searchindex: no database, no index")

// Manager caches one index per project directory, over one database.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	store   *docStore
	// database is the workspace database, or nil when the caller did not give
	// one; every search then answers ErrNoIndex.
	database *db.DB

	// busy counts background workers; idle is broadcast (under mu) when it
	// drops to zero, which is what Close waits for.
	busy int
	idle *sync.Cond
}

type entry struct {
	root string
	size atomic.Int64
	// used is the last search time, read by eviction under Manager.mu.
	used atomic.Int64
	// dropped says the entry has left the cache (evicted, pruned, rebuilt,
	// closed). Its worker stops instead of writing rows for a project nobody
	// holds any more.
	dropped atomic.Bool

	// view is what queries read: published whole by the build side, read with
	// no lock. Nil until the first build, and again after a pass failed, which
	// sends the next query to open the index itself and surface the error.
	view atomic.Pointer[indexView]
	// checked is when a walk last confirmed the view against the files, in
	// Unix nanoseconds.
	checked atomic.Int64

	// mu is the build lock; index is guarded by it.
	mu    sync.Mutex
	index *projectIndex

	// pendMu guards what the next background pass has to do, and whether a
	// worker is already on it.
	pendMu    sync.Mutex
	pendNodes map[string]struct{}
	pendGraph bool
	pendWalk  bool
	running   bool
}

// NewManager returns a cache with no database. It holds no index: every
// search answers ErrNoIndex and the caller scans the files directly. Use
// NewManagerWithDB.
func NewManager() *Manager {
	return newIndexManager(nil)
}

// NewManagerWithDB returns a cache backed by the workspace database.
func NewManagerWithDB(database *db.DB) *Manager {
	return newIndexManager(database)
}

func newIndexManager(database *db.DB) *Manager {
	m := &Manager{entries: make(map[string]*entry), database: database}
	m.idle = sync.NewCond(&m.mu)
	return m
}

// Search returns every node in the project whose indexed text contains the
// already-lowercased query, in graph order. An error means the project could
// not be indexed at all (an unreadable or unparsable graph.yaml, or a database
// that will not answer); the caller is expected to fall back to a direct scan,
// exactly as before.
func (m *Manager) Search(root, lowered string) ([]Match, error) {
	return m.SearchContext(context.Background(), root, lowered)
}

// SearchContext is Search with a deadline the caller controls.
func (m *Manager) SearchContext(ctx context.Context, root, lowered string) ([]Match, error) {
	return retryDropped(m, func() ([]Match, error) {
		view, err := m.viewFor(ctx, root)
		if err != nil {
			return nil, err
		}
		return view.query(ctx, lowered)
	})
}

// retryDropped runs a search once more, from an empty cache and a recreated
// table, when it failed because search_documents is gone. Dropping the table
// is a supported thing for an operator to do (it is derived data), and the
// alternative is a server whose search stays broken until it restarts.
func retryDropped[T any](m *Manager, run func() (T, error)) (T, error) {
	out, err := run()
	if err == nil || !isUndefinedTable(err) {
		return out, err
	}
	m.mu.Lock()
	m.store = nil
	m.dropAllLocked()
	m.mu.Unlock()
	return run()
}

// RankContext is the ranked search: the project's best limit matches for a
// query of keywords or a whole sentence (see indexView.rank). The locking
// and the fallback contract are SearchContext's.
func (m *Manager) RankContext(ctx context.Context, root, query string, limit int) ([]Ranked, error) {
	return retryDropped(m, func() ([]Ranked, error) {
		view, err := m.viewFor(ctx, root)
		if err != nil {
			return nil, err
		}
		return view.rank(ctx, query, limit)
	})
}

// viewFor returns what a query reads for root. With a view published it never
// waits: a view older than revalidateAfter asks for a walk in the background
// and is used as it stands. Only a project with no view yet is opened here,
// under its build lock, because there is nothing else to answer from.
func (m *Manager) viewFor(ctx context.Context, root string) (*indexView, error) {
	docs, err := m.docs(ctx)
	if err != nil {
		return nil, err
	}
	e := m.entryFor(root)
	if view := e.view.Load(); view != nil {
		if time.Since(time.Unix(0, e.checked.Load())) >= revalidateAfter {
			m.schedule(e, 0, func(e *entry) { e.pendWalk = true })
		}
		return view, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if view := e.view.Load(); view != nil {
		return view, nil
	}
	if e.index == nil {
		index, err := openIndex(ctx, docs, e.root)
		if err != nil {
			return nil, err
		}
		e.index = index
	}
	e.publishLocked(true)
	return e.view.Load(), nil
}

// publishLocked hands queries the index as it now stands. walked says a walk
// just confirmed it against the files. The caller holds e.mu.
func (e *entry) publishLocked(walked bool) {
	e.view.Store(e.index.view())
	e.size.Store(e.index.bytes)
	if walked {
		e.checked.Store(time.Now().UnixNano())
	}
}

// dropLocked forgets the index after a pass failed: the graph went away or
// stopped parsing, or the database refused. The rows stay; the next query
// opens the project afresh and surfaces the error if there still is one. The
// caller holds e.mu.
func (e *entry) dropLocked() {
	e.index = nil
	e.view.Store(nil)
}

// Update applies a change now, on the caller's goroutine, and returns once it
// is searchable. An empty nodeID means graph.yaml changed. The server's own
// paths use Notify and NotifyNode, which do the same work in the background;
// Update is for a caller that has to know the change is in.
//
// A project with nothing cached is left alone: there is no index to patch, and
// the next search opens a current one anyway.
func (m *Manager) Update(root, nodeID string) {
	m.UpdateContext(context.Background(), root, nodeID)
}

// UpdateContext is Update with a deadline the caller controls.
func (m *Manager) UpdateContext(ctx context.Context, root, nodeID string) {
	e := m.cached(root)
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.index == nil {
		return
	}
	var err error
	if nodeID == "" {
		err = e.index.refreshGraph(ctx)
	} else {
		err = e.index.refreshNode(ctx, nodeID)
	}
	if err != nil {
		e.dropLocked()
		return
	}
	e.publishLocked(false)
}

// Notify reports that path, a file inside the project at root, was written,
// renamed or removed. It returns at once; the index catches up in the
// background within notifyDebounce and one pass. It is what the store calls
// for its own writes, which the watcher deliberately never reports. A path the
// index does not read (history, run state, attachments) is ignored, and so is
// a project nothing has opened: its open reconciles anyway.
func (m *Manager) Notify(root, path string) {
	e := m.cached(root)
	if e == nil {
		return
	}
	nodeID, graph, walk, relevant := classifyPath(e.root, path)
	if !relevant {
		return
	}
	m.schedule(e, notifyDebounce, func(e *entry) {
		switch {
		case walk:
			e.pendWalk = true
		case graph:
			e.pendGraph = true
		default:
			e.noteNodeLocked(nodeID)
		}
	})
}

// NotifyNode is Notify for a watcher event, which names a node rather than a
// file. An empty nodeID means graph.yaml changed.
func (m *Manager) NotifyNode(root, nodeID string) {
	e := m.cached(root)
	if e == nil {
		return
	}
	m.schedule(e, notifyDebounce, func(e *entry) {
		if nodeID == "" {
			e.pendGraph = true
			return
		}
		e.noteNodeLocked(nodeID)
	})
}

// noteNodeLocked adds a node to the next pass. The caller holds e.pendMu.
func (e *entry) noteNodeLocked(nodeID string) {
	if e.pendNodes == nil {
		e.pendNodes = make(map[string]struct{})
	}
	e.pendNodes[nodeID] = struct{}{}
}

// Warm builds the project's index in the background if it has none, so the
// first search after a project opens finds it ready. Without a database it
// does nothing.
func (m *Manager) Warm(root string) {
	if m.database == nil {
		return
	}
	e := m.entryFor(root)
	if e.view.Load() != nil {
		return
	}
	m.schedule(e, 0, func(e *entry) { e.pendWalk = true })
}

// classifyPath says what a change to path means for the index: one node's
// files (nodeID), graph.yaml (graph), something under nodes/ only a walk can
// place (walk), or nothing the index reads (relevant false).
func classifyPath(root, path string) (nodeID string, graph, walk, relevant bool) {
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, false, false
	}
	rel = filepath.ToSlash(rel)
	if rel == "graph.yaml" {
		return "", true, false, true
	}
	if !strings.HasPrefix(rel, "nodes/") {
		return "", false, false, false
	}
	parts := strings.Split(rel, "/")
	name := parts[len(parts)-1]
	if strings.HasPrefix(name, ".") {
		// Temporary and bookkeeping files: the rename that follows is what
		// counts, and it is reported under the real name.
		return "", false, false, false
	}
	for _, part := range parts[1 : len(parts)-1] {
		if strings.HasSuffix(strings.ToLower(part), ".files") {
			return "", false, false, false
		}
	}
	parent := parts[len(parts)-2]
	lowerName, lowerParent := strings.ToLower(name), strings.ToLower(parent)
	switch {
	case strings.HasSuffix(lowerParent, ".pages"):
		return parent[:len(parent)-len(".pages")], false, false, true
	case strings.HasSuffix(lowerName, ".md"):
		return name[:len(name)-len(".md")], false, false, true
	case strings.HasSuffix(lowerName, ".pages"):
		return name[:len(name)-len(".pages")], false, false, true
	case strings.HasSuffix(lowerName, ".files"):
		return "", false, false, false
	}
	// A folder made, moved or removed: only a walk can tell which nodes it
	// held.
	return "", false, true, true
}

// schedule records a change with note and makes sure a worker will act on it,
// starting one after delay if none is running. A worker already running picks
// the change up on its next pass.
func (m *Manager) schedule(e *entry, delay time.Duration, note func(*entry)) {
	e.pendMu.Lock()
	note(e)
	if e.running {
		e.pendMu.Unlock()
		return
	}
	e.running = true
	e.pendMu.Unlock()
	m.mu.Lock()
	m.busy++
	m.mu.Unlock()
	go m.work(e, delay)
}

// work is one project's background worker: it waits, takes everything
// pending, applies it, and goes round again until nothing is left.
func (m *Manager) work(e *entry, delay time.Duration) {
	defer func() {
		m.mu.Lock()
		m.busy--
		if m.busy == 0 {
			m.idle.Broadcast()
		}
		m.mu.Unlock()
	}()
	for {
		if delay > 0 {
			time.Sleep(delay)
		}
		delay = notifyDebounce
		e.pendMu.Lock()
		nodes, graph, walk := e.pendNodes, e.pendGraph, e.pendWalk
		e.pendNodes, e.pendGraph, e.pendWalk = nil, false, false
		if e.dropped.Load() || (len(nodes) == 0 && !graph && !walk) {
			e.running = false
			e.pendMu.Unlock()
			return
		}
		e.pendMu.Unlock()
		safego.Call("search index pass", func() { m.pass(e, nodes, graph, walk) })
	}
}

// pass applies one batch of pending changes under the build lock and
// publishes the result.
func (m *Manager) pass(e *entry, nodes map[string]struct{}, graph, walk bool) {
	ctx, cancel := context.WithTimeout(context.Background(), backgroundPassTimeout)
	defer cancel()
	docs, err := m.docs(ctx)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dropped.Load() {
		return
	}
	if e.index == nil {
		index, err := openIndex(ctx, docs, e.root)
		if err != nil {
			e.dropLocked()
			return
		}
		e.index = index
		e.publishLocked(true)
		return
	}
	if walk {
		// A walk covers the graph and every node.
		if _, err := e.index.reconcile(ctx); err != nil {
			e.dropLocked()
			return
		}
		e.publishLocked(true)
		return
	}
	if graph {
		if err := e.index.refreshGraph(ctx); err != nil {
			e.dropLocked()
			return
		}
	}
	for id := range nodes {
		if err := e.index.refreshNode(ctx, id); err != nil {
			e.dropLocked()
			return
		}
	}
	e.publishLocked(false)
}

// Flush is retained for callers that had to ask for the old write-behind cache
// to be written out. Every change is committed as it is made now, so there is
// nothing left to flush.
func (m *Manager) Flush() {}

// Rebuild throws the whole index away and lets the next search rescan.
//
// It is the answer to the index and the files having disagreed for any reason
// at all, and it is safe by construction: every row is derived from Markdown
// and YAML that this deletes nothing of. It empties every project's rows:
// whatever disagreed may not have been confined to one.
func (m *Manager) Rebuild(ctx context.Context) error {
	docs, err := m.docs(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.dropAllLocked()
	m.mu.Unlock()
	return docs.reset(ctx)
}

// Prune forgets every project except the given roots.
//
// The index used to be a file inside each project and went away with it. It is
// rows in a shared database now, so something has to say which projects still
// exist; the workspace is the only thing that knows, and it should say so
// whenever its project list changes.
func (m *Manager) Prune(ctx context.Context, roots []string) error {
	docs, err := m.docs(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		keep[cleanRoot(root)] = struct{}{}
	}
	m.mu.Lock()
	for key, e := range m.entries {
		if _, ok := keep[key]; !ok {
			e.dropped.Store(true)
			delete(m.entries, key)
		}
	}
	m.mu.Unlock()
	return docs.prune(ctx, keep)
}

// Close drops what the cache holds and waits for background work to stop.
// The database belongs to the caller and is left open; the manager stays
// usable, starting again from an empty cache.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.store = nil
	m.dropAllLocked()
	for m.busy > 0 {
		m.idle.Wait()
	}
	m.mu.Unlock()
	return nil
}

// dropAllLocked empties the cache and tells every worker to stop. The caller
// holds m.mu.
func (m *Manager) dropAllLocked() {
	for _, e := range m.entries {
		e.dropped.Store(true)
	}
	m.entries = make(map[string]*entry)
}

// docs returns the store, creating its table on first use.
func (m *Manager) docs(ctx context.Context) (*docStore, error) {
	m.mu.Lock()
	if m.database == nil {
		m.mu.Unlock()
		return nil, ErrNoIndex
	}
	if m.store == nil {
		m.store = newDocStore(m.database)
	}
	docs := m.store
	m.mu.Unlock()
	// Outside the lock: creating the schema is database work, and Manager.mu
	// may not be held across any of that.
	if err := docs.ensure(ctx); err != nil {
		return nil, err
	}
	return docs, nil
}

func cleanRoot(root string) string {
	return filepath.Clean(root)
}

// cached is root's entry if the cache holds one, without creating it.
func (m *Manager) cached(root string) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[cleanRoot(root)]
}

func (m *Manager) entryFor(root string) *entry {
	key := cleanRoot(root)
	m.mu.Lock()
	e := m.entries[key]
	if e == nil {
		e = &entry{root: key}
		m.entries[key] = e
	}
	e.used.Store(time.Now().UnixNano())
	m.evictLocked()
	m.mu.Unlock()
	return e
}

// evictLocked drops the least recently searched projects until the cache is
// inside both bounds. Callers hold m.mu; the entries dropped are never locked
// here, so an eviction cannot stall behind a build.
func (m *Manager) evictLocked() {
	total := int64(0)
	for _, e := range m.entries {
		total += e.size.Load()
	}
	if len(m.entries) <= maxProjects && total <= memoryBudget {
		return
	}
	keys := make([]string, 0, len(m.entries))
	for key := range m.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return m.entries[keys[i]].used.Load() < m.entries[keys[j]].used.Load()
	})
	for _, key := range keys {
		if len(m.entries) <= maxProjects && total <= memoryBudget {
			return
		}
		if len(m.entries) == 1 {
			return
		}
		e := m.entries[key]
		total -= e.size.Load()
		e.dropped.Store(true)
		delete(m.entries, key)
	}
}
