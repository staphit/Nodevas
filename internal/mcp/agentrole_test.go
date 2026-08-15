package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/auth"
)

// roleSession is mcpSession with the process pinned to an agent role, the way
// `nodevas mcp --agent-role` pins it.
func roleSession(t *testing.T, url, role string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server, err := NewServer(ctx, Options{Server: url, Actor: "mcp:test", AgentRole: role})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// The role rides on every request, reads included: which class of agent is
// asking is part of who is asking, and enforcement lives on the server.
func TestEveryRequestDeclaresTheAgentRole(t *testing.T) {
	var seen []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(auth.AgentRoleHeaderName))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	client, err := NewClient(ClientOptions{Server: stub.URL, AgentRole: "orchestrator"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.get(ctx, "/api/graph", nil, &struct{}{}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := client.post(ctx, "/api/graph/ops", nil, map[string]any{}, nil); err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("saw %d requests, want 2", len(seen))
	}
	for index, role := range seen {
		if role != "orchestrator" {
			t.Fatalf("request %d carried role %q, want orchestrator", index, role)
		}
	}
}

// An empty role is a human session, and a human session is the absence of the
// header, not the header with an empty value: the server refuses values it
// does not know.
func TestNoRoleMeansNoHeader(t *testing.T) {
	var present bool
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header[http.CanonicalHeaderKey(auth.AgentRoleHeaderName)]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	client, err := NewClient(ClientOptions{Server: stub.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.get(context.Background(), "/api/graph", nil, &struct{}{}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if present {
		t.Fatal("a roleless client sent the agent-role header anyway")
	}
}

// A 403 means the server knows who is asking and said no. Presenting it as an
// authentication problem would send an agent chasing credentials it already
// has, so the two statuses map to different codes.
func TestAPermissionRefusalIsNotAnAuthenticationProblem(t *testing.T) {
	if got := codeFor(http.StatusForbidden); got != CodePermissionDenied {
		t.Fatalf("codeFor(403) = %q, want %q", got, CodePermissionDenied)
	}
	if got := codeFor(http.StatusUnauthorized); got != CodeAuthRequired {
		t.Fatalf("codeFor(401) = %q, want %q", got, CodeAuthRequired)
	}
}

// The server's refusal names the node and the rule; that prose is what the
// model reads, so it must arrive unchanged.
func TestAPermissionRefusalCarriesTheServersReason(t *testing.T) {
	const reason = `node "sealed" is human-only: agents may not modify it`
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"` + reason + `"}`))
	}))
	defer stub.Close()

	client, err := NewClient(ClientOptions{Server: stub.URL, AgentRole: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.post(context.Background(), "/api/graph/ops", nil, map[string]any{}, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error = %T, want *APIError", err)
	}
	if apiErr.Code != CodePermissionDenied {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodePermissionDenied)
	}
	if !strings.Contains(apiErr.Message, reason) {
		t.Fatalf("message = %q, want the server's reason to ride through", apiErr.Message)
	}
}

func TestWriteAccessRoundTripsThroughTheAuthoringTools(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var created createNodeOutput
	call(t, session, "create_node", map[string]any{
		"title": "Sealed", "writeAccess": "human-only",
	}, &created)
	if created.ID == "" {
		t.Fatalf("create_node returned no id: %+v", created)
	}
	var read getNodeOutput
	call(t, session, "get_node", map[string]any{"id": created.ID}, &read)
	if read.WriteAccess != "human-only" {
		t.Fatalf("writeAccess = %q, want human-only", read.WriteAccess)
	}

	var updated updateMetaOutput
	call(t, session, "update_node_meta", map[string]any{
		"id": "design", "writeAccess": "orchestrator",
	}, &updated)
	if strings.Join(updated.Changed, ",") != "writeAccess" {
		t.Fatalf("changed = %v, want only writeAccess", updated.Changed)
	}
	call(t, session, "get_node", map[string]any{"id": "design"}, &read)
	if read.WriteAccess != "orchestrator" {
		t.Fatalf("writeAccess = %q, want orchestrator", read.WriteAccess)
	}
}

// The whole feature, end to end: a human seals a node, and a worker-role
// session is refused with the server's reason rather than a credential chase.
func TestAWorkerSessionCannotModifyAHumanOnlyNode(t *testing.T) {
	url, _ := liveServer(t)
	human := mcpSession(t, url, "")
	call(t, human, "update_node_meta", map[string]any{
		"id": "design", "writeAccess": "human-only",
	}, nil)

	worker := roleSession(t, url, "worker")
	newTitle := "Retitled by a worker"
	refused := call(t, worker, "update_node_meta", map[string]any{
		"id": "design", "title": newTitle,
	}, nil)
	if !refused.IsError {
		t.Fatal("a worker modified a human-only node")
	}
	if !strings.Contains(errorText(refused), "human-only") {
		t.Fatalf("error = %q, want it to name the rule that refused the write", errorText(refused))
	}

	var read getNodeOutput
	call(t, human, "get_node", map[string]any{"id": "design"}, &read)
	if read.Title == newTitle {
		t.Fatal("the refused write went through anyway")
	}
}
