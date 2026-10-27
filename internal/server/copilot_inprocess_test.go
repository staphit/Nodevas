// The copilot's tool bridge, driven against the real router: its MCP tools
// reach the handlers in-process, authenticate as the signed-in person, carry
// the orchestrator agent class, and need no CSRF token — while a request from
// the network gets none of that.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
)

func openBridge(t *testing.T, opts toolbridge.Options) toolbridge.Bridge {
	t.Helper()
	bridge, err := toolbridge.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("toolbridge.Open: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	return bridge
}

func callTool(t *testing.T, bridge toolbridge.Bridge, name, input string) toolbridge.Result {
	t.Helper()
	result, err := bridge.Call(context.Background(), name, json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s: bridge failed: %v", name, err)
	}
	return result
}

// cookieHeader is the Cookie header a browser holding these cookies sends,
// which is what the copilot endpoint hands the bridge.
func cookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func createdNodeID(t *testing.T, result toolbridge.Result) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("create_node failed: %s", result.Output)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(result.Output), &created); err != nil || created.ID == "" {
		t.Fatalf("create_node output = %q (%v), want {id}", result.Output, err)
	}
	return created.ID
}

func storedNode(t *testing.T, pm *project.ProjectManager, id string) *engine.Node {
	t.Helper()
	graph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	return graph.NodeByID(id)
}

func TestCopilotBridgeRunsMCPToolsInProcess(t *testing.T) {
	srv, pm := twoProjectServer(t)
	bridge := openBridge(t, toolbridge.Options{
		Handler: srv.Handler(),
		Project: "main",
		Actor:   "copilot:local",
	})

	kinds := map[string]toolbridge.Kind{}
	for _, tool := range bridge.Tools() {
		kinds[tool.Name] = tool.Kind
	}
	for name, want := range map[string]toolbridge.Kind{
		"get_graph_outline": toolbridge.KindRead,
		"list_projects":     toolbridge.KindRead,
		"create_node":       toolbridge.KindWrite,
		"update_node_body":  toolbridge.KindRisky,
	} {
		if kinds[name] != want {
			t.Fatalf("%s kind = %q, want %q (tools: %v)", name, kinds[name], want, kinds)
		}
	}

	projects := callTool(t, bridge, "list_projects", `{}`)
	if projects.IsError || !strings.Contains(projects.Output, "other") {
		t.Fatalf("list_projects = %+v, want both projects", projects)
	}
	outline := callTool(t, bridge, "get_graph_outline", `{}`)
	if outline.IsError {
		t.Fatalf("get_graph_outline failed: %s", outline.Output)
	}

	id := createdNodeID(t, callTool(t, bridge, "create_node", `{"title":"Drafted by voice"}`))
	node := storedNode(t, pm, id)
	if node == nil || node.Title != "Drafted by voice" {
		t.Fatalf("node %q after create_node = %+v", id, node)
	}
}

// On an accounts server the agent-role header means nothing, so these are the
// in-process marker's effects alone: the request is the person (their cookie,
// their audit entry), it is an orchestrator (human-only nodes refuse it), and
// it needs no CSRF token.
func TestCopilotBridgeActsAsThePersonAndAsAnAgent(t *testing.T) {
	srv, pm, inbox := accountServerForTest(t)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{
		ID: "sealed", Title: "Sealed", WriteAccess: engine.WriteAccessHumanOnly,
	}, ""); err != nil {
		t.Fatalf("create human-only node: %v", err)
	}
	cookies, _ := signIn(t, srv, inbox, testEmail)
	bridge := openBridge(t, toolbridge.Options{
		Handler:    srv.Handler(),
		Cookie:     cookieHeader(cookies),
		RemoteAddr: "198.51.100.9",
		Actor:      "copilot:ann",
	})

	id := createdNodeID(t, callTool(t, bridge, "create_node", `{"title":"No token needed"}`))
	if storedNode(t, pm, id) == nil {
		t.Fatalf("node %q missing after create_node", id)
	}
	entries, err := srv.audit.Query(context.Background(), audit.Filter{Project: pm.Store().Root()})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].ActorName != "ann" || entries[0].ClientIP != "198.51.100.9" {
		t.Fatalf("audit = %+v, want ann's write from her address", entries)
	}

	sealed := callTool(t, bridge, "set_node_status", `{"id":"sealed","status":"done"}`)
	if !sealed.IsError || !strings.Contains(sealed.Output, "human-only") {
		t.Fatalf("set_node_status on a human-only node = %+v, want the agent refused", sealed)
	}

	// The same cookie from the network, even claiming to be an agent, still
	// needs the token: only the in-process marker lifts it.
	request := withCookies(httptest.NewRequest(http.MethodPost, "/api/nodes",
		strings.NewReader(`{"id":"forged","title":"Forged"}`)), cookies)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(auth.AgentRoleHeaderName, "orchestrator")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("network write without a token: status = %d, body = %s", response.Code, response.Body)
	}
}

func TestCopilotBridgeWithoutASessionIsRefused(t *testing.T) {
	srv, pm, _ := accountServerForTest(t)
	bridge := openBridge(t, toolbridge.Options{Handler: srv.Handler()})

	result := callTool(t, bridge, "create_node", `{"title":"Anonymous"}`)
	if !result.IsError {
		t.Fatalf("create_node with no session = %+v, want refused", result)
	}
	graph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Title == "Anonymous" {
			t.Fatal("an unauthenticated bridge wrote a node")
		}
	}
}
