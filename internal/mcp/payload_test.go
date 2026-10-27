package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// Measure actual MCP envelopes, including the SDK's text and structured forms.
func TestAgentPayloadMeasurements(t *testing.T) {
	endpoint, pm := liveServer(t)
	graph, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 98; i++ {
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: fmt.Sprintf("task-%03d", i), Title: "A representative task on the board", Kind: "task"})
	}
	if _, err := pm.Store().SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatal(err)
	}
	_, rev, err = pm.Store().LoadNodeContent("design")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pm.Store().SaveNodeContent(identity.Local, "design", strings.Repeat("Useful document content. ", 500), rev); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, endpoint, "")
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	t.Logf("tools/list: %d bytes; instructions: %d bytes", len(raw), len(instructions("fixture", false)))
	// 18000: W1.5 added save_attachment and attachment fields to the network
	// tools, with their descriptions already cut to the bone. 18500: node
	// types added the kind vocabulary to create_node and the board's layout
	// and nodeTypes to get_graph_outline, which a planner needs to pick types.
	if len(raw) > 18500 || len(instructions("fixture", false)) > 600 {
		t.Fatal("agent catalog exceeded its context budget")
	}
	for _, name := range []string{"get_node", "get_graph_outline"} {
		args := map[string]any{}
		if name == "get_node" {
			args["id"] = "design"
		}
		result := call(t, session, name, args, nil)
		if result.IsError {
			t.Fatal(errorText(result))
		}
		raw, _ = json.Marshal(result)
		t.Logf("%s: %d bytes", name, len(raw))
		// 7000: the outline carries the board's node types (nine built-ins
		// plus the project's own), about 1.5 KB of it in both copies.
		if len(raw) > 7000 {
			t.Fatalf("%s exceeded the default response budget", name)
		}
	}
	var body getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "design"}, &body)
	if !body.Truncated || body.NextOffset != 2000 {
		t.Fatal("default body page lost continuation")
	}
	complete, expectedRev := body.Body, body.Rev
	for body.Truncated {
		next := getNodeOutput{}
		call(t, session, "get_node", map[string]any{"id": "design", "offset": body.NextOffset}, &next)
		if next.Rev != expectedRev {
			t.Fatal("unchanged document revision drifted")
		}
		complete += next.Body
		body = next
	}
	disk, _, err := pm.Store().LoadNodeContent("design")
	if err != nil || complete != disk {
		t.Fatal("body pagination lost content")
	}
	call(t, session, "get_node", map[string]any{"id": "design", "maxBodyChars": 60000}, &body)
	if body.Truncated || body.Body != disk {
		t.Fatal("explicit full read no longer works")
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		var page Outline
		call(t, session, "get_graph_outline", map[string]any{"cursor": cursor}, &page)
		if len(page.Nodes) > 25 {
			t.Fatal("outline page exceeded default budget")
		}
		for _, node := range page.Nodes {
			if seen[node.ID] {
				t.Fatal("duplicate paged node")
			}
			seen[node.ID] = true
		}
		if page.Cursor == "" {
			break
		}
		cursor = page.Cursor
	}
	if len(seen) != 100 {
		t.Fatal("outline pagination lost nodes")
	}
}
