package store

import (
	"path/filepath"
	"sync"
	"testing"

	"nodevas/internal/identity"
)

// The search index keeps itself warm off what the store writes: every write
// the watcher deliberately stays quiet about has to reach it some other way.
func TestOnChangeHearsTheStoresOwnWrites(t *testing.T) {
	st := benchPagesStore(t, 2, 1)
	var mu sync.Mutex
	heard := map[string]bool{}
	st.SetOnChange(func(path string) {
		mu.Lock()
		heard[filepath.Clean(path)] = true
		mu.Unlock()
	})
	actor := identity.Actor{ID: "u", Name: "u"}

	_, rev, err := st.LoadNodeContent("n00000")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveNodeContent(actor, "n00000", "new body\n", rev); err != nil {
		t.Fatal(err)
	}
	_, _, pageRev, err := st.LoadNodePage("n00001", "page-0001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveNodePage(actor, "n00001", "page-0001", "# new page\n", pageRev); err != nil {
		t.Fatal(err)
	}
	x, y := 1.0, 2.0
	if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: "n00000", X: &x, Y: &y}}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{
		st.NodePath("n00000"),
		st.NodePagePath("n00001", "page-0001", PageFormatMarkdown),
		st.GraphPath(),
	} {
		if !heard[filepath.Clean(want)] {
			t.Errorf("no change reported for %s; heard %v", want, heard)
		}
	}
}
