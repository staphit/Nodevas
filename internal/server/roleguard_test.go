package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

type roleRouteFixture struct {
	Routes []struct {
		Method  string `json:"method"`
		Pattern string `json:"pattern"`
		Cap     string `json:"cap"`
	} `json:"routes"`
}

func readRoleRoutes(t *testing.T) roleRouteFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "role_routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture roleRouteFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestRoleGuardCoversEveryMCPRoute(t *testing.T) {
	for route := range mcpRoutes(t) {
		method, path, _ := strings.Cut(route, " ")
		if _, ok := roleRouteCapability(method, path); !ok {
			t.Errorf("the MCP sidecar calls %s, which no role may call; add it to the role route table", route)
		}
	}
	for _, route := range runnerProxyRoutes {
		path := strings.ReplaceAll(route.pattern, "*", "x")
		if _, ok := roleRouteCapability(route.method, path); !ok {
			t.Errorf("proxy route %s %s has no role capability", route.method, route.pattern)
		}
	}
	// The table matches the shared fixture.
	for _, route := range readRoleRoutes(t).Routes {
		path := strings.ReplaceAll(route.Pattern, "*", "x")
		if got, ok := roleRouteCapability(route.Method, path); !ok || got != route.Cap {
			t.Errorf("%s %s: capability %q (ok=%v), want %q", route.Method, route.Pattern, got, ok, route.Cap)
		}
	}
}

func TestRoleGuardDefaultDenies(t *testing.T) {
	if capability, ok := roleRouteCapability(http.MethodPost, "/api/nodes/x/claim"); !ok || capability != string(engine.CapClaim) {
		t.Fatalf("claim = %q %v", capability, ok)
	}
	for _, route := range [][2]string{
		{http.MethodDelete, "/api/nodes/x"},
		{http.MethodGet, "/api/runs"},
		{http.MethodPost, "/api/runs"},
		{http.MethodPut, "/api/graph"},
		{http.MethodPost, "/api/nodes/x/history/restore"},
		{http.MethodGet, "/api/nodes/x/acceptance"},
		{http.MethodPost, "/api/agents/messages/x/approve"},
		{http.MethodPost, "/api/nodes/x/../../runs"},
		{http.MethodGet, "/api/settings"},
	} {
		if capability, ok := roleRouteCapability(route[0], route[1]); ok {
			t.Errorf("%s %s is open to roles (capability %q)", route[0], route[1], capability)
		}
	}
}

func TestAcceptanceEndpointAllowedThroughProxy(t *testing.T) {
	if !runnerProxyAllows(http.MethodPost, "/api/nodes/x/acceptance") {
		t.Fatal("the runner proxy refuses POST /api/nodes/:id/acceptance (set_acceptance)")
	}
	if runnerProxyAllows(http.MethodDelete, "/api/nodes/x/acceptance") {
		t.Fatal("the runner proxy allows DELETE on acceptance")
	}
}

