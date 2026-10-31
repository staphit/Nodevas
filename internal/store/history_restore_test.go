package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// restoreStore has a node an agent may write, a human-only one, and one
// snapshot of each node file and of graph.yaml to restore.
func restoreStore(t *testing.T) *Store {
	t.Helper()
	st := NewStore(t.TempDir())
	g := &engine.Graph{Version: 1, Nodes: []*engine.Node{
		{ID: "open", Title: "Open", Kind: "scene", WriteAccess: engine.WriteAccessWorker},
		{ID: "locked", Title: "Locked", WriteAccess: engine.WriteAccessHumanOnly},
	}}
	if _, err := st.SaveGraph(identity.Local, g, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Root(), "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"open", "locked", "gone"} {
		path := filepath.Join(st.Root(), "nodes", id+".md")
		old := "---\nid: " + id + "\nkind: gate\nwrite_access: \"\"\n---\n\nold body\n"
		if err := st.WriteVersioned(path, []byte(old)); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteVersioned(path, []byte("---\nid: "+id+"\n---\n\nnew body\n")); err != nil {
			t.Fatal(err)
		}
	}
	graphPath := st.GraphPath()
	current, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersioned(graphPath, current); err != nil {
		t.Fatal(err)
	}
	return st
}

func latestSnapshot(t *testing.T, st *Store, rel string) string {
	t.Helper()
	versions, err := st.ListHistory(rel)
	if err != nil || len(versions) == 0 {
		t.Fatalf("history of %s: %v %v", rel, versions, err)
	}
	return versions[0].Name
}

func TestAnAgentCannotRestoreGraphYAML(t *testing.T) {
	st := restoreStore(t)
	before, _ := os.ReadFile(st.GraphPath())
	err := st.RestoreHistory(agentActor(identity.AgentOrchestrator), "graph.yaml", latestSnapshot(t, st, "graph.yaml"))
	var denied *ErrAgentRestoreDenied
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v, want ErrAgentRestoreDenied", err)
	}
	after, _ := os.ReadFile(st.GraphPath())
	if string(before) != string(after) {
		t.Fatal("graph.yaml changed")
	}
	// A person still can.
	if err := st.RestoreHistory(identity.Local, "graph.yaml", latestSnapshot(t, st, "graph.yaml")); err != nil {
		t.Fatalf("human restore: %v", err)
	}
}

func TestAnAgentRestoreFollowsWriteAccessAndTheGraph(t *testing.T) {
	st := restoreStore(t)
	agent := agentActor(identity.AgentWorker)

	err := st.RestoreHistory(agent, "nodes/locked.md", latestSnapshot(t, st, "nodes/locked.md"))
	var writeDenied *ErrNodeWriteDenied
	if !errors.As(err, &writeDenied) {
		t.Fatalf("human-only restore: err = %v", err)
	}

	err = st.RestoreHistory(agent, "nodes/gone.md", latestSnapshot(t, st, "nodes/gone.md"))
	var restoreDenied *ErrAgentRestoreDenied
	if !errors.As(err, &restoreDenied) || !strings.Contains(err.Error(), "not on the board") {
		t.Fatalf("off-board restore: err = %v", err)
	}

	if err := st.RestoreHistory(agent, "nodes/open.md", latestSnapshot(t, st, "nodes/open.md")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.Root(), "nodes", "open.md"))
	if err != nil {
		t.Fatal(err)
	}
	nf, err := engine.ParseNodeFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if nf.Meta["kind"] != "scene" || nf.Meta["write_access"] != engine.WriteAccessWorker {
		t.Fatalf("frontmatter not taken from the graph: %v", nf.Meta)
	}
	if !strings.Contains(nf.Body, "old body") {
		t.Fatalf("body not restored: %q", nf.Body)
	}
	// A person's restore is unchanged: the snapshot lands as it was.
	if err := st.RestoreHistory(identity.Local, "nodes/locked.md", latestSnapshot(t, st, "nodes/locked.md")); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryNodeID(t *testing.T) {
	for _, c := range []struct {
		rel      string
		id       string
		main, ok bool
	}{
		{"nodes/a.md", "a", true, true},
		{"nodes/a.pages/p1.md", "a", false, true},
		{"graph.yaml", "", false, false},
	} {
		id, main, ok := historyNodeID(c.rel)
		if id != c.id || main != c.main || ok != c.ok {
			t.Fatalf("historyNodeID(%q) = %q %v %v", c.rel, id, main, ok)
		}
	}
}
