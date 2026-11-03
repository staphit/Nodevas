package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// holdGraphWrite makes the next graph.yaml replacement fail after the node
// files have been written: on Windows an open handle refuses the rename, on
// Unix a read-only project root refuses the temp file.
func holdGraphWrite(t *testing.T, st *Store) {
	t.Helper()
	if runtime.GOOS == "windows" {
		file, err := os.Open(st.GraphPath())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { file.Close() })
		return
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.MkdirAll(filepath.Dir(st.NodePath("x")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(st.Root(), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(st.Root(), 0o755) })
}

// A failure on the last write of the batch (graph.yaml) rolls back the node
// files already written: nothing of the call is left behind.
func TestASubgraphWriteFailureMidwayLeavesNothing(t *testing.T) {
	st := subgraphStore(t)
	before := snapshot(t, st)
	holdGraphWrite(t, st)
	_, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "A", Body: "first", Refs: []string{"note-01"}},
			{Key: "b", Title: "B", Body: "second", DependsOn: []string{"a"}},
		},
		Edges: []SubgraphEdge{{From: "b", To: "intro"}},
	})
	if err == nil {
		t.Fatal("the graph write was expected to fail")
	}
	if !strings.Contains(err.Error(), "graph.yaml") && !strings.Contains(err.Error(), "denied") && !strings.Contains(err.Error(), "permission") {
		t.Fatalf("failed before the graph write: %v", err)
	}
	if after := snapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("a failed call left files behind:\nbefore %v\nafter %v", keys(before), keys(after))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// An agent wires what it may and nothing more: no reference op on, and no
// dependency into, a node above its rank, and an unknown kind is refused
// before anything is written.
func TestSubgraphAgentRulesMirrorTheSingleOps(t *testing.T) {
	st := subgraphStore(t)
	worker := agentActor(identity.AgentWorker)
	before := snapshot(t, st)

	// A human-only node's own refs cannot be changed by an agent via ops.
	if _, _, err := st.ApplyGraphOps(worker, []GraphOp{{Kind: "add-ref", NodeID: "finale", Ref: "note-01"}}); err == nil {
		t.Fatal("agent added a reference to a human-only node")
	}
	// Nor through create_subgraph's dependsOn pointing a human-only node
	// at a new one (edges into finale).
	_, err := st.CreateSubgraph(worker, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Edges: []SubgraphEdge{{From: "a", To: "finale"}},
	})
	var denied *ErrNodeWriteDenied
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v", err)
	}
	for _, kind := range []string{"custom-type-zz", "bug"} {
		_, err = st.CreateSubgraph(worker, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", Kind: kind}}})
		var typed *ErrNodeTypeDenied
		if !errors.As(err, &typed) {
			t.Fatalf("kind %q: err = %v", kind, err)
		}
	}
	// A reference never forms a cycle: a note may reference a scene that
	// references it, and a dependency loop through refs is not a loop.
	if after := snapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("refused calls wrote files")
	}
	result, err := st.CreateSubgraph(worker, SubgraphSpec{Nodes: []SubgraphNode{
		{Key: "n", Title: "Sheet", Kind: "note", Refs: []string{"s"}},
		{Key: "s", Title: "Scene", Kind: "scene", Refs: []string{"n"}, DependsOn: []string{"intro"}},
	}})
	if err != nil {
		t.Fatalf("mutual references refused: %v", err)
	}
	for _, issue := range result.Issues {
		if strings.Contains(issue.Msg, "cycle") {
			t.Fatalf("a reference loop was reported as a cycle: %+v", issue)
		}
	}
}

// Deleting a note drops every reference to it, so no dangling ref is left
// for Problems or the prompt.
func TestDeletingANoteUsedBySubgraphNodesPrunesRefs(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{
		{Key: "a", Title: "A", Kind: "scene", Refs: []string{"note-01"}},
		{Key: "b", Title: "B", Kind: "scene", Refs: []string{"note-01"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteNode(identity.Local, "note-01"); err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	for _, key := range []string{"a", "b"} {
		if refs := g.NodeByID(result.IDs[key]).Refs; len(refs) != 0 {
			t.Fatalf("%s still references %v", key, refs)
		}
	}
	for _, issue := range engine.Validate(g) {
		if strings.Contains(issue.Msg, "note-01") {
			t.Fatalf("deleted note surfaced in Problems: %+v", issue)
		}
	}
}
