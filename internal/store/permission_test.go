package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// permissionTestStore writes a one-node project whose node carries the given
// write access, plus its markdown document.
func permissionTestStore(t *testing.T, access string) *Store {
	t.Helper()
	root := t.TempDir()
	graph := &engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "target", Title: "Target", WriteAccess: access}},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nodes", "target.md"), []byte("# target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewStore(root)
}

func agentActor(class identity.AgentClass) identity.Actor {
	actor := identity.Local
	actor.Agent = class
	return actor
}

func stringPtr(v string) *string { return &v }

// The whole write-permission decision, exercised through every gated store
// operation: an actor may modify a node when its rank meets the node's
// requirement, and a person is never refused.
func TestNodeWriteAccessGatesEveryWriteOperation(t *testing.T) {
	operations := []struct {
		name string
		call func(st *Store, actor identity.Actor) error
	}{
		{"ApplyGraphOps node-metadata", func(st *Store, actor identity.Actor) error {
			_, _, err := st.ApplyGraphOps(actor, []GraphOp{{
				Kind: "node-metadata", NodeID: "target", Title: stringPtr("Renamed"),
			}})
			return err
		}},
		{"SaveNodeContent", func(st *Store, actor identity.Actor) error {
			_, rev, err := st.LoadNodeContent("target")
			if err != nil {
				return err
			}
			_, _, err = st.SaveNodeContent(actor, "target", "# rewritten\n", rev)
			return err
		}},
		{"DeleteNodes", func(st *Store, actor identity.Actor) error {
			_, err := st.DeleteNodes(actor, []string{"target"})
			return err
		}},
		{"ReportStatus", func(st *Store, actor identity.Actor) error {
			_, err := st.ReportStatus(actor, "target", engine.StatusDone, "tester", "", "", "")
			return err
		}},
		{"ClaimNode", func(st *Store, actor identity.Actor) error {
			_, err := st.ClaimNode(actor, "target", "agent-1", 0, "")
			return err
		}},
		{"CreateNodePage", func(st *Store, actor identity.Actor) error {
			_, _, _, err := st.CreateNodePage(actor, "target", "Notes", "")
			return err
		}},
	}
	actors := []struct {
		name  string
		actor identity.Actor
		rank  int
	}{
		{"human", identity.Local, 3},
		{"orchestrator", agentActor(identity.AgentOrchestrator), 2},
		{"worker", agentActor(identity.AgentWorker), 1},
	}
	accesses := []struct {
		value string
		rank  int
	}{
		{engine.WriteAccessAll, 0},
		{engine.WriteAccessWorker, 1},
		{engine.WriteAccessOrchestrator, 2},
		{engine.WriteAccessHumanOnly, 3},
	}
	for _, operation := range operations {
		for _, actor := range actors {
			for _, access := range accesses {
				st := permissionTestStore(t, access.value)
				err := operation.call(st, actor.actor)
				wantAllowed := actor.rank >= access.rank
				var denied *ErrNodeWriteDenied
				if wantAllowed {
					if err != nil {
						t.Errorf("%s / %s / access %q: err = %v, want success",
							operation.name, actor.name, access.value, err)
					}
					continue
				}
				if !errors.As(err, &denied) {
					t.Errorf("%s / %s / access %q: err = %v, want ErrNodeWriteDenied",
						operation.name, actor.name, access.value, err)
					continue
				}
				if denied.NodeID != "target" {
					t.Errorf("%s / %s / access %q: denied node = %q",
						operation.name, actor.name, access.value, denied.NodeID)
				}
			}
		}
	}
}

func TestNodeWriteDeniedSaysWhatToDoNext(t *testing.T) {
	worker := agentActor(identity.AgentWorker)
	err := checkNodeWrite(worker, &engine.Node{ID: "sealed", WriteAccess: engine.WriteAccessHumanOnly})
	if got, want := err.Error(), `node "sealed" is human-only: agents may not modify it`; got != want {
		t.Errorf("human-only message = %q, want %q", got, want)
	}
	err = checkNodeWrite(worker, &engine.Node{ID: "planned", WriteAccess: engine.WriteAccessOrchestrator})
	if got, want := err.Error(), `node "planned" requires orchestrator access; this agent is a worker`; got != want {
		t.Errorf("outranked message = %q, want %q", got, want)
	}
}

// SaveGraph replaces the whole file, so the gate applies per node and by the
// access each node had before the save: untouched protected nodes pass, new
// nodes are anyone's to add, and a changed protected node is refused.
func TestSaveGraphGatesOnlyTheNodesTheSaveChanges(t *testing.T) {
	root := t.TempDir()
	original := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "guarded", Title: "Guarded", WriteAccess: engine.WriteAccessHumanOnly},
			{ID: "open", Title: "Open"},
		},
	}
	data, err := engine.MarshalGraph(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(root)
	worker := agentActor(identity.AgentWorker)

	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.NodeByID("open").Title = "Open, renamed"
	graph.Nodes = append(graph.Nodes, &engine.Node{ID: "fresh", Title: "Fresh"})
	rev, err = st.SaveGraph(worker, graph, rev)
	if err != nil {
		t.Fatalf("save leaving the guarded node untouched: %v", err)
	}

	graph, rev, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.NodeByID("guarded").Title = "Guarded, renamed"
	if _, err := st.SaveGraph(worker, graph, rev); err == nil {
		t.Fatal("changing a human-only node through a graph save succeeded for a worker")
	} else {
		var denied *ErrNodeWriteDenied
		if !errors.As(err, &denied) || denied.NodeID != "guarded" {
			t.Fatalf("err = %v, want ErrNodeWriteDenied for guarded", err)
		}
	}
}

func TestCreateNodeNormalizesAllToUnrestricted(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	id, err := st.CreateNode(&engine.Node{ID: "fresh", Title: "Fresh", WriteAccess: "all"}, "")
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.NodeByID(id).WriteAccess; got != engine.WriteAccessAll {
		t.Fatalf("stored write access = %q, want the empty value", got)
	}
	content, _, err := st.LoadNodeContent(id)
	if err != nil {
		t.Fatal(err)
	}
	nf, err := engine.ParseNodeFile([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := nf.Meta["write_access"]; exists {
		t.Fatalf("frontmatter carries write_access for an unrestricted node: %v", nf.Meta)
	}
	if _, err := st.CreateNode(&engine.Node{ID: "junk", WriteAccess: "robots"}, ""); err == nil {
		t.Fatal("creating a node with an unknown write_access succeeded")
	}
}
