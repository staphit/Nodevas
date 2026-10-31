package realtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/store"
)

// An external write to graph.yaml drops the store's parsed graph even when the
// bytes are the same, so the cache never outlives a change the watcher saw.
func TestWatcherInvalidatesGraphCacheOnExternalWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("version: 1\nnodes:\n  - id: a\n    title: A\n")
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(root)
	stop, err := Watch(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	first, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if again, _, _ := st.LoadGraph(); again != first {
		t.Fatal("precondition: an unchanged graph should come from the cache")
	}
	if err := os.WriteFile(st.GraphPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _, err := st.LoadGraph()
		if err != nil {
			t.Fatal(err)
		}
		if current != first {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the watcher saw graph.yaml change but the cached graph was kept")
}

// A page file replaced from outside (here by a directory) is seen by the next
// page listing as soon as the watcher reports it, not after the store's
// recheck interval.
func TestWatcherInvalidatesPageListingsOnExternalChange(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "nodes", "a.pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), []byte("version: 1\nnodes:\n  - id: a\n    title: A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nodes", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"pages":[{"id":"page-0001","title":"One","format":"md"}]}`
	if err := os.WriteFile(filepath.Join(pagesDir, "pages.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(pagesDir, "page-0001.md")
	if err := os.WriteFile(page, []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(root)
	stop, err := Watch(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if _, err := st.ListNodePages("a"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(page); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(page, 0o755); err != nil {
		t.Fatal(err)
	}
	// Well inside the store's own recheck interval (5 s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := st.ListNodePages("a"); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the watcher saw a page file replaced but the listing kept its old verdict")
}
