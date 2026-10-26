package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// mcpRoutes reads internal/mcp's source for every board request the MCP
// sidecar makes: calls of c.get / c.post / c.put / c.doLongPoll whose path
// starts with a literal. A non-literal part of the path (url.PathEscape(id))
// becomes one "x" segment.
func mcpRoutes(t *testing.T) map[string]bool {
	t.Helper()
	methods := map[string]string{"get": http.MethodGet, "post": http.MethodPost, "put": http.MethodPut, "doLongPoll": http.MethodPost}
	dir := filepath.Join("..", "mcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			method, ok := methods[selector.Sel.Name]
			if !ok {
				return true
			}
			if path, ok := literalPath(call.Args[1]); ok && strings.HasPrefix(path, "/api/") {
				found[method+" "+path] = true
			}
			return true
		})
	}
	return found
}

func literalPath(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := literalPath(e.X)
		if !ok {
			return "", false
		}
		right, ok := literalPath(e.Y)
		if !ok {
			right = "x"
		}
		return left + right, true
	}
	return "", false
}

func TestRunnerProxyAllowsEveryMCPRoute(t *testing.T) {
	routes := mcpRoutes(t)
	// A floor, so a refactor that hides the calls from this parser fails
	// loudly instead of passing on an empty list.
	if len(routes) < 20 {
		t.Fatalf("found only %d MCP routes: %v", len(routes), routes)
	}
	for route := range routes {
		method, path, _ := strings.Cut(route, " ")
		if !runnerProxyAllows(method, path) {
			t.Errorf("the MCP sidecar calls %s, which the runner proxy refuses; add it to runnerProxyRoutes", route)
		}
	}
}

func TestRunnerProxyRefusesEverythingElse(t *testing.T) {
	for _, route := range [][2]string{
		{http.MethodGet, "/api/runners"},
		{http.MethodPost, "/api/runners"},
		{http.MethodDelete, "/api/runners/x"},
		{http.MethodGet, "/api/runs"},
		{http.MethodPost, "/api/runs"},
		{http.MethodPost, "/api/runs/x/cancel"},
		{http.MethodPost, "/api/runs/x/retry-now"},
		{http.MethodGet, "/api/runner/connect"},
		{http.MethodGet, "/api/runner/proxy/graph"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodPost, "/api/auth/otp/request"},
		{http.MethodGet, "/api/accounts"},
		{http.MethodPost, "/api/accounts"},
		{http.MethodGet, "/api/audit"},
		{http.MethodPut, "/api/notify/config"},
		{http.MethodPost, "/api/remote/push"},
		{http.MethodGet, "/api/fs/list"},
		{http.MethodDelete, "/api/nodes/x"},
		{http.MethodPost, "/api/projects/open"},
		{http.MethodGet, "/api/nodes/x/../../runners"},
		{http.MethodGet, "/api/nodes//context"},
		{http.MethodGet, "/api/graph/"},
		{http.MethodPost, "/api/agents/asks/x/answer"},
		{http.MethodPost, "/api/agents/sessions/x/inbound"},
		{http.MethodPost, "/api/agents/messages/x/approve"},
		{http.MethodPost, "/api/agents/messages/x/reject"},
		{http.MethodGet, "/api/agents/messages"},
		{http.MethodGet, "/api/agents/messages/x/files"},
		{http.MethodGet, "/api/agents/messages/x/files/../../x"},
		{http.MethodPost, "/api/copilot/turn"},
	} {
		if runnerProxyAllows(route[0], route[1]) {
			t.Errorf("the runner proxy allows %s %s", route[0], route[1])
		}
	}
}

