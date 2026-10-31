package server

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/store"
)

// seedRequiresOnly gives beta a dependency on alpha that exists only in its
// `requires` (a hand-written graph.yaml), next to a wired one on gamma.
func seedRequiresOnly(t *testing.T, st *store.Store) {
	t.Helper()
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph = graph.Clone()
	graph.Nodes = append(graph.Nodes, &engine.Node{ID: "gamma", Title: "Gamma"})
	graph.NodeByID("beta").Requires = "alpha and gamma"
	graph.Edges = []*engine.Edge{{From: "gamma", To: "beta"}}
	if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatalf("seed requires-only dependency: %v", err)
	}
}

func edgeBetween(graph *engine.Graph, from, to string) *engine.Edge {
	for _, edge := range graph.Edges {
		if edge != nil && edge.From == from && edge.To == to {
			return edge
		}
	}
	return nil
}

// The board draws a wire for a requires-only dependency, so restyling that wire
// has to work: the op writes the explicit edge it stands for, then styles it.
func TestSetEdgeStyleMaterialisesARequiresOnlyDependency(t *testing.T) {
	dashed, dotted := "dashed", "dotted"
	optional, required := engine.RelationOptional, engine.RelationRequired
	cases := []struct {
		name         string
		op           store.GraphOp
		wantRelation string
		wantLine     string
		wantRequires string
	}{
		{
			name:         "line style",
			op:           store.GraphOp{Kind: "set-edge-style", From: "alpha", To: "beta", Line: &dashed},
			wantRelation: engine.RelationRequired, wantLine: "dashed", wantRequires: "alpha and gamma",
		},
		{
			name:         "relation change",
			op:           store.GraphOp{Kind: "set-edge-style", From: "alpha", To: "beta", Relation: &optional},
			wantRelation: engine.RelationOptional, wantLine: "", wantRequires: "gamma",
		},
		{
			name: "required relation and line",
			op: store.GraphOp{
				Kind: "set-edge-style", From: "alpha", To: "beta", Relation: &required, Line: &dotted,
			},
			wantRelation: engine.RelationRequired, wantLine: "dotted", wantRequires: "alpha and gamma",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, st := opsTestServer(t)
			seedRequiresOnly(t, st)
			if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{tc.op}); err != nil {
				t.Fatalf("style requires-only dependency: %v", err)
			}
			graph, _, err := st.LoadGraph()
			if err != nil {
				t.Fatal(err)
			}
			edge := edgeBetween(graph, "alpha", "beta")
			if edge == nil {
				t.Fatalf("edges = %+v, want an explicit alpha->beta edge", graph.Edges)
			}
			if edge.Relation != tc.wantRelation || edge.Line != tc.wantLine {
				t.Fatalf("edge = %+v, want relation %q line %q", edge, tc.wantRelation, tc.wantLine)
			}
			if requires := graph.NodeByID("beta").Requires; requires != tc.wantRequires {
				t.Fatalf("beta requires = %q, want %q", requires, tc.wantRequires)
			}
			if edgeBetween(graph, "gamma", "beta") == nil || len(graph.Edges) != 2 {
				t.Fatalf("edges = %+v, want gamma->beta kept and one alpha->beta", graph.Edges)
			}
		})
	}
}

// A wire nothing names is still not a wire: no edge is invented for it.
func TestSetEdgeStyleStillRefusesAnUnknownDependency(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	dashed := "dashed"
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{
		Kind: "set-edge-style", From: "beta", To: "alpha", Line: &dashed,
	}}); err == nil {
		t.Fatal("styling a dependency nothing names was accepted")
	}
}

