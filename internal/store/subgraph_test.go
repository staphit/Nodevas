package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// subgraphStore is a story board: a note, an open scene and a human-only one.
func subgraphStore(t *testing.T) *Store {
	t.Helper()
	st := NewStore(t.TempDir())
	g := &engine.Graph{Version: 1, Nodes: []*engine.Node{
		{ID: "note-01", Title: "角色卡", Kind: "note"},
		{ID: "intro", Title: "Intro", Kind: "scene"},
		{ID: "finale", Title: "Finale", Kind: "scene", WriteAccess: engine.WriteAccessHumanOnly},
	}}
	if _, err := st.SaveGraph(identity.Local, g, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

// snapshot is graph.yaml plus every node file, to show nothing was written.
func snapshot(t *testing.T, st *Store) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(st.Root(), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(st.Root(), path)
		rel = filepath.ToSlash(rel)
		if rel == "graph.yaml" || strings.HasPrefix(rel, "nodes/") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestASubgraphIsCreatedInOneWrite(t *testing.T) {
	st := subgraphStore(t)
	_, rev, _ := st.LoadGraph()
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "第二場", Kind: "scene", Body: "Write it.", DependsOn: []string{"intro"}, Refs: []string{"note-01"}},
			{Key: "b", Title: "第三場", Kind: "scene", DependsOn: []string{"a"}, Refs: []string{"note-01", "c"}},
			{Key: "c", Title: "地圖", Kind: "note", Tags: []string{"world"}},
		},
		Edges: []SubgraphEdge{{From: "b", To: "finale"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IDs) != 3 || len(result.Created) != 3 || result.Rev == "" || result.Rev == rev {
		t.Fatalf("result = %+v", result)
	}
	if result.Dependencies != 3 || result.References != 3 {
		t.Fatalf("counts = %d dependencies, %d references", result.Dependencies, result.References)
	}
	g, newRev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if newRev != result.Rev {
		t.Fatal("result rev is not the graph's")
	}
	a, b, c := g.NodeByID(result.IDs["a"]), g.NodeByID(result.IDs["b"]), g.NodeByID(result.IDs["c"])
	if a.Requires != "intro" || b.Requires != a.ID || !strings.Contains(g.NodeByID("finale").Requires, b.ID) {
		t.Fatalf("requires: a=%q b=%q finale=%q", a.Requires, b.Requires, g.NodeByID("finale").Requires)
	}
	if !reflect.DeepEqual(a.Refs, []string{"note-01"}) || !reflect.DeepEqual(b.Refs, []string{"note-01", c.ID}) {
		t.Fatalf("refs: a=%v b=%v", a.Refs, b.Refs)
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.From+">"+e.To] = true
	}
	for _, want := range []string{"intro>" + a.ID, a.ID + ">" + b.ID, b.ID + ">finale"} {
		if !edges[want] {
			t.Fatalf("edge %s missing: %v", want, edges)
		}
	}
	data, err := os.ReadFile(st.NodePath(a.ID))
	if err != nil || !strings.Contains(string(data), "Write it.") || !strings.Contains(string(data), "kind: scene") {
		t.Fatalf("node file: %s %v", data, err)
	}
	// The frontmatter of the existing node that gained a dependency follows.
	finale, _ := os.ReadFile(st.NodePath("finale"))
	if !strings.Contains(string(finale), b.ID) {
		t.Fatalf("finale frontmatter not synced:\n%s", finale)
	}
}

func TestASubgraphCycleWritesNothing(t *testing.T) {
	st := subgraphStore(t)
	before := snapshot(t, st)
	// intro waits for the new node, which waits for intro.
	_, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "x", Title: "X", DependsOn: []string{"intro"}}},
		Edges: []SubgraphEdge{{From: "x", To: "intro"}},
	})
	var invalid *ErrInvalidSubgraph
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want a cycle", err)
	}
	if after := snapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("files changed:\nbefore %v\nafter %v", before, after)
	}
}

