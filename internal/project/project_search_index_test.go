package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/searchindex"
	"nodevas/internal/testpg"
)

// newTestSearchIndex is the project's index over a fresh database, as serve
// wires it with UseDatabase.
func newTestSearchIndex(t testing.TB) *searchIndex {
	t.Helper()
	index := &searchIndex{manager: searchindex.NewManagerWithDB(testpg.New(t))}
	// Background passes read the fixture's files; they end before it goes.
	t.Cleanup(func() { _ = index.manager.Close() })
	return index
}

// searchEventually polls a search until it returns exactly the nodes in want
// (in order) or within has passed.
func searchEventually(t *testing.T, index *searchIndex, project ProjectInfo, query string, want []string, within time.Duration) {
	t.Helper()
	began := time.Now()
	for {
		results, err := index.search(project, query)
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		got := make([]string, 0, len(results))
		for _, result := range results {
			got = append(got, result.NodeID)
		}
		if fmt.Sprint(got) == fmt.Sprint(want) {
			return
		}
		if time.Since(began) > within {
			t.Fatalf("search %q = %v after %v, want %v", query, got, within, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeSearchProject(t testing.TB, nodes ...*engine.Node) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graphData, err := engine.MarshalGraph(&engine.Graph{Version: 1, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graphData, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeNodeBody(t testing.TB, root, id, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSearchIndexRefreshesNodeMarkdown(t *testing.T) {
	root := writeSearchProject(t, &engine.Node{ID: "alpha", Title: "Alpha"})
	writeNodeBody(t, root, "alpha", "before body")

	project := ProjectInfo{Name: "main", Path: root}
	index := newTestSearchIndex(t)
	results, err := index.search(project, "before")
	if err != nil {
		t.Fatalf("initial search: %v", err)
	}
	if len(results) != 1 || results[0].NodeID != "alpha" {
		t.Fatalf("initial results = %+v", results)
	}

	writeNodeBody(t, root, "alpha", "after body with more text")
	// The watcher hook is the primary freshness path for outside edits; it
	// refreshes the node in the background, within a second.
	// internal/searchindex covers the stat-walk net that catches edits it
	// never hears about.
	index.invalidate(root, "alpha")

	searchEventually(t, index, project, "after", []string{"alpha"}, time.Second)
	searchEventually(t, index, project, "before", []string{}, time.Second)
}

// The server's own writes never reach the watcher. The store reports them
// instead, and a save is searchable within a second of returning.
func TestAStoreWriteIsSearchableWithinASecond(t *testing.T) {
	pm, info, st := searchLoadProject(t, 20)
	// A search no longer waits out the first build (coldSearchWait), so the
	// build is awaited here: the second this test measures starts at a save
	// into a built index, not at a cold build on a loaded machine.
	if !pm.AwaitSearchIndex(info.Path, 30*time.Second) {
		t.Fatal("the project's search index was not built")
	}
	_, rev, err := st.LoadNodeContent("n0003")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveNodeContent(identity.Actor{ID: "u", Name: "u"}, "n0003", "a freshly saved lighthouse\n", rev); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	for {
		results, err := pm.SearchProjectNodes(info, "lighthouse")
		if err != nil {
			t.Fatal(err)
		}
		if len(results) == 1 && results[0].NodeID == "n0003" {
			t.Logf("searchable after %v", time.Since(began))
			return
		}
		if time.Since(began) > time.Second {
			t.Fatalf("a saved node was not searchable within a second: %+v", results)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The index used to truncate node text, so anything past the first couple of
// kilobytes stopped being findable even though a plain scan would have found
// it.
func TestSearchIndexFindsTextDeepInALongNode(t *testing.T) {
	root := writeSearchProject(t, &engine.Node{ID: "alpha", Title: "Alpha"})
	body := strings.Repeat("padding line\n", 4000) + "buried needle\n"
	if len(body) < 16<<10 {
		t.Fatalf("the fixture is only %d bytes; it has to be well past any small cap", len(body))
	}
	writeNodeBody(t, root, "alpha", body)

	project := ProjectInfo{Name: "main", Path: root}
	results, err := newTestSearchIndex(t).search(project, "buried needle")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the node whose body holds the phrase", results)
	}
	// The direct scan is the fallback, so the two must agree.
	direct := SearchProjectNodesDirect(project, "buried needle")
	if len(direct) != len(results) {
		t.Errorf("index found %d, direct scan found %d", len(results), len(direct))
	}
}

// The index and its fallback have to return the same rows with the same
// snippets, or a project that trips the fallback would look different.
func TestSearchIndexAgreesWithTheDirectScan(t *testing.T) {
	root := writeSearchProject(t,
		&engine.Node{ID: "alpha", Title: "Alpha", Kind: "task", Tags: []string{"设计", "spec"}},
		&engine.Node{ID: "beta", Title: "Beta", Priority: "high"},
	)
	writeNodeBody(t, root, "alpha", "关于接口设计的说明，plus some English prose to slice a snippet from.")
	writeNodeBody(t, root, "beta", "unrelated body")

	project := ProjectInfo{Name: "main", Path: root}
	index := newTestSearchIndex(t)
	for _, query := range []string{"设计", "接口设计", "english prose", "alpha", "high"} {
		indexed, err := index.search(project, query)
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		direct := SearchProjectNodesDirect(project, query)
		if fmt.Sprint(indexed) != fmt.Sprint(direct) {
			t.Errorf("query %q:\n index = %+v\ndirect = %+v", query, indexed, direct)
		}
	}
}

func BenchmarkSearchWarmIndex(b *testing.B) {
	root, project := benchmarkProject(b)
	index := newTestSearchIndex(b)
	if _, err := index.search(project, "needle"); err != nil {
		b.Fatal(err)
	}
	_ = root
	b.ResetTimer()
	for b.Loop() {
		if _, err := index.search(project, "needle"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchDirectScan(b *testing.B) {
	_, project := benchmarkProject(b)
	b.ResetTimer()
	for b.Loop() {
		SearchProjectNodesDirect(project, "needle")
	}
}

// benchmarkProject builds a project big enough that the difference between
// "walk the postings" and "re-read and lowercase the whole workspace" is the
// thing being measured.
func benchmarkProject(b *testing.B) (string, ProjectInfo) {
	b.Helper()
	nodes := make([]*engine.Node, 0, 200)
	for i := range 200 {
		nodes = append(nodes, &engine.Node{ID: fmt.Sprintf("n%03d", i), Title: fmt.Sprintf("Node %d", i)})
	}
	root := writeSearchProject(b, nodes...)
	body := strings.Repeat("这是一段中文说明文字，用来填充节点内容。 filler prose line.\n", 200)
	for i, node := range nodes {
		text := body
		if i == 137 {
			text += "\nburied needle here\n"
		}
		writeNodeBody(b, root, node.ID, text)
	}
	return root, ProjectInfo{Name: "bench", Path: root}
}
