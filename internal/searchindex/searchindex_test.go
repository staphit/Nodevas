package searchindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
)

type fixtureNode struct {
	id    string
	title string
	body  string
	pages map[string]string
}

// writeProject lays out a minimal project on disk and returns its root.
func writeProject(t *testing.T, nodes ...fixtureNode) string {
	t.Helper()
	root := t.TempDir()
	writeGraph(t, root, nodes...)
	nodesDir := filepath.Join(root, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if err := os.WriteFile(filepath.Join(nodesDir, node.id+".md"), []byte(node.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if len(node.pages) == 0 {
			continue
		}
		pageDir := filepath.Join(nodesDir, node.id+".pages")
		if err := os.MkdirAll(pageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range node.pages {
			if err := os.WriteFile(filepath.Join(pageDir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func writeGraph(t *testing.T, root string, nodes ...fixtureNode) {
	t.Helper()
	graph := &engine.Graph{Version: 1}
	for _, node := range nodes {
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: node.id, Title: node.title})
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// openDB gives a test its own workspace database.
func openDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.OpenAt(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// newManager is the usual setup: a manager over a real database file.
func newManager(t *testing.T) *Manager {
	t.Helper()
	return NewManagerWithDB(openDB(t, filepath.Join(t.TempDir(), db.FileName)))
}

func ids(matches []Match) []string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.NodeID)
	}
	return out
}

func search(t *testing.T, m *Manager, root, query string) []Match {
	t.Helper()
	matches, err := m.Search(root, strings.ToLower(query))
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return matches
}

func count(t *testing.T, database *db.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ---------- persistence ----------

// The rows a previous process wrote are the cache now, and the point of
// modified_at is that reopening a project re-reads nothing. Asserted by lying
// to the index: the file's content changes while its stamp does not, so a
// process that reads it again gets a different answer from one that trusts the
// row, and only the second answer can pass.
func TestStoredRowsAreReusedWhenTheStampIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "the quick brown fox"},
		fixtureNode{id: "beta", title: "Beta", body: "北京大學的研究"},
	)
	first := NewManagerWithDB(openDB(t, path))
	if got := ids(search(t, first, root, "quick")); len(got) != 1 {
		t.Fatalf("first search = %v", got)
	}

	nodePath := filepath.Join(root, "nodes", "alpha.md")
	info, err := os.Stat(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodePath, []byte("the quick brown cat"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nodePath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	second := NewManagerWithDB(openDB(t, path))
	if got := ids(search(t, second, root, "brown fox")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("the stored row was not reused: %v", got)
	}
	if got := ids(search(t, second, root, "京大")); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("a CJK substring did not survive the restart: %v", got)
	}
}

// A changed file is picked up across a restart even though nothing told the
// index about it, which is the other half of what modified_at is for.
func TestAChangedFileIsReindexedOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "before body"})
	first := NewManagerWithDB(openDB(t, path))
	search(t, first, root, "before")

	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("after body"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := NewManagerWithDB(openDB(t, path))
	if got := ids(search(t, second, root, "after")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("an edit made while the index was closed was missed: %v", got)
	}
	if got := ids(search(t, second, root, "before")); len(got) != 0 {
		t.Fatalf("stale content after reopen: %v", got)
	}
}

// The tables are derived data an operator is allowed to delete, empty or drop.
// None of that may surface as an error, and a rescan has to put it back.
func TestDamagedTablesAreRebuiltByARescan(t *testing.T) {
	cases := map[string]func(t *testing.T, database *db.DB){
		"documents emptied": func(t *testing.T, database *db.DB) {
			if _, err := database.Exec(`DELETE FROM search_documents`); err != nil {
				t.Fatal(err)
			}
		},
		"postings emptied": func(t *testing.T, database *db.DB) {
			if _, err := database.Exec(`INSERT INTO search_fts (search_fts) VALUES ('delete-all')`); err != nil {
				t.Fatal(err)
			}
		},
		"content dropped": func(t *testing.T, database *db.DB) {
			if _, err := database.Exec(`DROP TABLE search_content`); err != nil {
				t.Fatal(err)
			}
		},
		"fts dropped": func(t *testing.T, database *db.DB) {
			if _, err := database.Exec(`DROP TABLE search_fts`); err != nil {
				t.Fatal(err)
			}
		},
		"everything dropped": func(t *testing.T, database *db.DB) {
			for _, table := range []string{"search_content", "search_fts", "search_documents"} {
				if _, err := database.Exec(`DROP TABLE ` + table); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), db.FileName)
			root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "buried needle 埋著的針"})
			database := openDB(t, path)
			first := NewManagerWithDB(database)
			search(t, first, root, "needle")

			damage(t, database)

			second := NewManagerWithDB(openDB(t, path))
			for _, query := range []string{"needle", "著的"} {
				got := ids(search(t, second, root, query))
				if len(got) != 1 || got[0] != "alpha" {
					t.Fatalf("after %s, query %q = %v", name, query, got)
				}
			}
		})
	}
}

