// The per-project index: how a project is read off disk into the database, how
// one node is refreshed in place, and how a query is answered.
//
// What lives in memory is only what it takes to decide that nothing has to be
// read: the graph's order, and per node its title, its metadata line and the
// freshness stamp of the files it was built from. The text and the tokens are
// in PostgreSQL (see persist.go).
//
// Nothing in this file locks. Every method here is called with the owning
// entry's mutex held (see manager.go for the locking contract).

package searchindex

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

const (
	// nodeContentLimit and pageContentLimit mirror what the direct scan reads,
	// so the index does not silently see less of a file than the fallback.
	nodeContentLimit = 2 << 20
	pageContentLimit = 2 << 20
	// docTextLimit bounds what one node contributes. The old index had a
	// whole-project budget and failed the project past it, which handed every
	// large workspace to the uncached direct scan forever. Truncating one
	// oversized document is a far smaller loss than that.
	docTextLimit = 1 << 20
)

// Match is one document that satisfied a query. Text is the indexed haystack,
// handed back so the caller can extract a snippet from it without re-reading
// the file.
type Match struct {
	NodeID string
	Title  string
	Text   string
}

// fileStamp is the identity of a file as cheaply as stat can tell it.
type fileStamp struct {
	Path    string
	Exists  bool
	Size    int64
	ModTime int64
}

