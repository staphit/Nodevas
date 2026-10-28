package store

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// A write holds the store's lock, so whatever it does there every other write
// waits for. These tests pin what a page write may skip (the files of pages it
// does not touch) and what concurrent writers may never lose.

// breakPage turns a page's file into a directory, the kind of damage the
// per-page check exists to catch.
func breakPage(t *testing.T, st *Store, nodeID, pageID string) {
	t.Helper()
	path := st.NodePagePath(nodeID, pageID, PageFormatMarkdown)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPageWritesDoNotCheckEveryOtherPage(t *testing.T) {
	st := benchPagesStore(t, 2, 3)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	breakPage(t, st, "n00001", "page-0003")

	// The listing still reports the damage...
	if _, err := st.ListNodePages("n00001"); err == nil || !strings.Contains(err.Error(), "page-0003") {
		t.Fatalf("ListNodePages with a broken page = %v, want an error naming it", err)
	}
	// ...but the pages next to it stay readable and writable: a write checks
	// the file it touches, not the whole node, while every other write waits.
	_, _, rev, err := st.LoadNodePage("n00001", "page-0001")
	if err != nil {
		t.Fatalf("LoadNodePage of a healthy page: %v", err)
	}
	if _, err := st.SaveNodePage(actor, "n00001", "page-0001", "# saved\n", rev); err != nil {
		t.Fatalf("SaveNodePage of a healthy page next to a broken one: %v", err)
	}
	if _, err := st.UpdateNodePage(actor, "n00001", "page-0002", "Renamed", nil); err != nil {
		t.Fatalf("UpdateNodePage next to a broken page: %v", err)
	}
	if _, _, _, err := st.CreateNodePage(actor, "n00001", "Another", PageFormatMarkdown); err != nil {
		t.Fatalf("CreateNodePage next to a broken page: %v", err)
	}
	// The broken page itself is still refused.
	if _, _, _, err := st.LoadNodePage("n00001", "page-0003"); err == nil {
		t.Fatal("LoadNodePage read a page whose file is a directory")
	}
	if _, err := st.SaveNodePage(actor, "n00001", "page-0003", "# over\n", ""); err == nil {
		t.Fatal("SaveNodePage wrote over a page whose file is a directory")
	}
}

func TestConcurrentWritersLoseNothing(t *testing.T) {
	const nodes, rounds = 6, 15
	st := benchPagesStore(t, nodes, 1)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	var wg sync.WaitGroup
	errs := make(chan error, nodes*3)
	for i := range nodes {
		nodeID := fmt.Sprintf("n%05d", i)
		// Moves of this node, one writer each.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 1; r <= rounds; r++ {
				x, y := float64(r), float64(i)
				if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: nodeID, X: &x, Y: &y}}); err != nil {
					errs <- err
					return
				}
			}
		}()
		// Title edits of the same node: a different field of the same file.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 1; r <= rounds; r++ {
				title := fmt.Sprintf("%s v%d", nodeID, r)
				if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "node-metadata", NodeID: nodeID, Title: &title}}); err != nil {
					errs <- err
					return
				}
			}
		}()
		// Saves of the node's page, chained on the revision each returns.
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, rev, err := st.LoadNodePage(nodeID, "page-0001")
			if err != nil {
				errs <- err
				return
			}
			for r := 1; r <= rounds; r++ {
				rev, err = st.SaveNodePage(actor, nodeID, "page-0001", fmt.Sprintf("# %s save %d\n", nodeID, r), rev)
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for i := range nodes {
		nodeID := fmt.Sprintf("n%05d", i)
		if got, want := g.UI.Positions[nodeID].X, float64(rounds); got != want {
			t.Errorf("%s x = %v, want the last move's %v", nodeID, got, want)
		}
		if got, want := g.NodeByID(nodeID).Title, fmt.Sprintf("%s v%d", nodeID, rounds); got != want {
			t.Errorf("%s title = %q, want the last edit's %q", nodeID, got, want)
		}
		_, content, _, err := st.LoadNodePage(nodeID, "page-0001")
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("# %s save %d\n", nodeID, rounds); content != want {
			t.Errorf("%s page = %q, want the last save's %q", nodeID, content, want)
		}
		doc, _, err := st.LoadNodeContent(nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("title: %s v%d", nodeID, rounds); !strings.Contains(doc, want) {
			t.Errorf("%s document frontmatter lost the last title edit:\n%s", nodeID, doc)
		}
	}
}

// queueMovesBehindLock starts one move per node while the test holds the
// store's write lock, so every one of them is waiting when it is released.
func queueMovesBehindLock(t *testing.T, st *Store, calls []func() (string, error)) []string {
	t.Helper()
	st.mu.Lock()
	revs := make([]string, len(calls))
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			revs[i], errs[i] = call()
		}()
	}
	// Long enough for every caller to reach the lock; the assertions below
	// hold however they happen to be ordered.
	time.Sleep(300 * time.Millisecond)
	st.mu.Unlock()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			revs[i] = "error: " + err.Error()
		}
	}
	return revs
}

