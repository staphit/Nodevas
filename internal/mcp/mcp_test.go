package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/server"
)

// liveServer starts a real Nodevas server over loopback and returns its URL.
// The tools are only worth testing against the actual handlers: their whole job
// is to project what those return, and a stub would be a projection of a
// projection.
func liveServer(t *testing.T) (string, *project.ProjectManager) {
	t.Helper()
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(pm.StopWatcher)

	graph := &engine.Graph{
		Version: 1,
		Users:   []engine.User{{ID: "claude", Name: "claude"}},
		Nodes: []*engine.Node{
			{ID: "design", Title: "Design the thing", Priority: "high", Kind: "task"},
			{
				ID: "build", Title: "Build it", Assignee: "claude",
				Tags: []string{"backend"}, Requires: "design",
			},
		},
		// `requires` is executable truth; this edge is its canvas projection.
		Edges: []*engine.Edge{{From: "design", To: "build"}},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pm.Store().Root(), "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pm.Store().SaveNodeContent(identity.Local, "design",
		"# Design the thing\n\nWork out the shape before writing any of it.\n", ""); err != nil {
		t.Fatalf("write node body: %v", err)
	}

	http := httptest.NewServer(server.New(pm, realtime.NewHub(), nil).Handler())
	t.Cleanup(http.Close)
	return http.URL, pm
}

// mcpSession wires a real MCP client to the server under test, so every
// assertion travels the protocol rather than calling a handler directly.
func mcpSession(t *testing.T, url, projectName string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server, err := NewServer(ctx, Options{Server: url, Project: projectName, Actor: "mcp:test"})
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

// call runs a tool and decodes its structured output.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, out any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !result.IsError {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("%s: re-encode output: %v", name, err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode output: %v", name, err)
		}
	}
	return result
}

func errorText(result *sdk.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestTheQueueToolOffersOnlyUnblockedWork(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var out readyTasksOutput
	call(t, session, "get_ready_tasks", nil, &out)
	if len(out.Tasks) != 1 || out.Tasks[0].ID != "design" {
		t.Fatalf("tasks = %+v, want only design", out.Tasks)
	}
	if out.Waiting != 1 {
		t.Fatalf("waiting = %d, want 1", out.Waiting)
	}
	if !strings.Contains(out.Summary, "ready") {
		t.Fatalf("summary = %q, want it to state the queue", out.Summary)
	}
}

// An agent handed an empty list has to tell "the work is done" from "people are
// the blockers". Those call for opposite next moves, so the difference is said
// in words rather than left to be inferred from two counts.
func TestAnEmptyQueueSaysWhyItIsEmpty(t *testing.T) {
	url, pm := liveServer(t)
	if _, err := pm.Store().SetStatus("design", engine.StatusInProgress, "someone", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	session := mcpSession(t, url, "")

	var out readyTasksOutput
	call(t, session, "get_ready_tasks", map[string]any{"why": true}, &out)
	if len(out.Tasks) != 0 {
		t.Fatalf("tasks = %+v, want none", out.Tasks)
	}
	if !strings.Contains(out.Summary, "blocked by prerequisites or conditions") {
		t.Fatalf("summary = %q, want it to explain why the queue is empty", out.Summary)
	}
	if len(out.Blocked) != 1 || out.Blocked[0].ID != "build" {
		t.Fatalf("blocked = %+v, want build", out.Blocked)
	}
}

func TestReadingANodeReturnsItsBodyAndItsNeighbours(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var out getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "design"}, &out)
	if !strings.Contains(out.Body, "Work out the shape") {
		t.Fatalf("body = %q, want the file's text", out.Body)
	}
	if out.Rev == "" {
		t.Fatal("get_node returned no rev; a later write would have nothing to base itself on")
	}
	if len(out.Downstream) != 1 || out.Downstream[0] != "build" {
		t.Fatalf("downstream = %v, want build", out.Downstream)
	}
	if out.Status != string(engine.StatusReady) {
		t.Fatalf("status = %q, want ready", out.Status)
	}
}

