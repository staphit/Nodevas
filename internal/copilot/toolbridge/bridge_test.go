package toolbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// seen is what the stub server observed about one request.
type seen struct {
	method, path, query, host, cookie, remote, body, requestURI, urlHost string
	agent                                                                identity.AgentClass
	marked                                                               bool
}

// stubServer answers the probe and records everything, standing in for the
// real router where only the transport's behaviour is under test.
type stubServer struct {
	mu   sync.Mutex
	seen []seen
}

func (s *stubServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	class, marked := auth.InProcessAgent(r.Context())
	s.mu.Lock()
	s.seen = append(s.seen, seen{
		method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, host: r.Host,
		cookie: r.Header.Get("Cookie"), remote: r.RemoteAddr, body: string(body),
		requestURI: r.RequestURI, urlHost: r.URL.Host, agent: class, marked: marked,
	})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/auth/status" {
		_, _ = io.WriteString(w, `{"mode":"local"}`)
		return
	}
	w.WriteHeader(http.StatusTeapot)
	_, _ = io.WriteString(w, `{"error":"stub"}`)
}

func (s *stubServer) last() seen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[len(s.seen)-1]
}

func TestTransportServesInProcessAsThePerson(t *testing.T) {
	stub := &stubServer{}
	transport := &inProcessTransport{
		handler: stub, host: "board.example.test", cookie: "nodevas_session=abc",
		remoteAddr: "203.0.113.7", role: identity.AgentWorker,
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequest(http.MethodPost, inProcessBase+"/api/nodes?project=p",
		strings.NewReader(`{"title":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	// Whatever the client put here must not survive: the person's cookie is
	// the only identity the request may carry.
	request.Header.Set("Cookie", "nodevas_session=someone-else")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusTeapot || !strings.Contains(string(answer), "stub") {
		t.Fatalf("response = %d %s", response.StatusCode, answer)
	}

	got := stub.last()
	want := seen{
		method: http.MethodPost, path: "/api/nodes", query: "project=p",
		host: "board.example.test", cookie: "nodevas_session=abc", remote: "203.0.113.7",
		body: `{"title":"x"}`, requestURI: "/api/nodes?project=p", urlHost: "",
		agent: identity.AgentWorker, marked: true,
	}
	if got != want {
		t.Fatalf("server saw %+v\nwant          %+v", got, want)
	}
	if request.Host != "nodevas.in-process.invalid" || request.Header.Get("Cookie") != "nodevas_session=someone-else" {
		t.Fatal("the transport changed the caller's request")
	}
}

func TestOpenListsToolsWithPermissionKinds(t *testing.T) {
	stub := &stubServer{}
	bridge, err := Open(context.Background(), Options{Handler: stub, Project: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	probe := stub.last()
	if probe.path != "/api/auth/status" || !probe.marked || probe.agent != identity.AgentOrchestrator ||
		probe.host != "localhost" || probe.remote != "127.0.0.1:0" {
		t.Fatalf("probe = %+v, want an orchestrator-marked request with the defaults", probe)
	}

	kinds := map[string]Kind{}
	for _, tool := range bridge.Tools() {
		kinds[tool.Name] = tool.Kind
		var schema map[string]any
		if err := json.Unmarshal(tool.Schema, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("%s schema = %s (%v), want a JSON object schema", tool.Name, tool.Schema, err)
		}
		if tool.Description == "" {
			t.Fatalf("%s has no description", tool.Name)
		}
	}
	for name, want := range map[string]Kind{
		"list_projects":     KindRead,
		"get_ready_tasks":   KindRead,
		"get_node":          KindRead,
		"get_graph_outline": KindRead,
		"search_nodes":      KindRead,
		"validate_graph":    KindRead,
		"create_node":       KindWrite,
		"create_subgraph":   KindWrite,
		"link_nodes":        KindWrite,
		"update_node_meta":  KindWrite,
		"set_node_status":   KindWrite,
		"update_node_body":  KindRisky,
	} {
		if got, ok := kinds[name]; !ok || got != want {
			t.Errorf("%s kind = %q (listed %v), want %q", name, got, ok, want)
		}
	}
	// No ChromaURL, no retrieval tools.
	if _, ok := kinds["search_documents"]; ok {
		t.Error("search_documents listed without a ChromaURL")
	}
}

func TestCallRefusesWhatTheModelGotWrongWithoutBreaking(t *testing.T) {
	bridge, err := Open(context.Background(), Options{Handler: &stubServer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	result, err := bridge.Call(context.Background(), "delete_everything", nil)
	if err != nil || !result.IsError || !strings.Contains(result.Output, "delete_everything") {
		t.Fatalf("unknown tool = %+v, %v; want an error result", result, err)
	}
	result, err = bridge.Call(context.Background(), "create_node", json.RawMessage(`{"title":`))
	if err != nil || !result.IsError {
		t.Fatalf("garbled input = %+v, %v; want an error result", result, err)
	}
	// A server-side failure (the stub answers 418) is the tool's error, for
	// the model to read.
	result, err = bridge.Call(context.Background(), "list_projects", json.RawMessage(`{}`))
	if err != nil || !result.IsError || result.Output == "" {
		t.Fatalf("failing tool = %+v, %v; want an error result with text", result, err)
	}
	if err := bridge.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := bridge.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpenValidatesOptions(t *testing.T) {
	if _, err := Open(context.Background(), Options{}); err == nil {
		t.Fatal("Open without a handler succeeded")
	}
	if _, err := Open(context.Background(), Options{Handler: &stubServer{}, AgentRole: "supervisor"}); err == nil {
		t.Fatal("Open with an unknown role succeeded")
	}
	stub := &stubServer{}
	bridge, err := Open(context.Background(), Options{Handler: stub, AgentRole: identity.AgentWorker})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	if got := stub.last().agent; got != identity.AgentWorker {
		t.Fatalf("worker bridge marked requests %q", got)
	}
}

func TestKindOf(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readOnly bool
		want     Kind
	}{
		{"get_node", true, KindRead},
		{"create_node", false, KindWrite},
		{"index_documents", false, KindWrite},
		{"update_node_body", false, KindRisky},
		{"some_future_tool", false, KindRisky},
		// The hint wins: a read-only tool never asks, whatever it is called.
		{"create_node", true, KindRead},
	} {
		if got := KindOf(tc.name, tc.readOnly); got != tc.want {
			t.Errorf("KindOf(%q, %v) = %q, want %q", tc.name, tc.readOnly, got, tc.want)
		}
	}
}

// A tool the copilot can only ever be refused is not offered: acceptance
// criteria are a person's or a planner/QA run's, a verdict is a review or QA
// run's, and the copilot has no folder to save an attachment into. Each
// would cost the model a call and the person a permission prompt for an
// error.
func TestOpenOffersNoToolTheCopilotCannotUse(t *testing.T) {
	bridge, err := Open(context.Background(), Options{Handler: &stubServer{}, Project: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	listed := map[string]bool{}
	for _, tool := range bridge.Tools() {
		listed[tool.Name] = true
	}
	if !listed["send_message"] || !listed["get_node"] {
		t.Fatalf("tools = %v, want the usable ones still listed", listed)
	}
	// The copilot has no folder to attach files from, so send_message's
	// files are always refused it.
	for _, tool := range bridge.Tools() {
		if tool.Name != "send_message" {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.Schema, &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["files"]; ok {
			t.Errorf("send_message offers the copilot files to attach: %s", tool.Schema)
		}
		if _, ok := schema.Properties["message"]; !ok || !slices.Contains(schema.Required, "message") {
			t.Errorf("send_message lost its other inputs: %s", tool.Schema)
		}
	}
	for _, name := range []string{"set_acceptance", "submit_verdict", "save_attachment"} {
		if listed[name] {
			t.Errorf("%s offered to the copilot, which is always refused it", name)
		}
		result, err := bridge.Call(context.Background(), name, json.RawMessage(`{}`))
		if err != nil || !result.IsError || !strings.Contains(result.Output, "unknown tool") {
			t.Errorf("calling %s = %+v, %v; want unknown tool", name, result, err)
		}
	}
}

// create_subgraph's per-node acceptance is always refused the role-less
// copilot, and the store checks every node before writing any, so one
// node carrying it failed the whole batch. It is neither offered nor sent.
func TestCreateSubgraphNeverCarriesAcceptance(t *testing.T) {
	stub := &stubServer{}
	bridge, err := Open(context.Background(), Options{Handler: stub, Project: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	var offered json.RawMessage
	for _, tool := range bridge.Tools() {
		if tool.Name == "create_subgraph" {
			offered = tool.Schema
		}
	}
	if offered == nil {
		t.Fatal("create_subgraph not offered")
	}
	var schema struct {
		Properties struct {
			Nodes struct {
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"items"`
			} `json:"nodes"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(offered, &schema); err != nil {
		t.Fatal(err)
	}
	node := schema.Properties.Nodes.Items
	if _, ok := node.Properties["acceptance"]; ok || slices.Contains(node.Required, "acceptance") {
		t.Errorf("create_subgraph offers per-node acceptance: %s", offered)
	}
	if _, ok := node.Properties["title"]; !ok {
		t.Errorf("create_subgraph lost its other node inputs: %s", offered)
	}

	_, err = bridge.Call(context.Background(), "create_subgraph", json.RawMessage(
		`{"nodes":[{"key":"a","title":"A","acceptance":["x"]},{"key":"b","title":"B","dependsOn":["a"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sent := stub.last()
	if sent.path != "/api/graph/subgraph" {
		t.Fatalf("last request = %s, want the subgraph POST", sent.path)
	}
	if strings.Contains(sent.body, "acceptance") || !strings.Contains(sent.body, `"dependsOn"`) {
		t.Errorf("subgraph body = %s, want acceptance dropped and the rest kept", sent.body)
	}
	// Only where dropping keeps the call's meaning: a message with files is
	// refused, not sent without them.
	if _, dropped := droppedArgs["send_message"]; dropped {
		t.Error("send_message's files are dropped from its arguments instead of refused")
	}
}

func TestWithoutInputsWalksNestedPaths(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{"type": "array"},
			"nodes": map[string]any{"type": "array", "items": map[string]any{
				"type":       "object",
				"required":   []any{"key", "acceptance"},
				"properties": map[string]any{"key": map[string]any{}, "acceptance": map[string]any{}},
			}},
		},
	}
	before, _ := json.Marshal(schema)
	got, _ := json.Marshal(withoutInputs(schema, []string{"files", "nodes.items.acceptance", "missing.items.x"}))
	if after, _ := json.Marshal(schema); string(after) != string(before) {
		t.Fatalf("withoutInputs changed its argument: %s", after)
	}
	want := `{"properties":{"nodes":{"items":{"properties":{"key":{}},"required":["key"],"type":"object"},"type":"array"}},"type":"object"}`
	if string(got) != want {
		t.Fatalf("withoutInputs = %s\nwant %s", got, want)
	}
}

func TestWithoutArgsDropsNestedArguments(t *testing.T) {
	in := json.RawMessage(`{"nodes":[{"key":"a","acceptance":["x"]},{"key":"b"},"odd"],"files":["f"],"keep":1}`)
	got := withoutArgs(in, []string{"files", "nodes.items.acceptance"})
	if string(got) != `{"keep":1,"nodes":[{"key":"a"},{"key":"b"},"odd"]}` {
		t.Fatalf("withoutArgs = %s", got)
	}
	if raw := withoutArgs(json.RawMessage(`not json`), []string{"files"}); string(raw) != `not json` {
		t.Fatalf("withoutArgs on invalid JSON = %s, want it untouched for the caller to refuse", raw)
	}
}

// Claiming, the inbox and the team roster are for agents working tasks; the
// copilot authors the board for a person, and each one offered costs a
// small model a tool to consider.
func TestOpenOffersNoNonAuthoringTools(t *testing.T) {
	bridge, err := Open(context.Background(), Options{Handler: &stubServer{}, Project: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for _, tool := range bridge.Tools() {
		if offTopic[tool.Name] {
			t.Errorf("%s offered to the copilot", tool.Name)
		}
	}
	for _, name := range []string{"claim_task", "release_task", "check_inbox", "list_team", "ask_user", "approve_tool"} {
		if !offTopic[name] {
			t.Errorf("%s is not in offTopic", name)
		}
	}
}