func stampFile(root, path string) fileStamp {
	clean := filepath.Clean(path)
	info, err := store.StatProjectPath(root, path)
	if err != nil {
		return fileStamp{Path: clean}
	}
	return fileStamp{
		Path:    clean,
		Exists:  true,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
}

// freshness is the value stored in search_documents.modified_at: the newest
// modification time of the files a node was built from, then how many there
// were and how much they held.
//
// The time comes first because that is the column's name and what an operator
// reading the row wants to see. The two counts are there because a time alone
// cannot see a subpage being deleted, or a file rewritten to a different length
// inside one filesystem timestamp tick.
func freshness(doc fileStamp, pages []fileStamp) string {
	newest := int64(0)
	files := 0
	bytes := int64(0)
	for _, stamp := range append([]fileStamp{doc}, pages...) {
		if !stamp.Exists {
			continue
		}
		files++
		bytes += stamp.Size
		if stamp.ModTime > newest {
			newest = stamp.ModTime
		}
	}
	if files == 0 {
		return ""
	}
	return time.Unix(0, newest).UTC().Format(time.RFC3339Nano) +
		" " + strconv.Itoa(files) + " files " + strconv.FormatInt(bytes, 10) + " bytes"
}

// document is what memory keeps of a node: enough to tell that the row in the
// database is still the right one.
type document struct {
	id    string
	title string
	meta  string
	path  string
	stamp string
}

func (d *document) size() int64 {
	return int64(len(d.id) + len(d.title) + len(d.meta) + len(d.path) + len(d.stamp))
}

// projectIndex is one project's half of the index: the part that cannot be
// derived from the database rows alone, because it is the graph's order.
type projectIndex struct {
	// project is the key every row of this project carries. It is the cleaned
	// root path, so two workspaces sharing a database cannot collide.
	project string
	root    string
	store   *docStore
	graph   fileStamp
	order   []string
	docs    map[string]*document
	bytes   int64

	// pending is what write queued and flush has not yet written, at most one
	// entry per node (pendingAt indexes it). A cold project is thousands of
	// nodes, and one transaction each would spend the build waiting on
	// commits; batches of them spend it indexing.
	pending      []pendingWrite
	pendingAt    map[string]int
	pendingBytes int
}

// pendingWrite is one node queued for the database.
type pendingWrite struct {
	doc   *storedDoc
	stamp string
}

const (
	// writeBatch and writeBatchBytes bound one flush: enough nodes to make the
	// commit cheap, few enough bytes that one transaction stays small.
	writeBatch      = 64
	writeBatchBytes = 4 << 20
)

func newProjectIndex(root string, docs *docStore) *projectIndex {
	clean := cleanRoot(root)
	return &projectIndex{
		project: clean,
		root:    clean,
		store:   docs,
		docs:    make(map[string]*document, 64),
	}
}

// ---------- building ----------

// openIndex brings a project's rows up to date and returns the memory half.
//
// There is no separate "load" and "build" any more: what was cached is what the
// database already holds, and reconciling it with the files on disk is the same
// work whether the rows are minutes or months old.
func openIndex(ctx context.Context, docs *docStore, root string) (*projectIndex, error) {
	ix := newProjectIndex(root, docs)
	stored, err := docs.stored(ctx, ix.project)
	if err != nil {
		return nil, err
	}
	for id, doc := range stored {
		ix.setDoc(&document{id: id, title: doc.title, meta: doc.meta, path: doc.path, stamp: doc.stamp})
	}
	if err := ix.refreshGraph(ctx); err != nil {
		return nil, err
	}
	if _, err := ix.reconcile(ctx); err != nil {
		return nil, err
	}
	return ix, nil
}

func (ix *projectIndex) setDoc(doc *document) {
	if old := ix.docs[doc.id]; old != nil {
		ix.bytes -= old.size()
	}
	ix.docs[doc.id] = doc
	ix.bytes += doc.size()
}

func (ix *projectIndex) forget(id string) {
	if old := ix.docs[id]; old != nil {
		ix.bytes -= old.size()
		delete(ix.docs, id)
	}
}

func userNames(graph *engine.Graph) map[string]string {
	names := make(map[string]string, len(graph.Users))
	for _, user := range graph.Users {
		names[user.ID] = user.Name
	}
	return names
}

// metadataText is the searchable projection of a node's graph fields. Its
// shape is load-bearing: snippets are cut out of the same string the old
// index built, so results stay identical from the client's side.
func metadataText(node *engine.Node, names map[string]string) string {
	return strings.Join([]string{
		node.ID,
		node.Title,
		node.Kind,
		node.Priority,
		names[node.Assignee],
		strings.Join(node.Tags, " "),
		node.Requires,
	}, " ")
}

// readBody reads a node's markdown and its text subpages. It never touches
// graph.yaml, which is what makes a watcher-driven node update cheap.
func readBody(root, id string) (string, fileStamp, []fileStamp) {
	nodePath := store.NodeDocPath(root, id)
	content := readCapped(root, nodePath, nodeContentLimit)
	pages, _ := pageStamps(root, store.NodePagesPath(root, id))
	var body strings.Builder
	body.WriteString(content)
	for _, page := range pages {
		data, err := store.ReadProjectFileLimit(root, page.Path, pageContentLimit)
		if err != nil {
			continue
		}
		remaining := pageContentLimit - body.Len()
		if len(data) > remaining {
			data = data[:remaining]
		}
		body.WriteByte('\n')
		body.Write(data)
		if body.Len() >= pageContentLimit {
			break
		}
	}
	return body.String(), stampFile(root, nodePath), pages
}

func readCapped(root, path string, limit int64) string {
	data, err := store.ReadProjectFileLimit(root, path, limit)
	if err != nil {
		return ""
	}
	return string(data)
}

// truncateRunes cuts to at most limit bytes without splitting a rune, so a
// truncated document cannot poison tokenisation with a replacement char.
func truncateRunes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func pageStamps(root, dir string) ([]fileStamp, error) {
	entries, err := store.ReadProjectDir(root, dir)
	if err != nil {
		return nil, err
	}
	stamps := make([]fileStamp, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		format, ok := store.PageFormatFromFilename(entry.Name())
		if !ok || !store.PageFormatIsText(format) {
			continue
		}
		stamps = append(stamps, stampFile(root, filepath.Join(dir, entry.Name())))
	}
	return stamps, nil
}

// ---------- writing ----------

// write indexes one node: it queues the text and the tokens, together with
// what the row was built from, for the next flush. Every caller flushes before
// it returns, so a write is in the database by the time the method that made
// it is done.
func (ix *projectIndex) write(ctx context.Context, id, title, meta, body string, doc fileStamp, pages []fileStamp) error {
	meta = truncateRunes(meta, docTextLimit)
	budget := docTextLimit - len(meta) - 1
	if budget < 0 {
		budget = 0
	}
	body = truncateRunes(body, budget)
	stamp := freshness(doc, pages)
	path := relativePath(ix.root, doc.Path)
	return ix.queue(ctx, &storedDoc{nodeID: id, title: title, path: path, meta: meta, body: body}, stamp)
}

// queue adds a node to the pending batch, replacing an earlier write of the
// same node, and flushes when the batch is full.
func (ix *projectIndex) queue(ctx context.Context, doc *storedDoc, stamp string) error {
	if ix.pendingAt == nil {
		ix.pendingAt = make(map[string]int, writeBatch)
	}
	write := pendingWrite{doc: doc, stamp: stamp}
	if at, ok := ix.pendingAt[doc.nodeID]; ok {
		ix.pendingBytes -= len(ix.pending[at].doc.meta) + len(ix.pending[at].doc.body)
		ix.pending[at] = write
	} else {
		ix.pendingAt[doc.nodeID] = len(ix.pending)
		ix.pending = append(ix.pending, write)
	}
	ix.pendingBytes += len(doc.meta) + len(doc.body)
	if len(ix.pending) >= writeBatch || ix.pendingBytes >= writeBatchBytes {
		return ix.flush(ctx)
	}
	return nil
}

// flush writes the pending batch in one transaction and only then records the
// nodes in memory: a batch that failed leaves memory saying the rows are old,
// so the next reconcile writes them again.
func (ix *projectIndex) flush(ctx context.Context) error {
	if len(ix.pending) == 0 {
		return nil
	}
	batch := ix.pending
	ix.pending, ix.pendingBytes = nil, 0
	clear(ix.pendingAt)
	if err := ix.store.putMany(ctx, ix.project, batch); err != nil {
		return err
	}
	for _, write := range batch {
		doc := write.doc
		ix.setDoc(&document{id: doc.nodeID, title: doc.title, meta: doc.meta, path: doc.path, stamp: write.stamp})
	}
	return nil
}

// reindex re-reads a node's files and writes it.
func (ix *projectIndex) reindex(ctx context.Context, id, title, meta string) error {
	body, doc, pages := readBody(ix.root, id)
	return ix.write(ctx, id, title, meta, body, doc, pages)
}

// relativePath is what search_documents.path holds: where the node's markdown
// lives inside the project, in slash form, so the row reads the same on every
// platform and means something to whoever opens the database.
func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// ---------- querying ----------

// query returns every document whose stored text contains the (already
// lowercased) query, in graph order.
//
// Two stages, and the second one is not optional. The database is searching
// tokens this package produced, and those tokens are character bigrams: a
// document holding "ab" and "bc" anywhere matches the bigrams of "abc" without
// containing "abc". Where the candidates come from changed, not that they are
// candidates, so containsFold still has to see the query as a substring of the
// text before a document is a result.
func (ix *projectIndex) query(ctx context.Context, lowered string) ([]Match, error) {
	candidates, err := ix.store.candidates(ctx, ix.project, queryTokens(lowered))
	if err != nil {
		return nil, err
	}
	verified := make(map[string]*storedDoc, len(candidates))
	for _, doc := range candidates {
		if containsFold(doc.text(), lowered) {
			verified[doc.nodeID] = doc
		}
	}
	out := make([]Match, 0, len(verified))
	for _, id := range ix.order {
		doc, ok := verified[id]
		if !ok {
			continue
		}
		out = append(out, Match{NodeID: id, Title: doc.title, Text: doc.text()})
	}
	return out, nil
}

// ---------- incremental refresh ----------

// refreshNode re-reads one node's files. A node the index has never heard of
// means the graph gained it, so the graph is reconciled instead.
func (ix *projectIndex) refreshNode(ctx context.Context, id string) error {
	doc, ok := ix.docs[id]
	if !ok {
		return ix.refreshGraph(ctx)
	}
	if err := ix.reindex(ctx, id, doc.title, doc.meta); err != nil {
		return err
	}
	return ix.flush(ctx)
}

// refreshGraph re-parses graph.yaml and reconciles the document set with it.
// A node whose metadata line is unchanged is left alone, so a graph edit that
// touched one node costs one YAML parse and no node reads at all.
func (ix *projectIndex) refreshGraph(ctx context.Context) error {
	graphPath := filepath.Join(ix.root, "graph.yaml")
	data, err := store.ReadProjectFile(ix.root, graphPath)
	if err != nil {
		return fmt.Errorf("read graph: %w", err)
	}
	graph, err := engine.ParseGraph(data)
	if err != nil {
		return fmt.Errorf("parse graph: %w", err)
	}
	names := userNames(graph)
	live := make(map[string]struct{}, len(graph.Nodes))
	order := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		if _, seen := live[node.ID]; seen {
			continue
		}
		live[node.ID] = struct{}{}
		order = append(order, node.ID)
		meta := metadataText(node, names)
		old, known := ix.docs[node.ID]
		if !known {
			if err := ix.reindex(ctx, node.ID, node.Title, meta); err != nil {
				return err
			}
			continue
		}
		if old.meta == meta && old.title == node.Title {
			continue
		}
		// Only the metadata moved. The body is already stored, so it is
		// rewritten from the database rather than re-read from the files.
		if err := ix.rewriteMeta(ctx, old, node.Title, meta); err != nil {
			return err
		}
	}
	if err := ix.flush(ctx); err != nil {
		return err
	}
	for id := range ix.docs {
		if _, ok := live[id]; !ok {
			if err := ix.store.remove(ctx, ix.project, id); err != nil {
				return err
			}
			ix.forget(id)
		}
	}
	ix.order = order
	ix.graph = stampFile(ix.root, graphPath)
	return nil
}

