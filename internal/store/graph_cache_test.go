package store

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// The parsed graph is cached per store and shared by every reader, so these
// tests pin the three things that make sharing safe: it is fresh (an edit from
// outside the server is seen), it is read-only (no store write path mutates
// it), and readers do not queue behind each other to get it.

func TestLoadGraphReusesParsedGraphWhileFileUnchanged(t *testing.T) {
	st := benchGraphStore(t, 20)
	first, firstRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	second, secondRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("an unchanged graph.yaml was parsed again instead of served from the cache")
	}
	if firstRev != secondRev || firstRev == "" {
		t.Fatalf("revisions %q and %q, want equal and non-empty", firstRev, secondRev)
	}
}

func TestLoadGraphPicksUpExternalEditWithSameSizeAndMtime(t *testing.T) {
	st := benchGraphStore(t, 20)
	before, beforeRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if before.NodeByID("n00003").Title != "Node n00003" {
		t.Fatalf("unexpected fixture title %q", before.NodeByID("n00003").Title)
	}
	info, err := os.Stat(st.GraphPath())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.GraphPath())
	if err != nil {
		t.Fatal(err)
	}
	// A git checkout or an agent rewriting the file: same length, and the
	// timestamp put back, so only the bytes tell.
	edited := strings.Replace(string(data), "title: Node n00003", "title: Edit n00003", 1)
	if len(edited) != len(data) || edited == string(data) {
		t.Fatal("fixture edit should change content but keep size")
	}
	if err := os.WriteFile(st.GraphPath(), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(st.GraphPath(), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, afterRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if got := after.NodeByID("n00003").Title; got != "Edit n00003" {
		t.Fatalf("title after external edit = %q, want %q", got, "Edit n00003")
	}
	if afterRev == beforeRev {
		t.Fatal("revision did not change after an external edit")
	}
	if before.NodeByID("n00003").Title != "Node n00003" {
		t.Fatal("the graph handed out before the edit changed under its reader")
	}
}

func TestLoadGraphSeesDeletedAndInvalidGraphFile(t *testing.T) {
	st := benchGraphStore(t, 5)
	if _, _, err := st.LoadGraph(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.GraphPath(), []byte("version: [not a graph\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.LoadGraph(); err == nil {
		t.Fatal("a broken graph.yaml must fail to load, not be served from the cache")
	}
	if err := os.Remove(st.GraphPath()); err != nil {
		t.Fatal(err)
	}
	g, rev, err := st.LoadGraph()
	if err != nil || rev != "" || len(g.Nodes) != 0 {
		t.Fatalf("missing graph.yaml: nodes=%d rev=%q err=%v, want empty graph", len(g.Nodes), rev, err)
	}
}

func TestInvalidateGraphCacheDropsCachedGraph(t *testing.T) {
	st := benchGraphStore(t, 5)
	first, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	st.InvalidateGraphCache()
	second, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("InvalidateGraphCache kept the cached graph")
	}
}

func TestSaveGraphInvalidatesCachedGraph(t *testing.T) {
	st := benchGraphStore(t, 5)
	loaded, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	edit := loaded.Clone()
	edit.NodeByID("n00001").Title = "Saved title"
	newRev, err := st.SaveGraph(identity.Local, edit, rev)
	if err != nil {
		t.Fatal(err)
	}
	after, afterRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if afterRev != newRev {
		t.Fatalf("rev after save = %q, want %q", afterRev, newRev)
	}
	if after == loaded || after.NodeByID("n00001").Title != "Saved title" {
		t.Fatal("LoadGraph after SaveGraph served the stale cached graph")
	}
	if loaded.NodeByID("n00001").Title != "Node n00001" {
		t.Fatal("the cached graph was mutated by the save")
	}
}

func TestLoadGraphFollowsSharedStatusChanges(t *testing.T) {
	st := benchGraphStore(t, 5)
	var mu sync.Mutex
	shared := []engine.StatusDefinition{{ID: "custom-status-a", Label: "A"}}
	st.SetSharedStatuses(func() []engine.StatusDefinition {
		mu.Lock()
		defer mu.Unlock()
		return append([]engine.StatusDefinition(nil), shared...)
	})
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if g.UI == nil || len(g.UI.CustomStatuses) != 1 {
		t.Fatalf("want the one shared status merged, got %+v", g.UI)
	}
	mu.Lock()
	shared = append(shared, engine.StatusDefinition{ID: "custom-status-b", Label: "B"})
	mu.Unlock()
	g, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(g.UI.CustomStatuses) != 2 {
		t.Fatalf("shared vocabulary change not reflected: %+v", g.UI.CustomStatuses)
	}
}

func TestClonedGraphMutationDoesNotLeakIntoOtherReaders(t *testing.T) {
	st := benchGraphStore(t, 5)
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	mine := g.Clone()
	mine.NodeByID("n00002").Title = "mine"
	mine.UI.Positions["n00002"] = engine.Position{X: -99}
	mine.Nodes = mine.Nodes[:1]
	other, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Nodes) != 5 || other.NodeByID("n00002").Title != "Node n00002" ||
		other.UI.Positions["n00002"].X == -99 {
		t.Fatal("a caller's edit of its clone reached another reader's graph")
	}
}

