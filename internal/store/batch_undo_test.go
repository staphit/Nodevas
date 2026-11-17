package store

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

var (
	batchBea = identity.Actor{ID: "u-bea", Name: "bea", Role: identity.RoleMember}
	batchDan = identity.Actor{ID: "u-dan", Name: "dan", Role: identity.RoleMember}
	batchAnn = identity.Actor{ID: "u-ann", Name: "ann", Role: identity.RoleAdmin}
)

// draftBatch commits a small Copilot draft as bea: a waits for intro, b for
// a, c stands alone, and finale (an existing node) now waits for b.
func draftBatch(t *testing.T, st *Store) SubgraphResult {
	t.Helper()
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "A", Body: "first", DependsOn: []string{"intro"}, Refs: []string{"note-01"}},
			{Key: "b", Title: "B", DependsOn: []string{"a"}, WriteAccess: "worker"},
			{Key: "c", Title: "C", Tags: []string{"x"}},
		},
		Edges:     []SubgraphEdge{{From: "b", To: "finale"}},
		Positions: map[string]SubgraphPosition{"a": {Col: 3, Row: 1}, "b": {Col: 4, Row: 1}},
		Batch:     &BatchSpec{Via: "copilot-draft:d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Undoable {
		t.Fatal("batch not undoable")
	}
	return result
}

func graphHas(t *testing.T, st *Store, id string) bool {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	return g.NodeByID(id) != nil
}

func inTrash(t *testing.T, st *Store, id string) bool {
	t.Helper()
	items, err := st.ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == "node" && item.NodeID == id {
			return true
		}
	}
	return false
}

func refusalCodes(refused []BatchRefusal) map[string]string {
	out := map[string]string{}
	for _, r := range refused {
		out[r.ID] = r.Code
	}
	return out
}

func TestUndoingABatchTrashesItsNodesAndTheirLinks(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)
	a, b, c := result.IDs["a"], result.IDs["b"], result.IDs["c"]

	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(undone.Trashed, []string{a, b, c}) || len(undone.Refused) != 0 {
		t.Fatalf("undo = %+v", undone)
	}
	g, _, _ := st.LoadGraph()
	for _, id := range []string{a, b, c} {
		if g.NodeByID(id) != nil {
			t.Fatalf("%s still on the board", id)
		}
		if !inTrash(t, st, id) {
			t.Fatalf("%s not in the trash", id)
		}
	}
	if finale := g.NodeByID("finale"); strings.Contains(finale.Requires, b) {
		t.Fatalf("finale still waits for b: %q", finale.Requires)
	}
	for _, e := range g.Edges {
		if e.From == b || e.To == a {
			t.Fatalf("edge %s->%s left behind", e.From, e.To)
		}
	}

	// A second undo is a no-op, not an error.
	again, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil || len(again.Trashed) != 0 || len(again.Refused) != 0 {
		t.Fatalf("second undo = %+v %v", again, err)
	}
}

