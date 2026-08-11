// The workspace-level owner of every project's index: where the database comes
// from, cache lifetime, freshness policy, and the locking contract.
//
// # Locking contract
//
// Two levels, and they are never held at the same time:
//
//   - Manager.mu guards only the entries map and the lazily opened store. It is
//     taken for the length of a map lookup or an eviction sweep and is always
//     released before any index work begins. No disk I/O, no tokenisation, no
//     query ever runs under it.
//   - entry.mu guards one project's index. Opening, reconciling and querying
//     all hold it, for that project only.
//
// The ordering rule is that Manager.mu may be taken while no entry.mu is held,
// and never the other way round; in practice no code path holds both, so a
// cold project rebuilding cannot block a query against any other project, and
// two projects are searched in parallel. entry.size is atomic precisely so the
// eviction sweep can read a project's footprint without waiting for its lock.
//
// SQLite serialises writers underneath all of this, which is a queue and not a
// correctness problem: the connection pool is one connection, so two projects
// indexing at once take turns instead of colliding.
//
// Both a query handler and the filesystem watcher call in concurrently. Every
// exported method takes the locks itself; nothing on projectIndex or document
// is safe to touch from outside.

package searchindex

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"nodevas/internal/db"
)

const (
	// maxProjects and memoryBudget bound what a long-lived server keeps in RAM.
	// Eviction is free of consequence: what is cached is only the bookkeeping
	// that lets a project be reopened without reading its files, and the rows
	// themselves are in the database.
	maxProjects  = 8
	memoryBudget = int64(64 << 20)
)

// revalidateAfter is how long a query trusts the index without re-walking the
// project (see projectIndex.reconcile for why a walk cannot be skipped
// entirely). It is deliberately short enough to be invisible — a save and the
// search that follows it are separated by a round trip — while still
// collapsing a burst of search-as-you-type keystrokes onto a single walk. It
// is a var only so tests can pin it.
var revalidateAfter = 250 * time.Millisecond

// Manager caches one index per project directory, over one database.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	store   *docStore
	// database is the workspace database, or nil when the caller did not give
	// one and the index has to keep itself.
	database *db.DB
}

type entry struct {
	root string
	size atomic.Int64
	// used is the last search time, read by eviction under Manager.mu.
	used atomic.Int64

	mu        sync.Mutex
	index     *projectIndex
	validated time.Time
}

// NewManager returns a cache with no database of its own.
//
// The index then lives in a private in-memory SQLite database: results are the
// same, and the only thing lost is that the next process has to read the
// project's files again instead of finding its rows already written. Prefer
// NewManagerWithDB, which puts them in the workspace database where they
// survive a restart.
func NewManager() *Manager {
	return &Manager{entries: make(map[string]*entry)}
}

// NewManagerWithDB returns a cache backed by the workspace database.
func NewManagerWithDB(database *db.DB) *Manager {
	return &Manager{entries: make(map[string]*entry), database: database}
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
	docs, err := m.docs(ctx)
	if err != nil {
		return nil, err
	}
	e := m.entryFor(root)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureLocked(ctx, docs); err != nil {
		return nil, err
	}
	e.size.Store(e.index.bytes)
	return e.index.query(ctx, lowered)
}

// Update applies a watcher event. An empty nodeID means graph.yaml changed.
//
// A project with nothing cached is left alone: there is no index to patch, and
// the next search opens a current one anyway.
func (m *Manager) Update(root, nodeID string) {
	m.UpdateContext(context.Background(), root, nodeID)
}

// UpdateContext is Update with a deadline the caller controls.
func (m *Manager) UpdateContext(ctx context.Context, root, nodeID string) {
	m.mu.Lock()
	e := m.entries[cleanRoot(root)]
	m.mu.Unlock()
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
		// The graph went away or stopped parsing. Drop what is cached rather
		// than serve an order that no longer describes anything; the rows stay
		// and the next search reconciles them.
		e.index = nil
		return
	}
	e.validated = time.Now()
	e.size.Store(e.index.bytes)
}

// Flush is retained for callers that had to ask for the old write-behind cache
// to be written out. Every change is committed as it is made now, so there is
// nothing left to flush.
func (m *Manager) Flush() {}

// Rebuild throws the whole index away and lets the next search rescan.
//
// It is the answer to the index and the files having disagreed for any reason
// at all, and it is safe by construction: every row is derived from Markdown
// and YAML that this deletes nothing of. It empties every project's rows
// because a contentless FTS5 table can only be emptied wholesale.
func (m *Manager) Rebuild(ctx context.Context) error {
	docs, err := m.docs(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.entries = make(map[string]*entry)
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
	for key := range m.entries {
		if _, ok := keep[key]; !ok {
			delete(m.entries, key)
		}
	}
	m.mu.Unlock()
	return docs.prune(ctx, keep)
}

// Close releases the in-memory database a Manager built for itself. A database
// passed to NewManagerWithDB belongs to the caller and is left open.
func (m *Manager) Close() error {
	m.mu.Lock()
	docs := m.store
	m.store = nil
	m.entries = make(map[string]*entry)
	m.mu.Unlock()
	if docs == nil {
		return nil
	}
	return docs.close()
}

// docs returns the store, opening the private in-memory one on first use.
func (m *Manager) docs(ctx context.Context) (*docStore, error) {
	m.mu.Lock()
	if m.store == nil {
		var err error
		if m.database != nil {
			m.store = newDocStore(m.database)
		} else if m.store, err = newMemoryDocStore(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
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
		delete(m.entries, key)
	}
}

// ensureLocked brings the entry's index up to date. The caller holds e.mu.
func (e *entry) ensureLocked(ctx context.Context, docs *docStore) error {
	if e.index == nil {
		index, err := openIndex(ctx, docs, e.root)
		if err != nil {
			return err
		}
		e.index = index
		e.validated = time.Now()
		return nil
	}
	if !e.validated.IsZero() && time.Since(e.validated) < revalidateAfter {
		return nil
	}
	if _, err := e.index.reconcile(ctx); err != nil {
		// The project moved out from under us. Reopen from scratch so the error
		// surfaces only if the project is genuinely unreadable.
		index, reopenErr := openIndex(ctx, docs, e.root)
		if reopenErr != nil {
			e.index = nil
			return reopenErr
		}
		e.index = index
	}
	e.validated = time.Now()
	return nil
}
