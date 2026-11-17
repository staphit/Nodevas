package store

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// edgeSet is graph.yaml's edges as "from->to relation" lines, sorted.
func edgeSet(t *testing.T, st *Store) []string {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range g.Edges {
		if e != nil {
			out = append(out, e.From+"->"+e.To+" "+e.Relation)
		}
	}
	slices.Sort(out)
	return out
}

func requiresOf(t *testing.T, st *Store, id string) string {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	n := g.NodeByID(id)
	if n == nil {
		t.Fatalf("%s is not on the board", id)
	}
	return n.Requires
}

// A kept batch node that waited for a trashed one waits for it again after
// the redo, and so does an existing node a person wired to the batch after
// the commit (finding 10).
func TestRedoPutsBackDependenciesOfNodesTheUndoKept(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "A"},
			{Key: "b", Title: "B", DependsOn: []string{"a"}},
			{Key: "c", Title: "C"},
		},
		Batch: &BatchSpec{Via: "copilot-draft:rewire"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := result.IDs["a"], result.IDs["b"], result.IDs["c"]
	title := "B, by hand"
	if _, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{
		{Kind: "node-metadata", NodeID: b, Title: &title},
		// After the commit, intro is made to wait for c.
		{Kind: "add-edge", From: c, To: "intro"},
	}); err != nil {
		t.Fatal(err)
	}

	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(undone.Trashed, []string{a, c}) || len(undone.Refused) != 1 {
		t.Fatalf("undo = %+v", undone)
	}
	if got := requiresOf(t, st, b); got != "" {
		t.Fatalf("after undo b.requires = %q", got)
	}

	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(redone.Restored, []string{a, c}) || len(redone.Skipped) != 0 || redone.Links != 2 {
		t.Fatalf("redo = %+v", redone)
	}
	if got := requiresOf(t, st, b); got != a {
		t.Fatalf("after redo b.requires = %q, want %q", got, a)
	}
	if got := requiresOf(t, st, "intro"); got != c {
		t.Fatalf("after redo intro.requires = %q, want %q", got, c)
	}
	edges := edgeSet(t, st)
	for _, want := range []string{a + "->" + b + " ", c + "->intro "} {
		if !slices.Contains(edges, want) {
			t.Fatalf("edge %q missing from %v", want, edges)
		}
	}
}

// A requires nobody changed since the undo gets its whole expression back:
// an "or" stays an "or", not an "and" of what is left plus the operand.
func TestRedoKeepsTheShapeOfAnUntouchedRequires(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:or"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := result.IDs["a"]
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.NodeByID("intro").Requires = a + " or note-01"
	if _, err := st.SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	want := requiresOf(t, st, "intro")
	if undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{}); err != nil || len(undone.Trashed) != 1 {
		t.Fatalf("undo = %+v %v", undone, err)
	}
	if got := requiresOf(t, st, "intro"); got != "note-01" {
		t.Fatalf("after undo intro.requires = %q", got)
	}
	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil || redone.Links != 1 {
		t.Fatalf("redo = %+v %v", redone, err)
	}
	if got := requiresOf(t, st, "intro"); got != want {
		t.Fatalf("after redo intro.requires = %q, want %q", got, want)
	}
}

// Undo and redo leave graph.yaml's edges as the commit made them, not only
// the nodes' requires (finding 13).
func TestRedoPutsBackTheBatchsGraphEdges(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)
	before := edgeSet(t, st)
	if len(before) != 3 {
		t.Fatalf("edges after commit = %v", before)
	}
	if _, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{}); err != nil {
		t.Fatal(err)
	}
	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if redone.Links != 1 || len(redone.Skipped) != 0 {
		t.Fatalf("redo = %+v", redone)
	}
	if after := edgeSet(t, st); !reflect.DeepEqual(after, before) {
		t.Fatalf("edges after redo = %v, want %v", after, before)
	}
	g, _, _ := st.LoadGraph()
	if issues := engine.Validate(g); hasErrorIssue(issues) {
		t.Fatalf("redo left errors: %+v", issues)
	}
}

