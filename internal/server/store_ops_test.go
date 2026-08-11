package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/store"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
)

func opsTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	server, pm := twoProjectServer(t)
	st := pm.Store()
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "alpha", Title: "Alpha"},
			{ID: "beta", Title: "Beta"},
		},
	}
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveGraph(graph, rev); err != nil {
		t.Fatalf("seed graph: %v", err)
	}
	return server, st
}

func TestGraphOpsMoveAndEdge(t *testing.T) {
	server, st := opsTestServer(t)

	body := `{"ops":[
		{"kind":"move","nodeId":"alpha","x":10,"y":20},
		{"kind":"add-edge","from":"alpha","to":"beta"}
	]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if position := graph.UI.Positions["alpha"]; position.X != 10 || position.Y != 20 {
		t.Fatalf("position = %+v, want {10 20}", position)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].From != "alpha" {
		t.Fatalf("edges = %+v", graph.Edges)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "alpha" {
		t.Fatalf("beta requires = %q, want alpha", requires)
	}
	if blocked := engine.Blocked(graph, nil)["beta"]; len(blocked) != 1 || blocked[0] != "alpha" {
		t.Fatalf("beta blocked by = %v, want [alpha]", blocked)
	}
	content, _, err := st.LoadNodeContent("beta")
	if err != nil {
		t.Fatal(err)
	}
	nodeFile, err := engine.ParseNodeFile([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if requires, exists := nodeFile.Meta["requires"]; !exists || requires != "alpha" {
		t.Fatalf("beta frontmatter requires = %#v, want alpha", requires)
	}
	if _, err := st.ClaimNode("beta", "agent-before-remove", time.Minute, ""); err == nil {
		t.Fatal("agent claimed beta while its required edge was blocked")
	} else {
		var notClaimable *store.ErrNotClaimable
		if !errors.As(err, &notClaimable) {
			t.Fatalf("claim beta error = %T %v, want ErrNotClaimable", err, err)
		}
	}

	remove := `{"ops":[{"kind":"remove-edge","from":"alpha","to":"beta"}]}`
	removeRequest := httptest.NewRequest(
		http.MethodPost, "/api/graph/ops", strings.NewReader(remove))
	removeRequest.Header.Set("Content-Type", "application/json")
	removeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(removeResponse, removeRequest)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("remove status = %d, body = %s", removeResponse.Code, removeResponse.Body)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 0 {
		t.Fatalf("edges after removal = %+v", graph.Edges)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "" {
		t.Fatalf("beta requires after removal = %q, want empty", requires)
	}
	if _, blocked := engine.Blocked(graph, nil)["beta"]; blocked {
		t.Fatal("Go readiness still blocks beta after its required edge was removed")
	}
	if _, err := st.ClaimNode("beta", "agent-after-remove", time.Minute, ""); err != nil {
		t.Fatalf("agent could not claim beta after required edge removal: %v", err)
	}
	content, _, err = st.LoadNodeContent("beta")
	if err != nil {
		t.Fatal(err)
	}
	nodeFile, err = engine.ParseNodeFile([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := nodeFile.Meta["requires"]; exists {
		t.Fatalf("beta frontmatter still contains requires: %+v", nodeFile.Meta)
	}
}

func TestGraphOpsRelationChangesRewriteOnlyTheirRequirement(t *testing.T) {
	_, st := opsTestServer(t)
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.Nodes = append(graph.Nodes, &engine.Node{ID: "gamma", Title: "Gamma"})
	graph.NodeByID("beta").Requires = "alpha and (gamma or flag(approved))"
	graph.Edges = []*engine.Edge{
		{From: "alpha", To: "beta"},
		{From: "gamma", To: "beta"},
	}
	if _, err := st.SaveGraph(graph, rev); err != nil {
		t.Fatal(err)
	}

	optional := engine.RelationOptional
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{{
		Kind: "set-edge-style", From: "alpha", To: "beta", Relation: &optional,
	}}); err != nil {
		t.Fatal(err)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.Edges[0].Relation != engine.RelationOptional {
		t.Fatalf("alpha edge relation = %q, want optional", graph.Edges[0].Relation)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "gamma or flag(approved)" {
		t.Fatalf("beta requires = %q, want remaining expression", requires)
	}

	required := engine.RelationRequired
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{{
		Kind: "set-edge-style", From: "alpha", To: "beta", Relation: &required,
	}}); err != nil {
		t.Fatal(err)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "(gamma or flag(approved)) and alpha" {
		t.Fatalf("beta requires after promotion = %q", requires)
	}
}

func TestGraphOpsRelationRemovalLeavesRequiresAlone(t *testing.T) {
	for _, relation := range []string{engine.RelationOptional, engine.RelationDeprecated} {
		t.Run(relation, func(t *testing.T) {
			_, st := opsTestServer(t)
			graph, rev, err := st.LoadGraph()
			if err != nil {
				t.Fatal(err)
			}
			// A malformed condition is useful here: deleting a decorative edge
			// must not parse or rewrite the executable condition at all.
			graph.NodeByID("beta").Requires = "alpha and"
			graph.Edges = []*engine.Edge{{From: "alpha", To: "beta", Relation: relation}}
			if _, err := st.SaveGraph(graph, rev); err != nil {
				t.Fatal(err)
			}

			if _, _, err := st.ApplyGraphOps([]store.GraphOp{{
				Kind: "remove-edge", From: "alpha", To: "beta",
			}}); err != nil {
				t.Fatalf("remove %s edge: %v", relation, err)
			}
			graph, _, err = st.LoadGraph()
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.Edges) != 0 || graph.NodeByID("beta").Requires != "alpha and" {
				t.Fatalf("relation removal rewrote semantics: node=%+v edges=%+v", graph.NodeByID("beta"), graph.Edges)
			}
		})
	}
}

func TestGraphOpsRejectDirectChangesToGateOwnedEdges(t *testing.T) {
	_, st := opsTestServer(t)
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.NodeByID("beta").Requires = "alpha"
	graph.Edges = []*engine.Edge{{From: "alpha", To: "beta"}}
	if graph.UI == nil {
		graph.UI = &engine.UIState{}
	}
	graph.UI.LogicGates = []engine.LogicGate{{
		ID: "gate-1", Operator: "must", Inputs: []string{"alpha"}, Output: "beta",
	}}
	if _, err := st.SaveGraph(graph, rev); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.ApplyGraphOps([]store.GraphOp{{
		Kind: "remove-edge", From: "alpha", To: "beta",
	}}); err == nil || !strings.Contains(err.Error(), "gate-1") {
		t.Fatalf("gate-owned removal error = %v", err)
	}
	optional := engine.RelationOptional
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{{
		Kind: "set-edge-style", From: "alpha", To: "beta", Relation: &optional,
	}}); err == nil || !strings.Contains(err.Error(), "gate-1") {
		t.Fatalf("gate-owned relation error = %v", err)
	}

	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("beta").Requires != "alpha" || len(graph.Edges) != 1 || !graph.Edges[0].Blocks() {
		t.Fatalf("rejected gate edits changed graph: node=%+v edges=%+v", graph.NodeByID("beta"), graph.Edges)
	}
}

// A metadata op names one field, so two people editing different fields of the
// same node no longer overwrite each other.
func TestGraphOpsMetadataLeavesOtherFieldsAlone(t *testing.T) {
	_, st := opsTestServer(t)

	title := "改過的標題"
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{
		{Kind: "node-metadata", NodeID: "alpha", Title: &title},
	}); err != nil {
		t.Fatal(err)
	}
	priority := "high"
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{
		{Kind: "node-metadata", NodeID: "alpha", Priority: &priority},
	}); err != nil {
		t.Fatal(err)
	}

	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	node := graph.NodeByID("alpha")
	if node.Title != title || node.Priority != priority {
		t.Fatalf("node = %+v, want both edits kept", node)
	}
}

// The point of the command path: concurrent edits to different nodes all land,
// where a whole-file PUT would have rejected all but one.
func TestConcurrentGraphOpsAllLand(t *testing.T) {
	_, st := opsTestServer(t)

	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(offset float64) {
			defer wait.Done()
			id := "alpha"
			if int(offset)%2 == 0 {
				id = "beta"
			}
			x, y := offset, offset*2
			if _, _, err := st.ApplyGraphOps([]store.GraphOp{
				{Kind: "move", NodeID: id, X: &x, Y: &y},
			}); err != nil {
				t.Errorf("move %s: %v", id, err)
			}
		}(float64(index))
	}
	wait.Wait()

	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := graph.UI.Positions["alpha"]; !ok {
		t.Fatal("alpha has no position after concurrent moves")
	}
	if _, ok := graph.UI.Positions["beta"]; !ok {
		t.Fatal("beta has no position after concurrent moves")
	}
}

func TestGraphOpsRejectUnknownKindAndNode(t *testing.T) {
	_, st := opsTestServer(t)

	if _, _, err := st.ApplyGraphOps([]store.GraphOp{{Kind: "explode"}}); err == nil {
		t.Fatal("an unknown operation was accepted")
	}
	x, y := 1.0, 1.0
	if _, _, err := st.ApplyGraphOps([]store.GraphOp{
		{Kind: "move", NodeID: "ghost", X: &x, Y: &y},
	}); err == nil {
		t.Fatal("a move of a node that does not exist was accepted")
	}
	// A failed batch must leave nothing behind.
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.UI != nil && len(graph.UI.Positions) != 0 {
		t.Fatalf("positions = %+v, want the failed batch discarded", graph.UI.Positions)
	}
}
