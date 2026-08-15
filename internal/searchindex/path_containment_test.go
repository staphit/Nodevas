package searchindex

import (
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
)

func TestSearchDoesNotIndexSymlinkedNode(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("unique-index-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	graphData, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha", Title: "Alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graphData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nodes", "alpha.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	matches, err := manager.Search(root, "unique-index-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("search index exposed symlink target: %+v", matches)
	}
}