// rewriteMeta replaces a node's metadata without touching its files.
func (ix *projectIndex) rewriteMeta(ctx context.Context, old *document, title, meta string) error {
	body, err := ix.store.body(ctx, ix.project, old.id)
	if err != nil {
		return err
	}
	meta = truncateRunes(meta, docTextLimit)
	return ix.queue(ctx, &storedDoc{nodeID: old.id, title: title, path: old.path, meta: meta, body: body}, old.stamp)
}

// ---------- the freshness net ----------

// nodeFiles is what one walk of nodes/ found for a node.
type nodeFiles struct {
	doc   fileStamp
	pages []fileStamp
}

// scanNodes walks nodes/ once and collects the stamps of every node document
// and every text subpage.
//
// One walk, not two stats per node: the old check paid an os.Stat plus an
// os.ReadDir for every node on every keystroke. A walk reads each directory
// once and takes the file metadata from the directory entry, so the syscall
// count follows the number of directories rather than the number of nodes.
func scanNodes(root string) map[string]*nodeFiles {
	nodesDir := filepath.Join(root, "nodes")
	found := make(map[string]*nodeFiles, 64)
	if err := store.ValidateProjectTree(root, nodesDir); err != nil {
		return found
	}
	at := func(id string) *nodeFiles {
		if existing, ok := found[id]; ok {
			return existing
		}
		fresh := &nodeFiles{}
		found[id] = fresh
		return fresh
	}
	_ = filepath.WalkDir(nodesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil //nolint:nilerr // a vanished directory is just "not there"
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if entry.IsDir() {
			if path == nodesDir {
				return nil
			}
			// Attachments hold no searchable text, and dot-directories are
			// bookkeeping. Neither was ever indexed.
			if strings.HasSuffix(lower, ".files") || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		parent := filepath.Base(filepath.Dir(path))
		if strings.HasSuffix(strings.ToLower(parent), ".pages") {
			format, ok := store.PageFormatFromFilename(name)
			if !ok || !store.PageFormatIsText(format) {
				return nil
			}
			id := parent[:len(parent)-len(".pages")]
			node := at(id)
			node.pages = append(node.pages, stampEntry(path, entry))
			return nil
		}
		if strings.HasSuffix(lower, ".md") {
			at(name[:len(name)-len(".md")]).doc = stampEntry(path, entry)
		}
		return nil
	})
	return found
}