// A redo that finds every node already back (restored from the Trash panel)
// leaves the batch undoable again instead of stuck (finding 11).
func TestARedoWithNothingToRestoreLeavesTheBatchUndoable(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:stuck"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := result.IDs["a"]
	if _, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	file := ""
	for _, item := range items {
		if item.NodeID == a {
			file = item.File
		}
	}
	if _, err := st.RestoreTrash(batchBea, file); err != nil {
		t.Fatal(err)
	}
	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil || len(redone.Restored) != 0 || refusalCodes(redone.Skipped)[a] != BatchSkippedExists {
		t.Fatalf("redo = %+v %v", redone, err)
	}
	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(undone.Trashed)+len(undone.Refused) != 1 {
		t.Fatalf("the undo after the redo was a silent no-op: %+v", undone)
	}
}

// A batch may only wire existing nodes to its own new nodes: an edge between
// two existing nodes is something its undo could not take back (finding 12).
func TestABatchRefusesAnEdgeBetweenTwoExistingNodes(t *testing.T) {
	st := subgraphStore(t)
	spec := SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Edges: []SubgraphEdge{{From: "note-01", To: "intro"}},
		Batch: &BatchSpec{Via: "copilot-draft:edges"},
	}
	before := snapshot(t, st)
	for name, run := range map[string]func(SubgraphSpec) (SubgraphResult, error){
		"check":  func(s SubgraphSpec) (SubgraphResult, error) { return st.CheckSubgraph(batchBea, s) },
		"create": func(s SubgraphSpec) (SubgraphResult, error) { return st.CreateSubgraph(batchBea, s) },
	} {
		_, err := run(spec)
		var invalid *ErrInvalidSubgraph
		if !errors.As(err, &invalid) || invalid.Field != "edges[0].from" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if after := snapshot(t, st); !reflect.DeepEqual(after, before) {
		t.Fatal("a refused batch wrote something")
	}
	// Without a batch there is no undo to keep honest.
	spec.Batch = nil
	if _, err := st.CreateSubgraph(batchBea, spec); err != nil {
		t.Fatalf("plain call: %v", err)
	}
}

// One live commit per draft (contract C3): a second commit with the via of
// a batch that is recorded and not undone is refused, naming that batch, and
// writes nothing; undoing the batch frees its via.
func TestALiveBatchsViaIsTaken(t *testing.T) {
	st := subgraphStore(t)
	spec := SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:once"},
	}
	first, err := st.CreateSubgraph(batchBea, spec)
	if err != nil || !first.Undoable {
		t.Fatalf("first = %+v %v", first, err)
	}
	before := snapshot(t, st)
	_, err = st.CreateSubgraph(batchBea, spec)
	var taken *ErrBatchViaTaken
	if !errors.As(err, &taken) || taken.BatchID != first.BatchID {
		t.Fatalf("second commit: %v", err)
	}
	if after := snapshot(t, st); !reflect.DeepEqual(after, before) {
		t.Fatal("the refused commit wrote something")
	}
	// Another member's commit with the same via is refused the same way.
	if _, err := st.CreateSubgraph(batchDan, spec); !errors.As(err, &taken) {
		t.Fatalf("dan's commit: %v", err)
	}
	// Another via is free.
	if _, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: spec.Nodes, Batch: &BatchSpec{Via: "copilot-draft:other"},
	}); err != nil {
		t.Fatalf("other via: %v", err)
	}
	if _, err := st.UndoBatch(batchBea, first.BatchID, BatchUndoOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := st.CreateSubgraph(batchBea, spec)
	if err != nil || !again.Undoable || again.BatchID == first.BatchID {
		t.Fatalf("after undo = %+v %v", again, err)
	}
}

// The batch's label (contract C4) is cleaned, bounded, recorded and named in
// the history.
func TestABatchLabelIsRecordedAndNamedInTheHistory(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:l1", Label: "  ollama\n · qwen3:8b\x00 "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loadBatchForTest(t, st, result.BatchID).Label; got != "ollama · qwen3:8b" {
		t.Fatalf("label = %q", got)
	}
	long := strings.Repeat("模", 40) // 120 bytes
	result, err = st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:l2", Label: long},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loadBatchForTest(t, st, result.BatchID).Label; got != strings.Repeat("模", 26) {
		t.Fatalf("long label = %q (%d bytes)", got, len(got))
	}

	git, root := newGitStore(t, time.Hour)
	if _, err := git.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "Draft node"}},
		Batch: &BatchSpec{Via: "copilot-draft:d9", Label: "ollama · qwen3:8b"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := git.FlushHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, message := headMessage(t, root); !strings.Contains(message,
		"Draft node: created from a Copilot draft (copilot-draft:d9, ollama · qwen3:8b)") {
		t.Fatalf("message = %q", message)
	}
}