func TestQueuedGraphOpsShareOneCommit(t *testing.T) {
	const nodes = 8
	st := benchPagesStore(t, nodes, 0)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	calls := make([]func() (string, error), nodes)
	for i := range calls {
		calls[i] = func() (string, error) {
			x, y := float64(1000+i), float64(i)
			_, rev, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: fmt.Sprintf("n%05d", i), X: &x, Y: &y}})
			return rev, err
		}
	}
	revs := queueMovesBehindLock(t, st, calls)

	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for i := range nodes {
		if got := g.UI.Positions[fmt.Sprintf("n%05d", i)].X; got != float64(1000+i) {
			t.Errorf("n%05d x = %v, want %v", i, got, 1000+i)
		}
	}
	// Moves that queued behind one lock hold are written together: one
	// graph.yaml write, so one revision, and it is the one on disk.
	distinct := map[string]bool{}
	for _, r := range revs {
		distinct[r] = true
	}
	if len(distinct) != 1 || !distinct[rev] {
		t.Fatalf("queued moves returned %d revisions %v, want the one on disk (%s) for all", len(distinct), revs, rev)
	}
}

func TestQueuedGraphOpsFailAlone(t *testing.T) {
	st := benchPagesStore(t, 3, 0)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	moveTo := func(nodeID string, x float64) GraphOp {
		y := 0.0
		return GraphOp{Kind: "move", NodeID: nodeID, X: &x, Y: &y}
	}
	calls := []func() (string, error){
		func() (string, error) {
			_, rev, err := st.ApplyGraphOps(actor, []GraphOp{moveTo("n00000", 11)})
			return rev, err
		},
		// A valid move and then an op that fails: neither may land.
		func() (string, error) {
			_, rev, err := st.ApplyGraphOps(actor, []GraphOp{moveTo("n00001", 22), moveTo("missing", 1)})
			return rev, err
		},
		func() (string, error) {
			_, rev, err := st.ApplyGraphOps(actor, []GraphOp{moveTo("n00002", 33)})
			return rev, err
		},
	}
	revs := queueMovesBehindLock(t, st, calls)
	if !strings.HasPrefix(revs[1], "error: ") || !strings.Contains(revs[1], "missing") {
		t.Fatalf("the call with a bad op returned %q, want its own error", revs[1])
	}
	for _, i := range []int{0, 2} {
		if strings.HasPrefix(revs[i], "error: ") {
			t.Fatalf("call %d failed with its neighbour: %s", i, revs[i])
		}
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.UI.Positions["n00000"].X; got != 11 {
		t.Errorf("n00000 x = %v, want 11", got)
	}
	if got := g.UI.Positions["n00002"].X; got != 33 {
		t.Errorf("n00002 x = %v, want 33", got)
	}
	if got := g.UI.Positions["n00001"].X; got == 22 {
		t.Error("the valid half of a failed call was written")
	}
}