func stampEntry(path string, entry fs.DirEntry) fileStamp {
	info, err := entry.Info()
	if err != nil {
		return fileStamp{Path: filepath.Clean(path)}
	}
	return fileStamp{
		Path:    filepath.Clean(path),
		Exists:  true,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
}

// reconcile catches changes the watcher never reported. It has to exist and it
// has to run often: writes the server makes itself are marked as self-writes
// and are deliberately never re-emitted by the watcher, so this is the only
// thing that sees them. The watcher path still earns its keep by refreshing a
// node the instant an outside editor touches it, and by leaving the stamps
// current so this walk finds nothing to do.
//
// It is also what a cold process does with rows a previous one wrote: the
// stamp comparison is the whole point of search_documents.modified_at, and a
// project nobody has edited since it was last indexed is reopened without a
// single node file being read.
func (ix *projectIndex) reconcile(ctx context.Context) (bool, error) {
	changed := false
	graphPath := filepath.Join(ix.root, "graph.yaml")
	if stampFile(ix.root, graphPath) != ix.graph {
		if err := ix.refreshGraph(ctx); err != nil {
			return false, err
		}
		changed = true
	}
	found := scanNodes(ix.root)
	for _, id := range ix.order {
		doc, ok := ix.docs[id]
		if !ok {
			continue
		}
		var onDisk nodeFiles
		if node := found[id]; node != nil {
			onDisk = *node
		}
		// The path is compared as well as the stamp because a node moved between
		// folders says the same thing from a different file, and its row is
		// keyed by where the file was.
		moved := onDisk.doc.Exists && relativePath(ix.root, onDisk.doc.Path) != doc.path
		if !moved && freshness(onDisk.doc, onDisk.pages) == doc.stamp {
			continue
		}
		if err := ix.reindex(ctx, id, doc.title, doc.meta); err != nil {
			return false, err
		}
		changed = true
	}
	if err := ix.flush(ctx); err != nil {
		return false, err
	}
	return changed, nil
}

// Ranked is one result of a ranked search. Match is the tier it ranked in:
// "title" (the query is the node's title), "phrase" (the text contains the
// whole query) or "terms" (it shares some of the query's terms). Score is
// BM25 (FTS5's bm25() negated), so higher is better; an exact title the
// terms did not reach scores 0.
type Ranked struct {
	NodeID string
	Title  string
	Text   string
	Match  string
	Score  float64
}

// Ranked-search tiers, best first.
const (
	MatchTitle  = "title"
	MatchPhrase = "phrase"
	MatchTerms  = "terms"
)

func matchTier(match string) int {
	switch match {
	case MatchTitle:
		return 0
	case MatchPhrase:
		return 1
	}
	return 2
}

// rank answers a query with the best documents first: an exact title, then
// the whole query as a phrase, then any shared terms, each tier by BM25 and
// then by graph order. Unlike query, a document need not contain every term,
// which is what lets a question typed as a sentence find anything.
func (ix *projectIndex) rank(ctx context.Context, query string, limit int) ([]Ranked, error) {
	trimmed := strings.TrimSpace(query)
	lowered := strings.ToLower(trimmed)
	candidates, err := ix.store.rankedCandidates(ctx, ix.project, rankTokens(lowered), limit)
	if err != nil {
		return nil, err
	}
	position := make(map[string]int, len(ix.order))
	for index, id := range ix.order {
		position[id] = index
	}
	byID := make(map[string]*rankedDoc, len(candidates))
	for _, doc := range candidates {
		if _, live := position[doc.nodeID]; live {
			byID[doc.nodeID] = doc
		}
	}
	// An exact title always comes first, even when the terms did not fetch it.
	for _, id := range ix.order {
		doc := ix.docs[id]
		if doc == nil || byID[id] != nil || !strings.EqualFold(strings.TrimSpace(doc.title), trimmed) {
			continue
		}
		stored, err := ix.store.document(ctx, ix.project, id)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			byID[id] = &rankedDoc{storedDoc: *stored}
		}
	}
	out := make([]Ranked, 0, len(byID))
	for id, doc := range byID {
		text := doc.text()
		match := MatchTerms
		switch {
		case strings.EqualFold(strings.TrimSpace(doc.title), trimmed):
			match = MatchTitle
		case containsFold(text, lowered):
			match = MatchPhrase
		}
		out = append(out, Ranked{NodeID: id, Title: doc.title, Text: text, Match: match, Score: doc.score})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if ta, tb := matchTier(out[a].Match), matchTier(out[b].Match); ta != tb {
			return ta < tb
		}
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return position[out[a].NodeID] < position[out[b].NodeID]
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