func TestUndoRefusesNodesThatChanged(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "renamed", Title: "R"},
			{Key: "rewritten", Title: "W", Body: "draft body"},
			{Key: "started", Title: "S"},
			{Key: "claimed", Title: "C"},
			{Key: "busy", Title: "U"},
			{Key: "linked", Title: "L"},
			{Key: "moved", Title: "M"},
			{Key: "untouched", Title: "T"},
		},
		Positions: map[string]SubgraphPosition{"moved": {Col: 1, Row: 1}},
		Batch:     &BatchSpec{Via: "copilot-draft:d2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := result.IDs
	title := "Renamed by hand"
	x, y := 9.0, 9.0
	if _, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{
		{Kind: "node-metadata", NodeID: id["renamed"], Title: &title},
		// Wiring and moving a node are not edits of the node itself.
		{Kind: "add-edge", From: "intro", To: id["linked"]},
		{Kind: "move", NodeID: id["moved"], X: &x, Y: &y},
	}); err != nil {
		t.Fatal(err)
	}
	content, rev, err := st.LoadNodeContent(id["rewritten"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveNodeContent(identity.Local, id["rewritten"], content+"\nmore by hand\n", rev); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(id["started"], engine.StatusInProgress, "local", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNode(identity.Local, id["claimed"], "agent-7", time.Hour, ""); err != nil {
		t.Fatal(err)
	}

	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{
		Busy: func(nodeID string) (bool, string) {
			if nodeID == id["busy"] {
				return true, "run r-1 is working on it"
			}
			return false, ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		id["renamed"]:   BatchRefusedChanged,
		id["rewritten"]: BatchRefusedChanged,
		id["started"]:   BatchRefusedStatus,
		id["claimed"]:   BatchRefusedClaimed,
		id["busy"]:      BatchRefusedBusy,
	}
	if got := refusalCodes(undone.Refused); !reflect.DeepEqual(got, want) {
		t.Fatalf("refused = %+v", undone.Refused)
	}
	trashed := slices.Clone(undone.Trashed)
	slices.Sort(trashed)
	wantTrashed := []string{id["linked"], id["moved"], id["untouched"]}
	slices.Sort(wantTrashed)
	if !reflect.DeepEqual(trashed, wantTrashed) {
		t.Fatalf("trashed = %v, want %v", undone.Trashed, wantTrashed)
	}
	for _, kept := range []string{"renamed", "rewritten", "started", "claimed", "busy"} {
		if !graphHas(t, st, id[kept]) {
			t.Fatalf("%s was deleted", kept)
		}
	}
	for _, r := range undone.Refused {
		if r.Reason == "" {
			t.Fatalf("refusal without a reason: %+v", r)
		}
	}
}

func TestUndoIsForTheBatchsAuthorOrAnAdmin(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)

	_, err := st.UndoBatch(batchDan, result.BatchID, BatchUndoOptions{})
	var notFound *ErrBatchNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("dan undoing bea's batch: %v", err)
	}
	if _, err := st.RedoBatch(batchDan, result.BatchID); !errors.As(err, &notFound) {
		t.Fatalf("dan redoing bea's batch: %v", err)
	}
	// The refusal is the one an unknown batch gets: no existence leak.
	_, unknown := st.UndoBatch(batchDan, "b-000000000000000000000000", BatchUndoOptions{})
	if unknown == nil || unknown.Error() != err.Error() {
		t.Fatalf("unknown batch: %v, someone else's: %v", unknown, err)
	}
	if _, err := st.UndoBatch(batchBea, "../../etc", BatchUndoOptions{}); !errors.As(err, &notFound) {
		t.Fatalf("malformed id: %v", err)
	}
	if !graphHas(t, st, result.IDs["a"]) {
		t.Fatal("a refused undo deleted nodes")
	}

	undone, err := st.UndoBatch(batchAnn, result.BatchID, BatchUndoOptions{})
	if err != nil || len(undone.Trashed) != 3 {
		t.Fatalf("admin undo = %+v %v", undone, err)
	}
}

func TestUndoSkipsNodesAlreadyGone(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)
	if _, err := st.DeleteNodes(identity.Local, []string{result.IDs["c"]}); err != nil {
		t.Fatal(err)
	}
	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(undone.Trashed, []string{result.IDs["a"], result.IDs["b"]}) || len(undone.Refused) != 0 {
		t.Fatalf("undo = %+v", undone)
	}
}

