package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/store"
)

func typedBool(v bool) *bool { return &v }

// typedRunsFixture adds one node of each type the type gate cares about, and
// a custom type agents are dispatched to.
func typedRunsFixture(t *testing.T) *runsFixture {
	t.Helper()
	f := newRunsFixture(t)
	st := f.server.pm.Store()
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.NodeTypes = []engine.NodeTypeDef{{ID: "custom-type-1", Label: "Chapter", AgentMode: engine.AgentDispatch,
		Completion: engine.CompletionAnyone, CountsInProgress: typedBool(true)}}
	if _, err := st.SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	for _, n := range []*engine.Node{
		{ID: "review", Title: "Review", Kind: "gate"},
		{ID: "goal", Title: "Goal", Kind: "goal"},
		{ID: "scene", Title: "Scene", Kind: "scene"},
		{ID: "chapter", Title: "Chapter", Kind: "custom-type-1"},
		{ID: "lore", Title: "Lore", Kind: "note"},
		{ID: "task-a", Title: "Task A"},
	} {
		if _, err := st.CreateNode(identity.Local, n, "Do "+n.Title+"."); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestRunsRefuseNodeTypesAgentsMayNotWork(t *testing.T) {
	f := typedRunsFixture(t)
	hello := runsHello(8)
	hello.Hello.Capabilities = append(hello.Hello.Capabilities, runnerwire.CapPlan)
	f.connect(t, hello)
	body := func(node, kind string) string {
		raw, _ := json.Marshal(runnerwire.CreateRunRequest{NodeID: node, RunnerID: f.runner.Runner.ID,
			Agent: "claude", RepoID: "repo-1", Kind: kind})
		return string(raw)
	}
	for _, tc := range []struct {
		name, node, kind string
		refused          bool
	}{
		{"review implement", "review", "", true},
		{"goal implement", "goal", "", true},
		{"note plan", "lore", runnerwire.JobPlan, true},
		{"goal plan", "goal", runnerwire.JobPlan, false},
		{"scene implement", "scene", "", false},
		{"custom dispatch implement", "chapter", "", false},
	} {
		response := f.do(t, &f.bea, http.MethodPost, "/api/runs", body(tc.node, tc.kind), nil)
		forbids := response.Code == http.StatusConflict && strings.Contains(response.Body.String(), store.CodeNodeTypeForbidsAgent)
		if forbids != tc.refused {
			t.Errorf("%s: %d %s", tc.name, response.Code, response.Body)
		}
		if !tc.refused && response.Code != http.StatusCreated {
			t.Errorf("%s: %d %s", tc.name, response.Code, response.Body)
		}
	}
}

func TestAutopilotRefusesAGoalOfANonAgentType(t *testing.T) {
	f := typedRunsFixture(t)
	st := f.server.pm.Store()
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{
		{Kind: "add-edge", From: "task-a", To: "review"},
		{Kind: "add-edge", From: "scene", To: "lore"},
	}); err != nil {
		t.Fatal(err)
	}
	f.connect(t, runsHello(2))
	for _, goal := range []string{"review", "lore"} {
		response := f.do(t, &f.bea, http.MethodPost, "/api/autopilot", `{"goalNodeId":"`+goal+`","policy":{}}`, nil)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), store.CodeNodeTypeForbidsAgent) {
			t.Fatalf("%s goal: %d %s", goal, response.Code, response.Body)
		}
	}
}

func TestOutlineCarriesLayoutAndNodeTypes(t *testing.T) {
	server, pm := readyQueueServer(t)
	g, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.Type = "story"
	if _, err := pm.Store().SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/graph?view=outline", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body struct {
		Layout    string            `json:"layout"`
		NodeTypes []engine.NodeType `json:"nodeTypes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Layout != "story" || len(body.NodeTypes) != len(engine.BuiltinNodeTypes()) || body.NodeTypes[4].ID != "gate" {
		t.Fatalf("outline = %+v", body)
	}
}

func TestAnAgentIsOnlyOfferedWorkAgentsDo(t *testing.T) {
	server, pm := readyQueueServer(t)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: "pick", Title: "Pick", Kind: "choice"}, ""); err != nil {
		t.Fatal(err)
	}
	person := getReadyQueue(t, server, "")
	if !strings.Contains(strings.Join(taskIDs(person.Tasks), ","), "pick") {
		t.Fatalf("a person's queue lacks the decision: %v", taskIDs(person.Tasks))
	}
	request := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var agent readyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &agent); err != nil {
		t.Fatalf("decode: %v (%s)", err, response.Body)
	}
	for _, task := range agent.Tasks {
		if task.ID == "pick" || task.AgentMode != "dispatch" {
			t.Fatalf("agent offered %s (%s)", task.ID, task.AgentMode)
		}
	}
	if len(agent.Tasks) == 0 {
		t.Fatal("the agent was offered nothing")
	}
}

func TestStatusAndCreateRefusalsCarryTheirCodes(t *testing.T) {
	server, pm := readyQueueServer(t)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: "lore", Title: "Lore", Kind: "note"}, ""); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := post("/api/nodes/lore/status", `{"status":"done"}`); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), store.CodeNodeTypeNoStatus) {
		t.Fatalf("status on a note: %d %s", response.Code, response.Body)
	}
	if response := post("/api/nodes", `{"title":"Ghost","kind":"custom-type-9"}`); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), store.CodeUnknownNodeType) {
		t.Fatalf("create with an unknown type: %d %s", response.Code, response.Body)
	}
}

// TestAnAgentCannotSettleOrRetypeAPeopleOnlyNode drives the HTTP routes an MCP
// sidecar uses, with the agent-role header it sends: a review stays a
// person's to finish, and retyping it is no way round that.
func TestAnAgentCannotSettleOrRetypeAPeopleOnlyNode(t *testing.T) {
	server, pm := readyQueueServer(t)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: "review", Title: "Review", Kind: "gate"}, ""); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string, agent bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if agent {
			request.Header.Set(auth.AgentRoleHeaderName, string(identity.AgentWorker))
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := post("/api/nodes/review/status", `{"status":"done"}`, true); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), store.CodeNodeTypePeopleOnly) {
		t.Fatalf("agent completing a review: %d %s", response.Code, response.Body)
	}
	if response := post("/api/nodes/review/status", `{"status":"in_progress"}`, true); response.Code != http.StatusOK {
		t.Fatalf("agent moving a review along without settling it: %d %s", response.Code, response.Body)
	}
	if response := post("/api/graph/ops", `{"ops":[{"kind":"node-metadata","nodeId":"review","nodeKind":"task"}]}`, true); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), store.CodeNodeTypePeopleOnly) {
		t.Fatalf("agent retyping a review: %d %s", response.Code, response.Body)
	}
	if response := post("/api/nodes/review/status", `{"status":"done"}`, false); response.Code != http.StatusOK {
		t.Fatalf("a person completing a review: %d %s", response.Code, response.Body)
	}
}