// batchHeavyNodes are nodes whose undo snapshot is large: many long
// acceptance criteria each.
func batchHeavyNodes(n int) []SubgraphNode {
	criteria := make([]string, engine.MaxAcceptanceItems)
	for i := range criteria {
		criteria[i] = strings.Repeat("驗", 200)
	}
	out := make([]SubgraphNode, n)
	for i := range out {
		out[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T", Acceptance: criteria}
	}
	return out
}

// One member's undone batches cannot grow the sidecar past what a read
// takes in, nor push another member's batch out (finding 33).
func TestTheBatchSidecarStaysBoundedAndShared(t *testing.T) {
	saved := maxBatchStoreBytes
	maxBatchStoreBytes = 256 << 10
	t.Cleanup(func() { maxBatchStoreBytes = saved })

	st := subgraphStore(t)
	dans, err := st.CreateSubgraph(batchDan, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "d", Title: "Dan's"}},
		Batch: &BatchSpec{Via: "copilot-draft:dan"},
	})
	if err != nil || !dans.Undoable {
		t.Fatalf("dan = %+v %v", dans, err)
	}
	for round := range 12 {
		result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
			Nodes: batchHeavyNodes(10),
			Batch: &BatchSpec{Via: fmt.Sprintf("manual-%d", round)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Undoable {
			t.Fatalf("round %d: not undoable", round)
		}
		undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
		if err != nil || len(undone.Trashed) != 10 {
			t.Fatalf("round %d: undo = %+v %v", round, undone, err)
		}
		info, err := os.Stat(st.batchesPath())
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > int64(maxBatchStoreBytes)+4096 {
			t.Fatalf("round %d: sidecar is %d bytes, budget %d", round, info.Size(), maxBatchStoreBytes)
		}
	}
	undone, err := st.UndoBatch(batchDan, dans.BatchID, BatchUndoOptions{})
	if err != nil || len(undone.Trashed) != 1 {
		t.Fatalf("dan's undo = %+v %v", undone, err)
	}
}

// The count limit also takes the busiest member's oldest batch first.
func TestTheBatchCountLimitTakesTheBusiestMembersOldest(t *testing.T) {
	saved := maxBatchRecords
	maxBatchRecords = 5
	t.Cleanup(func() { maxBatchRecords = saved })

	st := subgraphStore(t)
	dans, err := st.CreateSubgraph(batchDan, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "d", Title: "Dan's"}},
		Batch: &BatchSpec{Via: "copilot-draft:dan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var beas []string
	for i := range 8 {
		result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
			Nodes: []SubgraphNode{{Key: "b", Title: "Bea's"}},
			Batch: &BatchSpec{Via: fmt.Sprintf("copilot-draft:bea-%d", i)},
		})
		if err != nil || !result.Undoable {
			t.Fatalf("bea %d = %+v %v", i, result, err)
		}
		beas = append(beas, result.BatchID)
	}
	file, err := st.loadBatchesLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Batches) != 5 || file.Batches[dans.BatchID] == nil || file.Batches[beas[7]] == nil || file.Batches[beas[0]] != nil {
		t.Fatalf("kept %d records; dan's kept %v", len(file.Batches), file.Batches[dans.BatchID] != nil)
	}
}

// A sidecar already over the read limit (an older build let it grow) is
// started over by the next batch instead of leaving every batch undoable.
func TestAnOversizedSidecarIsStartedOver(t *testing.T) {
	st := subgraphStore(t)
	if err := os.MkdirAll(st.Root()+"/"+DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, maxBatchFileBytes+1)
	for i := range junk {
		junk[i] = ' '
	}
	if err := os.WriteFile(st.batchesPath(), junk, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:fresh"},
	})
	if err != nil || !result.Undoable {
		t.Fatalf("result = %+v %v", result, err)
	}
	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil || len(undone.Trashed) != 1 {
		t.Fatalf("undo = %+v %v", undone, err)
	}
}
