package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolsetSession is mcpSession with a toolset, returning the initialize
// result's instructions too.
func toolsetSession(t *testing.T, url, toolset string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server, err := NewServer(ctx, Options{Server: url, Actor: "run:claude", Toolset: toolset})
	if err != nil {
		t.Fatalf("NewServer(%q): %v", toolset, err)
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

func listedTools(t *testing.T, session *sdk.ClientSession) ([]string, map[string]string) {
	t.Helper()
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	defs := map[string]string{}
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		raw, _ := json.Marshal(tool)
		defs[tool.Name] = string(raw)
	}
	slices.Sort(names)
	return names, defs
}

// fullTools is every tool without RAG, which needs --chroma-url.
var fullTools = []string{
	"approve_tool", "ask_user", "check_inbox", "claim_task", "create_node", "create_subgraph", "get_graph_outline", "get_node",
	"get_ready_tasks", "link_nodes", "list_projects", "list_team", "release_task", "save_attachment",
	"search_nodes", "send_message", "set_acceptance", "set_node_status", "submit_verdict", "update_node_body", "update_node_meta",
	"validate_graph",
}

func TestFullToolsetListsEveryTool(t *testing.T) {
	url, _ := liveServer(t)
	for _, toolset := range []string{"", ToolsetFull} {
		names, _ := listedTools(t, toolsetSession(t, url, toolset))
		if !slices.Equal(names, fullTools) {
			t.Errorf("toolset %q lists %d tools %q, want the %d %q", toolset, len(names), names, len(fullTools), fullTools)
		}
	}
}

func TestRunToolsetListsOnlyWhatRunsUse(t *testing.T) {
	url, _ := liveServer(t)
	run := toolsetSession(t, url, ToolsetRun)
	names, defs := listedTools(t, run)
	want := []string{"approve_tool", "ask_user", "check_inbox", "claim_task", "create_node", "create_subgraph", "get_node",
		"link_nodes", "release_task", "send_message", "set_node_status", "submit_verdict"}
	if !slices.Equal(names, want) {
		t.Fatalf("run toolset lists %q, want %q", names, want)
	}
	// The same definitions external clients see, only fewer of them.
	_, full := listedTools(t, toolsetSession(t, url, ToolsetFull))
	for name, def := range defs {
		if full[name] != def {
			t.Errorf("%s differs from the full toolset's definition:\n run: %s\nfull: %s", name, def, full[name])
		}
	}
	if got := run.InitializeResult().Instructions; strings.Contains(got, "get_ready_tasks") {
		t.Errorf("run instructions point at a tool the run toolset leaves out: %q", got)
	}
}

func TestRunToolsetRefusesAnExcludedTool(t *testing.T) {
	url, _ := liveServer(t)
	run := toolsetSession(t, url, ToolsetRun)
	for _, name := range []string{"get_ready_tasks", "update_node_body", "search_nodes"} {
		result, err := run.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if text := errorText(result); !result.IsError || !strings.Contains(text, "not available") ||
			!strings.Contains(text, "--toolset run") {
			t.Errorf("%s under the run toolset: isError %v, %q", name, result.IsError, text)
		}
	}
	// An allowed tool still works.
	var node getNodeOutput
	if result := call(t, run, "get_node", map[string]any{"id": "design"}, &node); result.IsError || node.ID != "design" {
		t.Fatalf("get_node under the run toolset: %s", errorText(result))
	}
}

func TestUnknownToolsetIsRefused(t *testing.T) {
	if _, err := NewServer(context.Background(), Options{Server: "http://127.0.0.1:1", Toolset: "tiny"}); err == nil ||
		!strings.Contains(err.Error(), "tiny") {
		t.Fatalf("NewServer with toolset tiny: %v", err)
	}
}