// roleRun records a queued run of bea's runner on the test node, in role.
func (f *runsFixture) roleRun(t *testing.T, role string) runs.Record {
	t.Helper()
	rec, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: f.projectName(), NodeID: runsTestNode, NodeTitle: "Fix the login bug",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Fix", CreatedBy: f.runner.Runner.OwnerID, CreatedByName: "bea", Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// projectName is the fixture project's name as runs record it (the
// active project, below the workspace), so the role guard finds the run's
// project: a run of a project that does not resolve is refused.
func (f *runsFixture) projectName() string {
	return runsapi.ProjectName(f.server.pm, f.server.pm.Store())
}

func (f *runsFixture) proxied(t *testing.T, runID, method, path, body string, extra map[string]string) (int, string) {
	t.Helper()
	header := map[string]string{"Authorization": "Bearer " + f.runner.Token}
	if runID != "" {
		header[runnerwire.RunIDHeader] = runID
	}
	for key, value := range extra {
		header[key] = value
	}
	response := f.do(t, nil, method, runnerwire.ProxyPathPrefix+path, body, header)
	return response.Code, response.Body.String()
}

func (f *runsFixture) guardedNode(t *testing.T) {
	t.Helper()
	if _, err := f.server.pm.Store().CreateNode(identity.Local, &engine.Node{
		ID: "guarded", Title: "Guarded", WriteAccess: engine.WriteAccessOrchestrator,
	}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestProxiedRunActsAsItsRunsRole(t *testing.T) {
	f := newRunsFixture(t)
	tester := f.roleRun(t, engine.RoleTester)
	if code, body := f.proxied(t, tester.ID, http.MethodGet, "nodes/"+runsTestNode, "", nil); code != http.StatusOK {
		t.Fatalf("tester reading its node: %d %s", code, body)
	}
	code, body := f.proxied(t, tester.ID, http.MethodPost, "nodes/"+runsTestNode+"/claim", `{"owner":"run"}`, nil)
	if code != http.StatusForbidden || !strings.Contains(body, "ROLE_DENIED") || !strings.Contains(body, "tester") {
		t.Fatalf("tester claiming: %d %s", code, body)
	}
	executor := f.roleRun(t, engine.RoleExecutor)
	if code, body := f.proxied(t, executor.ID, http.MethodPost, "nodes/"+runsTestNode+"/claim", `{"owner":"run"}`, nil); code != http.StatusOK {
		t.Fatalf("executor claiming its node: %d %s", code, body)
	}
}

func TestProxiedRunCannotRaiseClassWithHeader(t *testing.T) {
	f := newRunsFixture(t)
	f.guardedNode(t)
	executor := f.roleRun(t, engine.RoleExecutor)
	code, body := f.proxied(t, executor.ID, http.MethodPost, "nodes/guarded/status", `{"status":"in_progress","by":"run"}`,
		map[string]string{"X-Nodevas-Agent-Role": "orchestrator"})
	if code != http.StatusForbidden {
		t.Fatalf("an executor run claiming orchestrator wrote an orchestrator node: %d %s", code, body)
	}
}

func TestProxiedLegacyRunCappedAtWorker(t *testing.T) {
	f := newRunsFixture(t)
	f.guardedNode(t)
	legacy := f.roleRun(t, "")
	code, body := f.proxied(t, legacy.ID, http.MethodPost, "nodes/guarded/status", `{"status":"in_progress","by":"run"}`,
		map[string]string{"X-Nodevas-Agent-Role": "orchestrator"})
	if code != http.StatusForbidden {
		t.Fatalf("a legacy run raised itself to orchestrator: %d %s", code, body)
	}
	// It still works as a worker, as before.
	if code, body := f.proxied(t, legacy.ID, http.MethodPost, "nodes/"+runsTestNode+"/claim", `{"owner":"run"}`, nil); code != http.StatusOK {
		t.Fatalf("legacy run claiming: %d %s", code, body)
	}
}

func TestProxyRefusesFinishedRun(t *testing.T) {
	f := newRunsFixture(t)
	run := f.roleRun(t, engine.RoleExecutor)
	if _, err := f.server.runs.Cancel(context.Background(), run.ID, "cancelled by bea"); err != nil {
		t.Fatal(err)
	}
	code, body := f.proxied(t, run.ID, http.MethodGet, "graph", "", nil)
	if code != http.StatusUnauthorized || !strings.Contains(body, "RUN_ENDED") {
		t.Fatalf("a finished run's leftovers: %d %s", code, body)
	}
}

// Owner tooling on a runner token, naming no run, keeps today's behavior.
func TestProxyWithoutRunIDKeepsHeaderClass(t *testing.T) {
	f := newRunsFixture(t)
	f.guardedNode(t)
	if code, body := f.proxied(t, "", http.MethodPost, "nodes/guarded/status", `{"status":"in_progress","by":"tool"}`,
		map[string]string{"X-Nodevas-Agent-Role": "orchestrator"}); code != http.StatusOK {
		t.Fatalf("orchestrator header without a run: %d %s", code, body)
	}
	if code, _ := f.proxied(t, "", http.MethodPost, "nodes/guarded/status", `{"status":"in_progress","by":"tool"}`, nil); code != http.StatusForbidden {
		t.Fatalf("no header without a run: %d, want the worker's 403", code)
	}
}

func loopbackRoleServer(t *testing.T) *runnerTestServer {
	t.Helper()
	pm := projectManagerForTest(t)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: "work", Title: "Work"}, ""); err != nil {
		t.Fatal(err)
	}
	server := serverForTest(t, pm, nil, nil)
	return &runnerTestServer{server: server, handler: server.Handler()}
}

func TestLoopbackRoleHeader(t *testing.T) {
	s := loopbackRoleServer(t)
	tester := map[string]string{"X-Nodevas-Agent-Role": "worker", "X-Nodevas-Role": engine.RoleTester}
	if code := s.do(t, nil, http.MethodGet, "/api/nodes/work", "", tester).Code; code != http.StatusOK {
		t.Fatalf("tester reading: %d", code)
	}
	response := s.do(t, nil, http.MethodPost, "/api/nodes/work/claim", `{"owner":"me"}`, tester)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ROLE_DENIED") {
		t.Fatalf("tester claiming over loopback: %d %s", response.Code, response.Body)
	}
	executor := map[string]string{"X-Nodevas-Role": engine.RoleExecutor}
	if response := s.do(t, nil, http.MethodPost, "/api/nodes/work/claim", `{"owner":"me"}`, executor); response.Code != http.StatusOK {
		t.Fatalf("executor claiming over loopback: %d %s", response.Code, response.Body)
	}
}

func TestUnknownRoleRefused(t *testing.T) {
	s := loopbackRoleServer(t)
	response := s.do(t, nil, http.MethodGet, "/api/graph", "", map[string]string{"X-Nodevas-Role": "custom-role-ghost"})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "UNKNOWN_ROLE") {
		t.Fatalf("a role the project does not define: %d %s", response.Code, response.Body)
	}
	if code := s.do(t, nil, http.MethodGet, "/api/graph", "", map[string]string{"X-Nodevas-Role": "Boss!"}).Code; code != http.StatusUnauthorized {
		t.Fatalf("a malformed role: %d, want 401", code)
	}
}

// A browser session is a person: the role header means nothing to it.
func TestSessionAuthIgnoresRoleHeader(t *testing.T) {
	f := newRunsFixture(t)
	response := f.do(t, &f.bea, http.MethodPost, "/api/nodes/"+runsTestNode+"/claim", `{"owner":"bea"}`,
		map[string]string{"X-Nodevas-Role": engine.RoleReviewer})
	if response.Code != http.StatusOK {
		t.Fatalf("a person with a role header: %d %s", response.Code, response.Body)
	}
}