// Rebuild is the deliberate version of the same thing.
func TestRebuildEmptiesTheIndexAndTheNextSearchRestoresIt(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "buried needle"})
	manager := newManager(t)
	search(t, manager, root, "needle")
	if err := manager.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 0 {
		t.Fatalf("rebuild left %d rows", n)
	}
	if got := ids(search(t, manager, root, "needle")); len(got) != 1 {
		t.Fatalf("the rescan after a rebuild found %v", got)
	}
}

// A manager with no database of its own still answers, it just starts cold.
func TestManagerWithoutADatabaseStillSearches(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "私有記憶體 index"})
	manager := NewManager()
	t.Cleanup(func() { manager.Close() })
	if got := ids(search(t, manager, root, "記憶")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("in-memory manager = %v", got)
	}
}

// ---------- one document, one row ----------

func TestReindexingTheSameDocumentDoesNotDoubleIt(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "重複的內容 duplicated"})
	manager := newManager(t)
	search(t, manager, root, "duplicated")
	for range 3 {
		manager.Update(root, "alpha")
	}
	for _, query := range []string{"duplicated", "重複"} {
		if got := ids(search(t, manager, root, query)); len(got) != 1 {
			t.Fatalf("query %q returned %v after reindexing", query, got)
		}
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 1 {
		t.Fatalf("search_documents holds %d rows for one node", n)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_content`); n != 1 {
		t.Fatalf("search_content holds %d rows for one node", n)
	}
	if n := count(t, manager.database,
		`SELECT count(*) FROM search_fts WHERE search_fts MATCH ?`, matchExpression([]string{"重複"})); n != 1 {
		t.Fatalf("search_fts matched %d rows for one node", n)
	}
}

// A node that vanishes from the graph takes its rows and its postings with it.
// Whether the postings really went is asserted by the consistency check a fresh
// store runs: it wipes the index when the tables disagree about how many
// documents there are, so a leftover posting shows up as an empty index.
func TestADeletedNodeLeavesNothingBehind(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "shared body"},
		fixtureNode{id: "beta", title: "Beta", body: "shared body"},
	)
	manager := newManager(t)
	if got := ids(search(t, manager, root, "shared")); len(got) != 2 {
		t.Fatalf("initial results = %v", got)
	}
	writeGraph(t, root, fixtureNode{id: "alpha", title: "Alpha"})
	manager.Update(root, "")
	if got := ids(search(t, manager, root, "shared")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("after the deletion results = %v", got)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 1 {
		t.Fatalf("search_documents kept %d rows", n)
	}

	fresh := &docStore{sql: manager.database.Writer(), read: manager.database.Reader()}
	if err := fresh.ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 1 {
		t.Fatalf("the consistency check wiped the index: %d rows left", n)
	}
}

// ---------- incremental updates ----------

func TestWatcherUpdateReplacesNodeContent(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "before body"})
	manager := newManager(t)
	if got := ids(search(t, manager, root, "before")); len(got) != 1 {
		t.Fatalf("initial results = %v", got)
	}

	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("after body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The watcher path: no waiting on the revalidation window.
	manager.Update(root, "alpha")

	if got := ids(search(t, manager, root, "after")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("new content not found: %v", got)
	}
	if got := ids(search(t, manager, root, "before")); len(got) != 0 {
		t.Fatalf("old content still returned: %v", got)
	}
}

func TestGraphUpdateAddsRemovesAndRetitles(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "shared body"},
		fixtureNode{id: "beta", title: "Beta", body: "shared body"},
	)
	manager := newManager(t)
	if got := ids(search(t, manager, root, "shared")); len(got) != 2 {
		t.Fatalf("initial results = %v", got)
	}

	// Drop beta, rename alpha, add gamma.
	writeGraph(t, root,
		fixtureNode{id: "alpha", title: "Renamed Alpha"},
		fixtureNode{id: "gamma", title: "Gamma"},
	)
	if err := os.WriteFile(filepath.Join(root, "nodes", "gamma.md"), []byte("shared body"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager.Update(root, "")

	if got := ids(search(t, manager, root, "shared")); len(got) != 2 || got[0] != "alpha" || got[1] != "gamma" {
		t.Fatalf("after graph change results = %v", got)
	}
	matches := search(t, manager, root, "renamed alpha")
	if len(matches) != 1 || matches[0].Title != "Renamed Alpha" {
		t.Fatalf("new title not searchable: %+v", matches)
	}
	// The body survived the metadata swap without being re-read from a graph
	// the node's markdown never changed under.
	if !strings.Contains(matches[0].Text, "shared body") {
		t.Fatalf("body lost on graph refresh: %q", matches[0].Text)
	}
}

// The server marks its own writes so the watcher never re-emits them, so the
// walk-based net is the only thing that sees an edit the API made.
func TestReconcileCatchesAnUnannouncedEdit(t *testing.T) {
	previous := revalidateAfter
	revalidateAfter = 0
	t.Cleanup(func() { revalidateAfter = previous })

	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "before body"})
	manager := newManager(t)
	if got := ids(search(t, manager, root, "before")); len(got) != 1 {
		t.Fatalf("initial results = %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("after body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ids(search(t, manager, root, "after")); len(got) != 1 {
		t.Fatalf("unannounced edit not picked up: %v", got)
	}
	if got := ids(search(t, manager, root, "before")); len(got) != 0 {
		t.Fatalf("stale content after unannounced edit: %v", got)
	}
}

func TestSubpagesAreIndexedAndRefreshed(t *testing.T) {
	previous := revalidateAfter
	revalidateAfter = 0
	t.Cleanup(func() { revalidateAfter = previous })

	root := writeProject(t, fixtureNode{
		id: "alpha", title: "Alpha", body: "body",
		pages: map[string]string{"one.md": "subpage lantern"},
	})
	manager := newManager(t)
	if got := ids(search(t, manager, root, "lantern")); len(got) != 1 {
		t.Fatalf("subpage text not indexed: %v", got)
	}
	page := filepath.Join(root, "nodes", "alpha.pages", "one.md")
	if err := os.WriteFile(page, []byte("subpage beacon"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ids(search(t, manager, root, "beacon")); len(got) != 1 {
		t.Fatalf("edited subpage not picked up: %v", got)
	}
	// A deleted subpage leaves no newer file behind, so only the file count in
	// the stamp can notice it.
	if err := os.Remove(page); err != nil {
		t.Fatal(err)
	}
	if got := ids(search(t, manager, root, "beacon")); len(got) != 0 {
		t.Fatalf("a deleted subpage is still searchable: %v", got)
	}
}

// ---------- tokenisation ----------

func TestCJKSubstringIsFoundMidWord(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "北京大學", body: "這是一段關於機器學習的中文說明。"},
		fixtureNode{id: "beta", title: "Beta", body: "unrelated english text"},
	)
	manager := newManager(t)
	// One character, two characters, three, and a run that starts mid-word.
	for _, query := range []string{"習", "京大", "機器學習", "段關於"} {
		got := ids(search(t, manager, root, query))
		if len(got) != 1 || got[0] != "alpha" {
			t.Errorf("query %q = %v, want [alpha]", query, got)
		}
	}
}

func TestMixedChineseAndEnglishQuery(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "設計 Spec", body: "介面 API 的設計說明"},
		fixtureNode{id: "beta", title: "Other", body: "介面 without the english word"},
		fixtureNode{id: "gamma", title: "Third", body: "API without the chinese word"},
	)
	manager := newManager(t)
	got := ids(search(t, manager, root, "介面 api"))
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("mixed query = %v, want [alpha]", got)
	}
	if got := ids(search(t, manager, root, "設計")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("cjk-only query = %v", got)
	}
}

// Bigrams cannot tell "abc" from a document that merely holds "ab" and "bc"
// somewhere, and FTS5 indexing those bigrams inherits the problem exactly. The
// verification pass is what keeps it from reaching anyone.
func TestBigramFalsePositivesAreFilteredOut(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "decoy", title: "Decoy", body: "乙丙 甲乙 and: bc ab"},
		fixtureNode{id: "real", title: "Real", body: "甲乙丙 and: abc"},
	)
	manager := newManager(t)
	search(t, manager, root, "甲乙丙")

	// The decoy must be a candidate, or the test is proving nothing.
	docs, err := manager.docs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := docs.candidates(context.Background(), cleanRoot(root), queryTokens("甲乙丙"))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("FTS5 offered %d candidates; the fixture no longer tests the filter", len(candidates))
	}

	for _, query := range []string{"甲乙丙", "abc"} {
		got := ids(search(t, manager, root, query))
		if len(got) != 1 || got[0] != "real" {
			t.Errorf("query %q = %v, want [real] only", query, got)
		}
	}
}

func TestCaseIsFoldedWithoutLoweringDocuments(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "MiXeD CaSe HeAdInG"})
	manager := newManager(t)
	matches := search(t, manager, root, "mixed case")
	if len(matches) != 1 {
		t.Fatalf("case-insensitive match failed: %v", ids(matches))
	}
	if !strings.Contains(matches[0].Text, "MiXeD") {
		t.Fatalf("stored text was lowercased: %q", matches[0].Text)
	}
}

func TestQueryOfPunctuationOnlyStillVerifies(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "a -- b"},
		fixtureNode{id: "beta", title: "Beta", body: "no dashes here"},
	)
	manager := newManager(t)
	if got := ids(search(t, manager, root, "--")); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("token-free query = %v, want [alpha]", got)
	}
}

// FTS5 has a query language of its own, and none of it may be reachable from
// the search box: a quote, a star, a minus, parentheses or the word NEAR are
// text to be found, not syntax to be obeyed.
func TestFTS5SyntaxInAQueryIsTreatedAsText(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: `release "v2" NEAR (beta) OR -x AND * done`},
		fixtureNode{id: "beta", title: "Beta", body: "release notes for something else"},
	)
	manager := newManager(t)

	// Present as text: found, and only in the document that has it.
	for _, query := range []string{`"v2"`, `near (beta)`, `or -x`, `* done`, `release "v2" near`} {
		got := ids(search(t, manager, root, query))
		if len(got) != 1 || got[0] != "alpha" {
			t.Errorf("query %q = %v, want [alpha]", query, got)
		}
	}
	// Absent as text: found nowhere, however it would read as an expression.
	for _, query := range []string{`"release" OR "notes"`, `rel*`, `(release NEAR notes)`, `""""`, `^release`} {
		if got := ids(search(t, manager, root, query)); len(got) != 0 {
			t.Errorf("query %q = %v, want no results", query, got)
		}
	}
	// And a query that is nothing but syntax must not error.
	if _, err := manager.Search(root, `*"()-`); err != nil {
		t.Fatalf("a syntax-only query errored: %v", err)
	}
}

func TestContainsFoldMatchesStringsToLower(t *testing.T) {
	texts := []string{"Hello World", "ÄÖÜ straße", "北京大學", "MiXeD", ""}
	queries := []string{"hello", "world", "äöü", "straße", "京大", "mixed", "zzz", ""}
	for _, text := range texts {
		for _, query := range queries {
			want := strings.Contains(strings.ToLower(text), query)
			if got := containsFold(text, query); got != want {
				t.Errorf("containsFold(%q, %q) = %v, want %v", text, query, got, want)
			}
		}
	}
}

// Retracting a document from a contentless FTS5 table means handing back the
// same string that was indexed, so the token order cannot come from a map.
func TestIndexTokensAreDeterministic(t *testing.T) {
	text := "甲乙丙丁 the quick brown fox 北京大學"
	first := indexTokens(text)
	for range 20 {
		if got := indexTokens(text); got != first {
			t.Fatalf("indexTokens is not deterministic:\n%q\n%q", first, got)
		}
	}
	if !strings.Contains(" "+first+" ", " 甲乙 ") || !strings.Contains(" "+first+" ", " 甲 ") {
		t.Fatalf("bigrams and single characters are not both present: %q", first)
	}
}

// ---------- locking ----------

// A cold or rebuilding project must not stop another project being searched.
// Asserted structurally rather than by timing: project A's index lock is held
// for the whole of project B's search, on this very goroutine. Any design that
// serialised the two behind one mutex would deadlock here instead of failing.
func TestOneProjectsLockDoesNotBlockAnother(t *testing.T) {
	slow := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "slow project"})
	other := writeProject(t, fixtureNode{id: "beta", title: "Beta", body: "other project"})
	manager := newManager(t)

	held := manager.entryFor(slow)
	held.mu.Lock()
	defer held.mu.Unlock()

	got := ids(search(t, manager, other, "other project"))
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("second project = %v while the first was locked", got)
	}
}

func TestConcurrentSearchesAreRaceFree(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "concurrent 併發 body"},
		fixtureNode{id: "beta", title: "Beta", body: "another 併發 body"},
	)
	manager := newManager(t)
	done := make(chan struct{})
	for worker := range 8 {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for range 20 {
				if _, err := manager.Search(root, "併發"); err != nil {
					t.Error(err)
					return
				}
				if worker%2 == 0 {
					manager.Update(root, "alpha")
				}
			}
		}(worker)
	}
	for range 8 {
		<-done
	}
}

// Two projects in one database do not see each other's documents.
func TestProjectsAreIsolatedWithinOneDatabase(t *testing.T) {
	manager := newManager(t)
	first := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "shared needle"})
	second := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "shared needle"})
	if got := ids(search(t, manager, first, "needle")); len(got) != 1 {
		t.Fatalf("first project = %v", got)
	}
	if got := ids(search(t, manager, second, "needle")); len(got) != 1 {
		t.Fatalf("second project = %v", got)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 2 {
		t.Fatalf("two projects wrote %d rows", n)
	}
}

// A project that is gone takes its rows with it, and only its own.
func TestPruneDropsProjectsThatAreNoLongerListed(t *testing.T) {
	manager := newManager(t)
	kept := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "kept 保留"})
	gone := writeProject(t, fixtureNode{id: "beta", title: "Beta", body: "gone 移除"})
	search(t, manager, kept, "kept")
	search(t, manager, gone, "gone")

	if err := manager.Prune(context.Background(), []string{kept}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 1 {
		t.Fatalf("prune left %d rows", n)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_content`); n != 1 {
		t.Fatalf("prune left %d content rows", n)
	}
	if got := ids(search(t, manager, kept, "保留")); len(got) != 1 {
		t.Fatalf("the surviving project = %v", got)
	}
	// The postings went with the rows: a consistency check that found otherwise
	// would empty the index.
	fresh := &docStore{sql: manager.database.Writer(), read: manager.database.Reader()}
	if err := fresh.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, manager.database, `SELECT count(*) FROM search_documents`); n != 1 {
		t.Fatalf("prune left orphaned postings: %d rows after the check", n)
	}
}

// ---------- bounds ----------

func TestCacheEvictsLeastRecentlySearchedProjects(t *testing.T) {
	manager := newManager(t)
	roots := make([]string, 0, maxProjects+3)
	for i := range maxProjects + 3 {
		root := writeProject(t, fixtureNode{
			id: "alpha", title: "Alpha", body: strings.Repeat("x", i+1) + " body",
		})
		roots = append(roots, root)
		search(t, manager, root, "body")
	}
	manager.mu.Lock()
	cached := len(manager.entries)
	_, oldestStillCached := manager.entries[filepath.Clean(roots[0])]
	manager.mu.Unlock()
	if cached > maxProjects {
		t.Fatalf("cache holds %d projects, budget is %d", cached, maxProjects)
	}
	if oldestStillCached {
		t.Fatal("the least recently searched project was not evicted")
	}
	// Evicted is not lost: its rows are still in the database.
	if got := ids(search(t, manager, roots[0], "body")); len(got) != 1 {
		t.Fatalf("evicted project no longer searchable: %v", got)
	}
}

func TestOversizedDocumentIsTruncatedNotFatal(t *testing.T) {
	body := strings.Repeat("padding ", (docTextLimit/8)+1024) + " tail-marker"
	root := writeProject(t,
		fixtureNode{id: "big", title: "Big", body: body},
		fixtureNode{id: "small", title: "Small", body: "small needle"},
	)
	manager := newManager(t)
	// The old index failed the whole project past a budget; the rest of it must
	// stay searchable.
	if got := ids(search(t, manager, root, "small needle")); len(got) != 1 || got[0] != "small" {
		t.Fatalf("a large sibling broke the project: %v", got)
	}
	if got := ids(search(t, manager, root, "padding")); len(got) != 1 || got[0] != "big" {
		t.Fatalf("the truncated document is not searchable at all: %v", got)
	}
	if got := ids(search(t, manager, root, "tail-marker")); len(got) != 0 {
		t.Fatalf("text past the limit was indexed: %v", got)
	}
}

// A long query is more tokens than the MATCH expression carries. Dropping the
// surplus can only widen the candidate set, never lose a document, because the
// verification pass decides either way.
func TestAQueryLongerThanTheTokenBudgetStillMatches(t *testing.T) {
	long := "北京大學研究所計畫書的第一章緒論與背景說明"
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "序言 " + long + " 附錄"},
		fixtureNode{id: "beta", title: "Beta", body: "北京大學"},
	)
	manager := newManager(t)
	if len(queryTokens(long)) <= maxQueryTokens {
		t.Fatalf("the fixture query has only %d tokens; it no longer exceeds the budget", len(queryTokens(long)))
	}
	if got := ids(search(t, manager, root, long)); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("long query = %v, want [alpha]", got)
	}
}

func TestFreshnessStampReadsAsATime(t *testing.T) {
	stamp := freshness(fileStamp{Exists: true, Size: 12, ModTime: time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC).UnixNano()}, nil)
	if !strings.HasPrefix(stamp, "2026-08-04T10:00:00Z") {
		t.Fatalf("modified_at does not start with the time: %q", stamp)
	}
	if freshness(fileStamp{}, nil) != "" {
		t.Fatal("a node with no files must have an empty stamp")
	}
}
