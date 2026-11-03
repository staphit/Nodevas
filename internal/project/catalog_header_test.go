package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/realtime"
)

func writeGraphFile(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "graph.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func listedByName(t *testing.T, pm *ProjectManager) map[string]ProjectInfo {
	t.Helper()
	infos, err := pm.List()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ProjectInfo{}
	for _, info := range infos {
		out[info.Name] = info
	}
	return out
}

// The catalog carries each project's layout and office style, read from the
// graph.yaml header it already caches by stat.
func TestCatalogCarriesLayoutAndOfficeStyle(t *testing.T) {
	workspace := lockTestWorkspace(t)
	novel := writeGraphFile(t, filepath.Join(workspace, "novel"), "version: 1\ntype: story\nnodes: []\n")
	writeGraphFile(t, filepath.Join(workspace, "app"), "version: 1\ntype: workflow\noffice_style: cafe\nnodes:\n  - id: a\n")
	writeGraphFile(t, filepath.Join(workspace, "odd"), "version: 1\noffice_style: Space\nnodes: []\n")
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer pm.Close()

	got := listedByName(t, pm)
	if n := got["novel"]; n.Layout != "story" || n.OfficeStyle != "study" || n.OfficeStyleSet {
		t.Fatalf("novel = %+v", n)
	}
	if a := got["app"]; a.Layout != "workflow" || a.OfficeStyle != "cafe" || !a.OfficeStyleSet || a.Nodes != 1 {
		t.Fatalf("app = %+v", a)
	}
	if o := got["odd"]; o.OfficeStyle != "engineering" || o.OfficeStyleSet {
		t.Fatalf("an unknown stored style should resolve to the default: %+v", o)
	}

	// Same size and modification time: the cached header is served, the file
	// is not parsed again.
	info, err := os.Stat(novel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(novel, []byte("version: 1\ntype: xyzab\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(novel, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if n := listedByName(t, pm)["novel"]; n.Layout != "story" {
		t.Fatalf("the unchanged stat re-parsed the file: %+v", n)
	}
	later := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(novel, later, later); err != nil {
		t.Fatal(err)
	}
	if n := listedByName(t, pm)["novel"]; n.Layout != "xyzab" || n.OfficeStyle != "engineering" {
		t.Fatalf("a new modification time did not re-parse: %+v", n)
	}
}