// When every node was refused nothing was undone, so the batch stays
// undoable: put the change back and undo again.
func TestAnUndoThatTrashedNothingCanBeRetried(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(batchBea, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:d3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := result.IDs["a"]
	if _, err := st.ClaimNode(identity.Local, id, "agent-7", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil || len(undone.Trashed) != 0 || len(undone.Refused) != 1 {
		t.Fatalf("undo = %+v %v", undone, err)
	}
	if err := st.ReleaseClaim(id, "agent-7"); err != nil {
		t.Fatal(err)
	}
	undone, err = st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil || !reflect.DeepEqual(undone.Trashed, []string{id}) {
		t.Fatalf("retry = %+v %v", undone, err)
	}
}

func TestRedoRestoresTheBatchAndItsLinksIntoExistingNodes(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)
	a, b, c := result.IDs["a"], result.IDs["b"], result.IDs["c"]
	g, _, _ := st.LoadGraph()
	wantA, wantB := *g.NodeByID(a), *g.NodeByID(b)
	bodyBefore, _, err := st.LoadNodeContent(a)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to redo before an undo.
	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil || len(redone.Restored) != 0 {
		t.Fatalf("redo before undo = %+v %v", redone, err)
	}

	if _, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{}); err != nil {
		t.Fatal(err)
	}
	redone, err = st.RedoBatch(batchBea, result.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(redone.Restored, []string{a, b, c}) || len(redone.Skipped) != 0 || redone.Links != 1 {
		t.Fatalf("redo = %+v", redone)
	}
	g, _, _ = st.LoadGraph()
	gotA, gotB := g.NodeByID(a), g.NodeByID(b)
	if gotA == nil || gotB == nil || g.NodeByID(c) == nil {
		t.Fatal("nodes not restored")
	}
	// What a plain trash restore loses comes back: refs, write access, and
	// the dependency the batch put into finale.
	if !reflect.DeepEqual(gotA.Refs, wantA.Refs) || gotA.Requires != wantA.Requires {
		t.Fatalf("a = %+v, want %+v", gotA, wantA)
	}
	if gotB.WriteAccess != wantB.WriteAccess || gotB.Requires != wantB.Requires {
		t.Fatalf("b = %+v, want %+v", gotB, wantB)
	}
	if !strings.Contains(g.NodeByID("finale").Requires, b) {
		t.Fatalf("finale does not wait for b again: %q", g.NodeByID("finale").Requires)
	}
	if got := g.UI.Positions[a]; got != (engine.Position{X: 3, Y: 1}) {
		t.Fatalf("a's position = %+v", got)
	}
	if inTrash(t, st, a) {
		t.Fatal("a is still in the trash")
	}
	bodyAfter, _, err := st.LoadNodeContent(a)
	if err != nil || bodyAfter != bodyBefore {
		t.Fatalf("a's document:\n%s\nwant\n%s (%v)", bodyAfter, bodyBefore, err)
	}

	// A second redo is a no-op; the batch can be undone again.
	again, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil || len(again.Restored) != 0 {
		t.Fatalf("second redo = %+v %v", again, err)
	}
	undone, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{})
	if err != nil || len(undone.Trashed) != 3 {
		t.Fatalf("undo after redo = %+v %v", undone, err)
	}
}

// A link whose existing endpoint has since gone is skipped, and the rest of
// the batch still comes back.
func TestRedoSkipsLinksToNodesThatAreGone(t *testing.T) {
	st := subgraphStore(t)
	result := draftBatch(t, st)
	if _, err := st.UndoBatch(batchBea, result.BatchID, BatchUndoOptions{}); err != nil {
		t.Fatal(err)
	}
	// finale (the link's target) and intro (a's dependency) both go.
	if _, err := st.DeleteNodes(identity.Local, []string{"finale", "intro"}); err != nil {
		t.Fatal(err)
	}
	redone, err := st.RedoBatch(batchBea, result.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(redone.Restored) != 3 || redone.Links != 0 {
		t.Fatalf("redo = %+v", redone)
	}
	g, _, _ := st.LoadGraph()
	if a := g.NodeByID(result.IDs["a"]); a == nil || a.Requires != "" {
		t.Fatalf("a = %+v", a)
	}
	if issues := engine.Validate(g); hasErrorIssue(issues) {
		t.Fatalf("redo left errors: %+v", issues)
	}
}

func hasErrorIssue(issues []engine.Issue) bool {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}
