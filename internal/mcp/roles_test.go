package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// builtinRoleTools reads each built-in role's tools from the shared fixture.
func builtinRoleTools(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "agent_roles_builtin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Roles []struct {
			ID    string   `json:"id"`
			Tools []string `json:"tools"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, role := range fixture.Roles {
		tools := slices.Clone(role.Tools)
		slices.Sort(tools)
		out[role.ID] = tools
	}
	return out
}

func roleOptionsSession(t *testing.T, opts Options) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	if opts.Actor == "" {
		opts.Actor = "run:claude"
	}
	server, err := NewServer(ctx, opts)
	if err != nil {
		t.Fatalf("NewServer(%+v): %v", opts, err)
	}
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestRoleToolsetListsExactlyRoleTools(t *testing.T) {
	url, _ := liveServer(t)
	for role, want := range builtinRoleTools(t) {
		// RAG tools exist only with --chroma-url, which this server lacks.
		want = slices.DeleteFunc(want, func(name string) bool { return name == "search_documents" || name == "index_documents" })
		session := roleOptionsSession(t, Options{Server: url, Role: role, AgentRole: "worker"})
		names, _ := listedTools(t, session)
		if !slices.Equal(names, want) {
			t.Errorf("role %s lists %q, want %q", role, names, want)
		}
		if got := session.InitializeResult().Instructions; !strings.Contains(strings.ToLower(got), role) {
			t.Errorf("role %s instructions do not name the role: %q", role, got)
		}
	}
	// --tools wins over the role's defaults.
	session := roleOptionsSession(t, Options{Server: url, Role: "executor", Tools: []string{"get_node", "claim_task", "update_node_body"}})
	if names, _ := listedTools(t, session); !slices.Equal(names, []string{"claim_task", "get_node", "update_node_body"}) {
		t.Fatalf("--tools lists %q", names)
	}
}

func TestRoleToolDefinitionsByteIdentical(t *testing.T) {
	url, _ := liveServer(t)
	_, full := listedTools(t, toolsetSession(t, url, ToolsetFull))
	if _, ok := full["set_acceptance"]; !ok {
		t.Fatal("the full toolset has no set_acceptance")
	}
	for role := range builtinRoleTools(t) {
		_, defs := listedTools(t, roleOptionsSession(t, Options{Server: url, Role: role}))
		for name, def := range defs {
			if full[name] != def {
				t.Errorf("%s/%s differs from the full toolset's definition", role, name)
			}
		}
	}
}

func TestRoleRefusesToolOutsideRole(t *testing.T) {
	url, _ := liveServer(t)
	tester := roleOptionsSession(t, Options{Server: url, Role: "tester"})
	for _, name := range []string{"claim_task", "set_node_status", "update_node_body", "set_acceptance", "create_subgraph"} {
		result, err := tester.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if text := errorText(result); !result.IsError || !strings.Contains(text, "role tester serves only") {
			t.Errorf("tester calling %s: isError %v, %q", name, result.IsError, text)
		}
	}
	var node getNodeOutput
	if result := call(t, tester, "get_node", map[string]any{"id": "design"}, &node); result.IsError || node.ID != "design" {
		t.Fatalf("tester reading: %s", errorText(result))
	}
}

func TestToolsFlagRejectsUnknownTool(t *testing.T) {
	for _, opts := range []Options{
		{Server: "http://127.0.0.1:1", Tools: []string{"get_node", "exec_shell"}},
		{Server: "http://127.0.0.1:1", Role: "boss"},
	} {
		if _, err := NewServer(context.Background(), opts); err == nil {
			t.Errorf("NewServer(%+v) started", opts)
		}
	}
	_, err := NewServer(context.Background(), Options{Server: "http://127.0.0.1:1", Tools: []string{"exec_shell"}})
	if err == nil || !strings.Contains(err.Error(), "exec_shell") {
		t.Fatalf("error does not name the unknown tool: %v", err)
	}
}

func TestRoleHeaderOnEveryRequest(t *testing.T) {
	var seen []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(auth.RoleHeaderName))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()
	client, err := NewClient(ClientOptions{Server: stub.URL, AgentRole: "worker", Role: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = client.get(ctx, "/api/graph", nil, &struct{}{})
	_ = client.post(ctx, "/api/graph/ops", nil, map[string]any{}, nil)
	if len(seen) != 2 || seen[0] != "tester" || seen[1] != "tester" {
		t.Fatalf("role headers = %q", seen)
	}
}

func TestNoRoleNoRoleHeader(t *testing.T) {
	var present bool
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header[http.CanonicalHeaderKey(auth.RoleHeaderName)]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()
	client, err := NewClient(ClientOptions{Server: stub.URL, AgentRole: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	_ = client.get(context.Background(), "/api/graph", nil, &struct{}{})
	if present {
		t.Fatal("a legacy session sent a role header")
	}
}

func TestSetAcceptanceTool(t *testing.T) {
	url, pm := liveServer(t)
	session := mcpSession(t, url, "")
	result := call(t, session, "set_acceptance", map[string]any{
		"id": "build", "items": []map[string]any{{"text": "GET /x returns 200"}, {"text": "unknown ids 404"}},
	}, nil)
	if result.IsError {
		t.Fatalf("set_acceptance: %s", errorText(result))
	}
	g, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	acc := g.NodeByID("build").Acceptance
	if acc == nil || len(acc.Items) != 2 || acc.Items[0].ID != "a1" {
		t.Fatalf("stored acceptance = %+v", acc)
	}
	// create_subgraph carries acceptance too.
	result = call(t, session, "create_subgraph", map[string]any{"nodes": []map[string]any{
		{"key": "t1", "title": "Write the API", "kind": "task", "acceptance": []string{"returns JSON"}},
	}}, nil)
	if result.IsError {
		t.Fatalf("create_subgraph with acceptance: %s", errorText(result))
	}
	g, _, _ = pm.Store().LoadGraph()
	found := false
	for _, n := range g.Nodes {
		if n.Title == "Write the API" && n.Acceptance != nil && len(n.Acceptance.Items) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("create_subgraph dropped the acceptance")
	}
}

func TestSetAcceptanceRefusedWhenLocked(t *testing.T) {
	url, pm := liveServer(t)
	if _, err := pm.Store().SetAcceptance(identity.Local, "build", nil, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := pm.Store().LockAcceptance("build"); err != nil {
		t.Fatal(err)
	}
	planner := roleOptionsSession(t, Options{Server: url, Role: "planner", AgentRole: "orchestrator"})
	result := call(t, planner, "set_acceptance", map[string]any{"id": "build", "items": []map[string]any{{"text": "weaker"}}}, nil)
	if text := errorText(result); !result.IsError || !strings.Contains(text, "ACCEPTANCE_LOCKED") {
		t.Fatalf("planner on locked acceptance: isError %v %q", result.IsError, text)
	}
}

// Each role's tools/list payload, measured. A role pays only for the tools it
// may use: the executor less than today's run toolset, and so on.
func TestRolePayloadBudgets(t *testing.T) {
	url, _ := liveServer(t)
	size := func(session *sdk.ClientSession) int {
		list, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(list)
		return len(raw)
	}
	run := size(toolsetSession(t, url, ToolsetRun))
	// Every role carries approve_tool (about 400 bytes), the target of the
	// runner's permission relay: the reviewer's ceiling rose from 3000.
	ceilings := map[string]int{
		"planner": 13000, "qa": 11000, "researcher": 11000,
		"executor": 7000, "tester": 4500, "reviewer": 3500,
	}
	for role, ceiling := range ceilings {
		got := size(roleOptionsSession(t, Options{Server: url, Role: role}))
		t.Logf("%s: %d bytes (run toolset %d)", role, got, run)
		if got > ceiling {
			t.Errorf("role %s tools/list = %d bytes, over its %d budget", role, got, ceiling)
		}
	}
	if exec := size(roleOptionsSession(t, Options{Server: url, Role: "executor"})); exec >= run {
		t.Errorf("executor (%d bytes) is not smaller than the run toolset (%d)", exec, run)
	}
}