func TestAnUnknownNodeIsAToolErrorRatherThanAProtocolFailure(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "get_node", map[string]any{"id": "nope"}, nil)
	if !result.IsError {
		t.Fatal("reading a missing node succeeded")
	}
	if !strings.Contains(errorText(result), "no node") {
		t.Fatalf("error = %q, want it to name the missing node", errorText(result))
	}
}

// The catalog carries the host path of every project. That is the one field
// that describes the operator's machine rather than the work, and nothing an
// agent does needs it.
func TestListingProjectsDoesNotLeakHostPaths(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "list_projects", nil, nil)
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"path"`) {
		t.Fatalf("list_projects carried a host path: %s", raw)
	}
}

func TestARemoteServerIsRefused(t *testing.T) {
	for _, target := range []string{
		"http://192.168.1.10:5666",
		"https://nodevas.example.com",
		// A name that might resolve to loopback today is still refused:
		// resolution can change, and "it pointed home when I checked" is not
		// a property to hang a trust boundary on.
		"http://my-laptop.local:5666",
	} {
		if _, err := NewClient(ClientOptions{Server: target}); err == nil {
			t.Fatalf("%s was accepted", target)
		}
	}
	if _, err := NewClient(ClientOptions{Server: "http://127.0.0.1:5666"}); err != nil {
		t.Fatalf("loopback was refused: %v", err)
	}
	if _, err := NewClient(ClientOptions{Server: "http://localhost:5666"}); err != nil {
		t.Fatalf("localhost was refused: %v", err)
	}
}

func TestAServerThatIsNotRunningSaysWhatToStart(t *testing.T) {
	// Port 1 on loopback: nothing listens there, and it is loopback so the
	// refusal under test is the connection, not the address check.
	client, err := NewClient(ClientOptions{Server: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Probe(context.Background())
	if err == nil {
		t.Fatal("probing a dead server succeeded")
	}
	if !strings.Contains(err.Error(), "nodevas serve") {
		t.Fatalf("error = %q, want it to name the command to run", err)
	}
}

func TestAConflictHandsBackWhatTheDiskSays(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict","diskRev":"abc123","diskContent":"theirs"}`))
	}))
	defer stub.Close()
	client, err := NewClient(ClientOptions{Server: stub.URL})
	if err != nil {
		t.Fatal(err)
	}

	err = client.get(context.Background(), "/api/graph", nil, &struct{}{})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error = %T, want *APIError", err)
	}
	if apiErr.Code != CodeConflict {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodeConflict)
	}
	if apiErr.CurrentRev != "abc123" || apiErr.DiskContent != "theirs" {
		t.Fatalf("conflict lost the disk version: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "re-read") {
		t.Fatalf("message = %q, want it to say what to do next", apiErr.Message)
	}
}

// A body cut at a byte boundary would hand back a broken character where a word
// was. The window is measured in characters for that reason.
func TestATruncatedBodyIsCutOnACharacterBoundary(t *testing.T) {
	body := "日本語のテキストです"

	head, truncated, next := sliceBody(body, 0, 3)
	if !truncated || head != "日本語" {
		t.Fatalf("head = %q (truncated %v), want the first three characters", head, truncated)
	}
	tail, truncatedAgain, _ := sliceBody(body, next, 100)
	if truncatedAgain {
		t.Fatal("the remainder reported itself as truncated")
	}
	if head+tail != body {
		t.Fatalf("the two halves do not reassemble: %q + %q", head, tail)
	}
}

func TestReadingPastTheEndOfABodyReturnsNothingRatherThanFailing(t *testing.T) {
	if body, truncated, next := sliceBody("short", 500, 100); body != "" || truncated || next != 0 {
		t.Fatalf("got %q/%v/%d, want an empty, finished read", body, truncated, next)
	}
}
