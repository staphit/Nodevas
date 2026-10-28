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
