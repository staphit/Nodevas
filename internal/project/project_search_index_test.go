package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

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
	index := newSearchIndex()
	results, err := index.search(project, "before")
	if err != nil {
		t.Fatalf("initial search: %v", err)
	}
	if len(results) != 1 || results[0].NodeID != "alpha" {
		t.Fatalf("initial results = %+v", results)
	}

	writeNodeBody(t, root, "alpha", "after body with more text")
	// The watcher hook is the primary freshness path; internal/searchindex
	// covers the stat-walk net that catches edits it never hears about.
	index.invalidate(root, "alpha")

	results, err = index.search(project, "after")
	if err != nil {
		t.Fatalf("search after edit: %v", err)
	}
	if len(results) != 1 || results[0].NodeID != "alpha" {
		t.Fatalf("results after edit = %+v", results)
	}
	results, err = index.search(project, "before")
	if err != nil {
		t.Fatalf("search old term: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("stale results after edit = %+v", results)
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
	results, err := newSearchIndex().search(project, "buried needle")
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
	index := newSearchIndex()
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
	index := newSearchIndex()
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