// TestLoadGraphDoesNotWaitForOtherReaders holds a read lock the way an
// in-flight reader does; a second reader must still get through.
func TestLoadGraphDoesNotWaitForOtherReaders(t *testing.T) {
	st := benchGraphStore(t, 5)
	st.mu.RLock()
	defer st.mu.RUnlock()
	done := make(chan error, 1)
	go func() {
		_, _, err := st.LoadGraph()
		if err == nil {
			_, err = st.ListNodePages("n00001")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadGraph/ListNodePages blocked behind another reader")
	}
}

// TestStoreWritesNeverMutateTheSharedGraph runs readers that walk the graph
// they were handed while writers go through the store's write paths. Under
// -race any write path that edits the cached graph in place is a data race;
// without -race the snapshot comparison catches it.
func TestStoreWritesNeverMutateTheSharedGraph(t *testing.T) {
	st := benchGraphStore(t, 30)
	shared, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := shared.Clone()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				g, _, err := st.LoadGraph()
				if err != nil {
					t.Error(err)
					return
				}
				walkGraph(g)
				walkGraph(shared)
			}
		}()
	}

	for i := range 15 {
		x, y := float64(i*7), float64(i*3)
		title := fmt.Sprintf("Renamed %d", i)
		if _, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{
			{Kind: "move", NodeID: "n00001", X: &x, Y: &y},
			{Kind: "node-metadata", NodeID: "n00002", Title: &title},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateNode(identity.Local, &engine.Node{ID: fmt.Sprintf("extra%02d", i), Title: "extra"}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetStatus("n00000", engine.StatusInProgress, "t", ""); err != nil && i == 0 {
			t.Fatal(err)
		}
		g, rev, err := st.LoadGraph()
		if err != nil {
			t.Fatal(err)
		}
		edit := g.Clone()
		edit.NodeByID("n00003").Title = title
		if _, err := st.SaveGraph(identity.Local, edit, rev); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()

	if !reflect.DeepEqual(snapshot, shared) {
		t.Fatal("a store write path mutated a graph already handed to a reader")
	}
	final, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if final.NodeByID("n00002").Title != "Renamed 14" || final.NodeByID("extra14") == nil {
		t.Fatal("writes did not land")
	}
}

// walkGraph reads every field a handler would serialize.
func walkGraph(g *engine.Graph) int {
	n := 0
	for _, node := range g.Nodes {
		n += len(node.ID) + len(node.Title) + len(node.Tags)
	}
	for _, edge := range g.Edges {
		n += len(edge.From)
	}
	if g.UI != nil {
		for id, p := range g.UI.Positions {
			n += len(id) + int(p.X)
		}
	}
	return n
}