// Bend points and a label placement on a requires-only wire used to be swept
// as decorations of a missing edge. They are kept, with the edge they belong to.
func TestBendPointsOnARequiresOnlyDependencyAreKept(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph = graph.Clone()
	if graph.UI == nil {
		graph.UI = &engine.UIState{}
	}
	graph.UI.WireVertices = map[string][]engine.Position{"alpha->beta": {{X: 40, Y: 60}}}
	graph.UI.EdgeLabels = map[string]engine.EdgeLabelPlacement{"alpha->beta": {Ratio: 0.25}}
	if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatalf("save bend points: %v", err)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if vertices := graph.UI.WireVertices["alpha->beta"]; len(vertices) != 1 || vertices[0].X != 40 {
		t.Fatalf("wire vertices = %+v, want the alpha->beta bend kept", graph.UI.WireVertices)
	}
	if _, ok := graph.UI.EdgeLabels["alpha->beta"]; !ok {
		t.Fatalf("edge labels = %+v, want the alpha->beta placement kept", graph.UI.EdgeLabels)
	}
	edge := edgeBetween(graph, "alpha", "beta")
	if edge == nil || !edge.Blocks() {
		t.Fatalf("edges = %+v, want a required alpha->beta edge", graph.Edges)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "alpha and gamma" {
		t.Fatalf("beta requires = %q, want it unchanged", requires)
	}
	for _, issue := range engine.Validate(graph) {
		if issue.Severity == "error" {
			t.Fatalf("materialised graph has an error: %+v", issue)
		}
	}
}

// Removing a wire takes its bends with it, so a later save cannot bring the
// edge back from a stale bend.
func TestRemoveEdgeDropsItsBendPoints(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph = graph.Clone()
	if graph.UI == nil {
		graph.UI = &engine.UIState{}
	}
	graph.UI.WireVertices = map[string][]engine.Position{"gamma->beta": {{X: 1, Y: 2}}}
	if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{
		Kind: "remove-edge", From: "gamma", To: "beta",
	}}); err != nil {
		t.Fatal(err)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := graph.UI.WireVertices["gamma->beta"]; ok || edgeBetween(graph, "gamma", "beta") != nil {
		t.Fatalf("removed wire kept: edges=%+v vertices=%+v", graph.Edges, graph.UI.WireVertices)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "alpha" {
		t.Fatalf("beta requires = %q, want alpha", requires)
	}
}

// Adding a dependency that closes a loop is refused, and the loop is found
// through dependencies that live only in `requires` as well as through wires.
func TestAddEdgeRefusesACycleThroughARequiresOnlyDependency(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st) // beta waits for alpha (requires only) and gamma (wired)
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = st.ApplyGraphOps(identity.Local, []store.GraphOp{{Kind: "add-edge", From: "beta", To: "alpha"}})
	if err == nil {
		t.Fatal("a dependency closing a cycle through requires was accepted")
	}
	const cycle = "alpha -> beta -> alpha"
	if !strings.Contains(err.Error(), cycle) {
		t.Fatalf("error = %q, want it to name the cycle %q", err, cycle)
	}
	chinese := locale.Render(err, locale.Chinese)
	if chinese == err.Error() || !strings.Contains(chinese, "循環") || !strings.Contains(chinese, cycle) {
		t.Fatalf("zh-TW error = %q, want a translated sentence naming %q", chinese, cycle)
	}

	graph, after, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if after != rev || graph.NodeByID("alpha").Requires != "" || edgeBetween(graph, "beta", "alpha") != nil {
		t.Fatalf("refused cycle changed the graph: alpha=%+v edges=%+v", graph.NodeByID("alpha"), graph.Edges)
	}
}

// A longer loop is named hop by hop, wires and requires alike.
func TestAddEdgeNamesALongerCycle(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	_, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{Kind: "add-edge", From: "beta", To: "gamma"}})
	if err == nil || !strings.Contains(err.Error(), "gamma -> beta -> gamma") {
		t.Fatalf("error = %v, want the gamma -> beta -> gamma cycle named", err)
	}
}

// Promoting an optional wire to required adds a dependency too.
func TestSetEdgeStyleRefusesARequiredRelationThatClosesACycle(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph = graph.Clone()
	graph.Edges = append(graph.Edges, &engine.Edge{From: "beta", To: "alpha", Relation: engine.RelationOptional})
	if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatal(err)
	}
	required := engine.RelationRequired
	_, _, err = st.ApplyGraphOps(identity.Local, []store.GraphOp{{
		Kind: "set-edge-style", From: "beta", To: "alpha", Relation: &required,
	}})
	if err == nil || !strings.Contains(err.Error(), "alpha -> beta -> alpha") {
		t.Fatalf("error = %v, want the alpha -> beta -> alpha cycle named", err)
	}
}

// A dependency that does not close a loop is added as before.
func TestAddEdgeWithoutACycleStillLands(t *testing.T) {
	_, st := opsTestServer(t)
	seedRequiresOnly(t, st)
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{Kind: "add-edge", From: "alpha", To: "gamma"}}); err != nil {
		t.Fatalf("add acyclic dependency: %v", err)
	}
}
