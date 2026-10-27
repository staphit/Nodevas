package toolbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
		"claim_task":        KindWrite,
		"release_task":      KindWrite,
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
