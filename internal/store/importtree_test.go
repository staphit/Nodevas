package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

func newImportGraph() *engine.Graph {
	return &engine.Graph{Version: 1, Type: "workflow"}
}

func TestImportDocumentsWritesNodesAndGraph(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	count, err := s.ImportDocuments(newImportGraph(), []ImportDocument{
		{Title: "第一章", Body: "# 第一章\n\n開場\n"},
		{Title: "第二章", Body: "# 第二章\n"},
	})
	if err != nil {
		t.Fatalf("ImportDocuments: %v", err)
	}
	if count != 2 {
		t.Fatalf("imported %d documents, want 2", count)
	}
	g, _, err := s.LoadGraph()
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("graph has %d nodes, want 2", len(g.Nodes))
	}
	if g.Nodes[0].ID != "node-0001" || g.Nodes[0].Title != "第一章" {
		t.Fatalf("first node = %+v", g.Nodes[0])
	}
	if g.Nodes[1].ID != "node-0002" {
		t.Fatalf("second node id = %q", g.Nodes[1].ID)
	}
	content, _, err := s.LoadNodeContent("node-0001")
	if err != nil {
		t.Fatalf("LoadNodeContent: %v", err)
	}
	// The store owns frontmatter, so an imported document carries the same
	// header as one created through CreateNode.
	if !strings.Contains(content, "id: node-0001") || !strings.Contains(content, "開場") {
		t.Fatalf("imported node file = %q", content)
	}
}

func TestImportDocumentsLeavesUsableEmptyProject(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := s.ImportDocuments(newImportGraph(), nil); err != nil {
		t.Fatalf("ImportDocuments: %v", err)
	}
	if _, err := os.Stat(s.GraphPath()); err != nil {
		t.Fatalf("graph.yaml: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "nodes"))
	if err != nil {
		t.Fatalf("nodes directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty import left %d node files", len(entries))
	}
}

func TestImportDocumentsRefusesExistingProject(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := s.ImportDocuments(newImportGraph(), nil); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := s.ImportDocuments(newImportGraph(), []ImportDocument{
		{Title: "覆蓋", Body: "x"},
	}); err == nil {
		t.Fatal("import over an existing project succeeded, want refusal")
	}
	if _, err := os.Stat(s.NodePath("node-0001")); !os.IsNotExist(err) {
		t.Fatalf("refused import still wrote a node file: %v", err)
	}
}