func TestSubgraphRefusals(t *testing.T) {
	st := subgraphStore(t)
	cases := []struct {
		name  string
		spec  SubgraphSpec
		field string
	}{
		{"unknown dependsOn", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", DependsOn: []string{"nope"}}}}, "nodes[0].dependsOn[0]"},
		{"unknown ref", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}, {Key: "b", Title: "B", Refs: []string{"a", "zz"}}}}, "nodes[1].refs[1]"},
		{"edge to a new node", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}, {Key: "b", Title: "B"}}, Edges: []SubgraphEdge{{From: "a", To: "b"}}}, "edges[0].to"},
		{"key is an existing id", SubgraphSpec{Nodes: []SubgraphNode{{Key: "intro", Title: "A"}}}, "nodes[0].key"},
		{"duplicate key", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}, {Key: "a", Title: "B"}}}, "nodes[1].key"},
		{"bad key", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a b", Title: "A"}}}, "nodes[0].key"},
		{"no title", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "  "}}}, "nodes[0].title"},
		{"self dependency", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", DependsOn: []string{"a"}}}}, "nodes[0].dependsOn[0]"},
		{"bad priority", SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", Priority: "asap"}}}, "nodes[0] (key a)"},
	}
	before := snapshot(t, st)
	for _, c := range cases {
		_, err := st.CreateSubgraph(identity.Local, c.spec)
		var invalid *ErrInvalidSubgraph
		if !errors.As(err, &invalid) || invalid.Field != c.field {
			t.Errorf("%s: err = %v (%+v), want field %s", c.name, err, invalid, c.field)
		}
	}
	_, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", Kind: "custom-type-9"}}})
	var typed *ErrNodeTypeDenied
	if !errors.As(err, &typed) || typed.Code != CodeUnknownNodeType || !strings.HasPrefix(typed.Msg, "nodes[0].kind: ") {
		t.Fatalf("unknown kind: %v", err)
	}
	if after := snapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("a refused call wrote files")
	}
}

func TestSubgraphPermissions(t *testing.T) {
	st := subgraphStore(t)
	worker := agentActor(identity.AgentWorker)
	before := snapshot(t, st)
	_, err := st.CreateSubgraph(worker, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Edges: []SubgraphEdge{{From: "a", To: "finale"}},
	})
	var denied *ErrNodeWriteDenied
	if !errors.As(err, &denied) || denied.NodeID != "finale" {
		t.Fatalf("edge into a human-only node: %v", err)
	}
	if after := snapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("a refused call wrote files")
	}
	// A new human-only node may still be wired to another new node.
	result, err := st.CreateSubgraph(worker, SubgraphSpec{Nodes: []SubgraphNode{
		{Key: "a", Title: "A"},
		{Key: "b", Title: "B", WriteAccess: "human-only", DependsOn: []string{"a"}, Refs: []string{"note-01"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	if b := g.NodeByID(result.IDs["b"]); b.WriteAccess != engine.WriteAccessHumanOnly || b.Requires != result.IDs["a"] {
		t.Fatalf("b = %+v", b)
	}
}

func TestSubgraphSizeLimits(t *testing.T) {
	st := subgraphStore(t)
	many := make([]SubgraphNode, maxSubgraphNodes+1)
	for i := range many {
		many[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T"}
	}
	bigBody := strings.Repeat("x", maxSubgraphBodyBytes+1)
	total := make([]SubgraphNode, 17)
	for i := range total {
		total[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T", Body: strings.Repeat("x", maxSubgraphBodyBytes)}
	}
	for name, spec := range map[string]SubgraphSpec{
		"101 nodes":   {Nodes: many},
		"a big body":  {Nodes: []SubgraphNode{{Key: "a", Title: "A", Body: bigBody}}},
		"total bytes": {Nodes: total},
	} {
		_, err := st.CreateSubgraph(identity.Local, spec)
		var tooLarge *ErrSubgraphTooLarge
		if !errors.As(err, &tooLarge) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSubgraphIDsSkipTheTrash(t *testing.T) {
	st := subgraphStore(t)
	first, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteNodes(identity.Local, []string{first.IDs["a"]}); err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}, {Key: "b", Title: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.IDs["a"] == first.IDs["a"] || second.IDs["a"] == second.IDs["b"] {
		t.Fatalf("ids reused: first %v, second %v", first.IDs, second.IDs)
	}
}