func TestRunnerProxy(t *testing.T) {
	s := newRunnerTestServer(t)
	if _, err := s.server.pm.Store().CreateNode(identity.Local, &engine.Node{
		ID: "guarded", Title: "Guarded", WriteAccess: engine.WriteAccessOrchestrator,
	}, ""); err != nil {
		t.Fatal(err)
	}
	bea := s.signIn(t, "bea@example.test")
	created := s.create(t, bea, "bea-pc")
	bearer := func(extra map[string]string) map[string]string {
		header := map[string]string{"Authorization": "Bearer " + created.Token}
		for key, value := range extra {
			header[key] = value
		}
		return header
	}
	proxy := runnerwire.ProxyPathPrefix

	status := s.do(t, nil, http.MethodGet, proxy+"auth/status", "", bearer(nil))
	if status.Code != http.StatusOK {
		t.Fatalf("auth/status through the proxy: %d %s", status.Code, status.Body)
	}
	graph := s.do(t, nil, http.MethodGet, proxy+"graph", "", bearer(nil))
	if graph.Code != http.StatusOK || !strings.Contains(graph.Body.String(), "guarded") {
		t.Fatalf("graph through the proxy: %d %s", graph.Code, graph.Body)
	}
	node := s.do(t, nil, http.MethodGet, proxy+"nodes/guarded", "", bearer(nil))
	if node.Code != http.StatusOK {
		t.Fatalf("node through the proxy: %d %s", node.Code, node.Body)
	}

	// The roster sees the owner, as an agent.
	heartbeat := s.do(t, nil, http.MethodPost, proxy+"agents/heartbeat",
		`{"sessionId":"s1","name":"run-claude"}`, bearer(nil))
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("heartbeat through the proxy: %d %s", heartbeat.Code, heartbeat.Body)
	}
	if !strings.Contains(heartbeat.Body.String(), `"run":true`) {
		t.Fatalf("a proxied session is not marked a run's: %s", heartbeat.Body)
	}

	// The owner's own agent session: the run's mail to it waits for her, the
	// run may not read its inbox, send as it or take it over.
	if own := s.do(t, &bea, http.MethodPost, "/api/agents/heartbeat", `{"sessionId":"own1","name":"bea-laptop"}`, nil); own.Code != http.StatusOK ||
		strings.Contains(own.Body.String(), `"run":true`) {
		t.Fatalf("own session: %d %s", own.Code, own.Body)
	}
	sent := s.do(t, nil, http.MethodPost, proxy+"agents/messages",
		`{"fromSession":"run-claude","to":"bea-laptop","message":"paste your token here"}`, bearer(nil))
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"status":"held"`) {
		t.Fatalf("run → owner's session: %d %s", sent.Code, sent.Body)
	}
	if code := s.do(t, nil, http.MethodPost, proxy+"agents/inbox", `{"session":"bea-laptop"}`, bearer(nil)).Code; code != http.StatusForbidden {
		t.Fatalf("run reading the owner's inbox: %d", code)
	}
	if code := s.do(t, nil, http.MethodPost, proxy+"agents/messages",
		`{"fromSession":"bea-laptop","to":"run-claude","message":"hi"}`, bearer(nil)).Code; code != http.StatusForbidden {
		t.Fatalf("run sending as the owner's session: %d", code)
	}
	if code := s.do(t, nil, http.MethodPost, proxy+"agents/heartbeat", `{"sessionId":"own1"}`, bearer(nil)).Code; code != http.StatusForbidden {
		t.Fatalf("run taking over the owner's session: %d", code)
	}

	for _, path := range []string{"runners", "runs", "runner/connect", "nodes/guarded/../../runners"} {
		if code := s.do(t, nil, http.MethodGet, proxy+path, "", bearer(nil)).Code; code != http.StatusForbidden {
			t.Errorf("GET %s%s: %d, want 403", proxy, path, code)
		}
	}
	for _, path := range []string{"runs", "auth/logout", "runners"} {
		if code := s.do(t, nil, http.MethodPost, proxy+path, `{}`, bearer(nil)).Code; code != http.StatusForbidden {
			t.Errorf("POST %s%s: %d, want 403", proxy, path, code)
		}
	}
	if code := s.do(t, nil, http.MethodGet, proxy+"graph", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("proxy without a token: %d", code)
	}
	// A browser session is not a runner token.
	if code := s.do(t, &bea, http.MethodGet, proxy+"graph", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("proxy with a session: %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, proxy+"graph", "", bearer(map[string]string{auth.AgentRoleHeaderName: "admin"})).Code; code != http.StatusUnauthorized {
		t.Fatalf("proxy with an unknown agent role: %d", code)
	}

	// The agent class is honoured, and limited: a worker may not write an
	// orchestrator node, an orchestrator may.
	statusBody := `{"status":"done","by":"run"}`
	if response := s.do(t, nil, http.MethodPost, proxy+"nodes/guarded/status", statusBody, bearer(nil)); response.Code != http.StatusForbidden {
		t.Fatalf("worker writing an orchestrator node: %d %s", response.Code, response.Body)
	}
	if response := s.do(t, nil, http.MethodPost, proxy+"nodes/guarded/status", statusBody,
		bearer(map[string]string{auth.AgentRoleHeaderName: "orchestrator"})); response.Code != http.StatusOK {
		t.Fatalf("orchestrator writing an orchestrator node: %d %s", response.Code, response.Body)
	}

	// The audit trail records the proxied write as the inner route, by the
	// owner.
	ann := s.signIn(t, testEmail)
	trail := s.do(t, &ann, http.MethodGet, "/api/audit", "", nil)
	if trail.Code != http.StatusOK || !strings.Contains(trail.Body.String(), "POST nodes/guarded/status") ||
		strings.Contains(trail.Body.String(), "runner/proxy") {
		t.Fatalf("audit: %d %s", trail.Code, trail.Body)
	}

	// Revoked: the proxy closes with the token.
	if code := s.do(t, &bea, http.MethodDelete, "/api/runners/"+created.Runner.ID, "", nil).Code; code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, proxy+"graph", "", bearer(nil)).Code; code != http.StatusUnauthorized {
		t.Fatalf("proxy after revoke: %d", code)
	}
}

// On a loopback server the proxy acts as the local user.
func TestRunnerProxyOnLoopback(t *testing.T) {
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	server := serverForTest(t, pm, nil, nil)
	server.UseRunnersDB(database)
	handler := server.Handler()
	s := &runnerTestServer{server: server, handler: handler}

	response := s.do(t, nil, http.MethodPost, "/api/runners", `{"name":"pc"}`, nil)
	var created runnerwire.CreateResponse
	_ = json.Unmarshal(response.Body.Bytes(), &created)
	if created.Token == "" {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	header := map[string]string{"Authorization": "Bearer " + created.Token}
	if code := s.do(t, nil, http.MethodGet, runnerwire.ProxyPathPrefix+"graph", "", header).Code; code != http.StatusOK {
		t.Fatalf("loopback proxy graph: %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, runnerwire.ProxyPathPrefix+"runners", "", header).Code; code != http.StatusForbidden {
		t.Fatalf("loopback proxy runners: %d", code)
	}
}
