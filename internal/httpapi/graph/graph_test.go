package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/store"
)

// seedGraph puts a two-node graph on disk and returns the revision a caller
// must send to write over it.
func seedGraph(t *testing.T, st *store.Store, nodes ...*engine.Node) string {
	t.Helper()
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	rev, err = st.SaveGraph(identity.Local, &engine.Graph{Version: 1, Nodes: nodes}, rev)
	if err != nil {
		t.Fatalf("seed graph: %v", err)
	}
	return rev
}

func TestGraphReadOnAFreshProjectReturnsAnEmptyGraphWithItsRev(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	response := httptest.NewRecorder()
	api.getGraph(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Graph    *engine.Graph            `json:"graph"`
		Rev      string                   `json:"rev"`
		Statuses map[string]engine.Status `json:"statuses"`
		Issues   []engine.Issue           `json:"issues"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Graph == nil || payload.Graph.Version < 1 {
		t.Fatalf("graph = %+v, want a version 1 document", payload.Graph)
	}
	if len(payload.Graph.Nodes) != 0 {
		t.Fatalf("fresh project already has nodes: %+v", payload.Graph.Nodes)
	}
	if payload.Rev == "" {
		t.Fatal("rev is empty; the client has nothing to lock its next save against")
	}
	for _, issue := range payload.Issues {
		if issue.Severity == "error" {
			t.Fatalf("fresh project reports an error: %+v", issue)
		}
	}
}

func TestGraphWriteWithAStaleBaseRevIsRejectedAsAConflict(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t, st, &engine.Node{ID: "alpha", Title: "Alpha"})

	body := `{"graph":{"version":1,"nodes":[{"id":"alpha","title":"Overwritten"}]},` +
		`"baseRev":"000000000000"}`
	request := httptest.NewRequest(http.MethodPut, "/api/graph", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.putGraph(ginContext(response, request))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body)
	}
	var payload struct {
		Error   string `json:"error"`
		DiskRev string `json:"diskRev"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "conflict" || payload.DiskRev == "" {
		t.Fatalf("conflict payload = %+v; the client cannot recover without the disk revision", payload)
	}

	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if node := graph.NodeByID("alpha"); node == nil || node.Title != "Alpha" {
		t.Fatalf("the rejected save still landed: %+v", node)
	}
}

func TestGraphWriteWithTheCurrentRevSaves(t *testing.T) {
	api, st := graphTestAPI(t)
	rev := seedGraph(t, st, &engine.Node{ID: "alpha", Title: "Alpha"})

	body := `{"graph":{"version":1,"nodes":[{"id":"alpha","title":"Renamed"}]},` +
		`"baseRev":"` + rev + `"}`
	request := httptest.NewRequest(http.MethodPut, "/api/graph", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.putGraph(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		OK  bool   `json:"ok"`
		Rev string `json:"rev"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Rev == "" || payload.Rev == rev {
		t.Fatalf("save payload = %+v, want a new revision", payload)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if node := graph.NodeByID("alpha"); node == nil || node.Title != "Renamed" {
		t.Fatalf("saved node = %+v", node)
	}
}

// The ops route carries no base revision at all: two people moving different
// nodes must both succeed, so the handler must never demand one.
func TestGraphOpsApplyWithoutABaseRev(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t,
		st,
		&engine.Node{ID: "alpha", Title: "Alpha"},
		&engine.Node{ID: "beta", Title: "Beta"},
	)

	body := `{"ops":[
		{"kind":"move","nodeId":"alpha","x":10,"y":20},
		{"kind":"add-edge","from":"alpha","to":"beta"}
	]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.postGraphOps(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		OK    bool          `json:"ok"`
		Graph *engine.Graph `json:"graph"`
		Rev   string        `json:"rev"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Rev == "" || payload.Graph == nil {
		t.Fatalf("ops payload = %+v", payload)
	}

	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if position := graph.UI.Positions["alpha"]; position.X != 10 || position.Y != 20 {
		t.Fatalf("position = %+v, want {10 20}", position)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].From != "alpha" || graph.Edges[0].To != "beta" {
		t.Fatalf("edges = %+v", graph.Edges)
	}
	if requires := graph.NodeByID("beta").Requires; requires != "alpha" {
		t.Fatalf("beta requires = %q, want alpha", requires)
	}
}

func TestDependencyGraphOpsRequireCanonicalReload(t *testing.T) {
	for _, kind := range []string{"add-edge", "remove-edge", "set-edge-style"} {
		if !dependencyOpsRequireReload([]store.GraphOp{{Kind: kind}}) {
			t.Errorf("%s did not require graph reload", kind)
		}
	}
	if dependencyOpsRequireReload([]store.GraphOp{{Kind: "move"}, {Kind: "node-metadata"}}) {
		t.Fatal("layout/metadata ops unnecessarily required graph reload")
	}
}

func TestGraphOpsRejectAnEmptyBatch(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(`{"ops":[]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.postGraphOps(ginContext(response, request))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body)
	}
}

func runValidate(t *testing.T, api *API) []engine.Issue {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/validate", nil)
	response := httptest.NewRecorder()
	api.getValidate(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Issues []engine.Issue `json:"issues"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Issues
}

func TestValidateReportsNoErrorsForAWellFormedGraph(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t,
		st,
		&engine.Node{ID: "alpha", Title: "Alpha"},
		&engine.Node{ID: "beta", Title: "Beta", Requires: "alpha"},
	)

	for _, issue := range runValidate(t, api) {
		if issue.Severity == "error" {
			t.Fatalf("valid graph reported %+v", issue)
		}
	}
}

func TestValidateReportsADependencyCycle(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t,
		st,
		&engine.Node{ID: "alpha", Title: "Alpha", Requires: "beta"},
		&engine.Node{ID: "beta", Title: "Beta", Requires: "alpha"},
	)

	found := false
	for _, issue := range runValidate(t, api) {
		if issue.Severity == "error" && strings.Contains(issue.Msg, "dependency cycle") {
			found = true
			if issue.Field != "requires" {
				t.Fatalf("cycle issue points at field %q", issue.Field)
			}
		}
	}
	if !found {
		t.Fatal("a graph where alpha and beta require each other validated clean")
	}
}
