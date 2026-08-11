package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

func TestACreatedTaskWaitsForWhatItDependsOn(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var created createNodeOutput
	call(t, session, "create_node", map[string]any{
		"title":     "Write the docs",
		"body":      "# Write the docs\n\nCover the new endpoint.\n",
		"dependsOn": []string{"design"},
	}, &created)
	if created.ID == "" {
		t.Fatalf("create_node returned no id: %+v", created)
	}

	// design is unfinished, so the new task is not offered.
	var queue readyTasksOutput
	call(t, session, "get_ready_tasks", map[string]any{"why": true}, &queue)
	for _, task := range queue.Tasks {
		if task.ID == created.ID {
			t.Fatal("a task with an unfinished prerequisite was offered as ready")
		}
	}
	found := false
	for _, task := range queue.Blocked {
		if task.ID == created.ID {
			found = true
			if len(task.BlockedBy) != 1 || task.BlockedBy[0] != "design" {
				t.Fatalf("blocked by %v, want design", task.BlockedBy)
			}
		}
	}
	if !found {
		t.Fatalf("the new task is neither ready nor blocked: %+v", queue)
	}
}

// The node is made before the wiring, so a failure between them leaves a real
// node behind. Reporting that as an error would have the agent create it twice.
func TestAPartlyWiredNewNodeIsStillReportedAsCreated(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var created createNodeOutput
	call(t, session, "create_node", map[string]any{
		"title":     "Orphan",
		"dependsOn": []string{"no-such-node"},
	}, &created)
	if created.ID == "" {
		t.Fatalf("a created node was not reported: %+v", created)
	}
	if !strings.Contains(created.Note, "link_nodes") {
		t.Fatalf("note = %q, want it to say how to finish the wiring", created.Note)
	}
}

func TestWritingABodyWithoutTheCurrentRevIsRefused(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	missing := call(t, session, "update_node_body", map[string]any{
		"id": "design", "content": "# Design\n\nrewritten\n", "baseRev": "",
	}, nil)
	if !missing.IsError || !strings.Contains(errorText(missing), "get_node") {
		t.Fatalf("an unversioned write was allowed: %q", errorText(missing))
	}

	stale := call(t, session, "update_node_body", map[string]any{
		"id": "design", "content": "# Design\n\nrewritten\n", "baseRev": "000000000000",
	}, nil)
	if !stale.IsError {
		t.Fatal("a write against a stale rev overwrote the file")
	}
	if !strings.Contains(errorText(stale), "re-read") {
		t.Fatalf("error = %q, want it to say what to do about the conflict", errorText(stale))
	}
}

func TestARoundTripThroughGetNodeCanRewriteTheBody(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var read getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "design"}, &read)

	var written updateBodyOutput
	call(t, session, "update_node_body", map[string]any{
		"id": "design", "content": "# Design the thing\n\nRewritten by an agent.\n",
		"baseRev": read.Rev,
	}, &written)
	if written.Rev == "" || written.Rev == read.Rev {
		t.Fatalf("rev = %q, want a new one after the write", written.Rev)
	}

	call(t, session, "get_node", map[string]any{"id": "design"}, &read)
	if !strings.Contains(read.Body, "Rewritten by an agent") {
		t.Fatalf("body = %q, want the new text", read.Body)
	}
}

// A nil field means "leave alone". Reporting an untouched field as changed
// would be a claim the agent then repeats to a person.
func TestOnlyTheFieldsGivenAreChangedAndReported(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var updated updateMetaOutput
	call(t, session, "update_node_meta", map[string]any{
		"id": "build", "priority": "urgent",
	}, &updated)
	if strings.Join(updated.Changed, ",") != "priority" {
		t.Fatalf("changed = %v, want only priority", updated.Changed)
	}

	var read getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "build"}, &read)
	if read.Priority != "urgent" {
		t.Fatalf("priority = %q, want urgent", read.Priority)
	}
	if read.Title != "Build it" {
		t.Fatalf("title = %q, want it untouched", read.Title)
	}
	if read.Assignee != "claude" {
		t.Fatalf("assignee = %q, want it untouched", read.Assignee)
	}
}

func TestAskingToChangeNothingIsAnError(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "update_node_meta", map[string]any{"id": "build"}, nil)
	if !result.IsError {
		t.Fatal("an empty update was accepted")
	}
}

func TestDependenciesCanBeAddedAndRemoved(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var removed linkNodesOutput
	call(t, session, "link_nodes", map[string]any{
		"from": "design", "to": "build", "remove": true,
	}, &removed)

	// With the wire gone, build is actionable.
	var queue readyTasksOutput
	call(t, session, "get_ready_tasks", nil, &queue)
	ids := map[string]bool{}
	for _, task := range queue.Tasks {
		ids[task.ID] = true
	}
	if !ids["build"] {
		t.Fatalf("tasks = %+v, want build released once the dependency was removed", queue.Tasks)
	}

	call(t, session, "link_nodes", map[string]any{"from": "design", "to": "build"}, nil)
	call(t, session, "get_ready_tasks", nil, &queue)
	for _, task := range queue.Tasks {
		if task.ID == "build" {
			t.Fatal("build stayed actionable after the dependency was put back")
		}
	}
}

func TestANodeCannotBeMadeToDependOnItself(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "link_nodes", map[string]any{"from": "design", "to": "design"}, nil)
	if !result.IsError {
		t.Fatal("a self-dependency was accepted")
	}
}

// Positions, colours, groups, saved views: most of a large graph's bytes, and
// nothing a caller that cannot see the board can use.
func TestTheOutlineLeavesOutEverythingVisual(t *testing.T) {
	url, pm := liveServer(t)
	graph, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.UI = &engine.UIState{
		Positions:  map[string]engine.Position{"design": {X: 3, Y: 4}},
		NodeStyles: map[string]engine.NodeStyle{"design": {Color: "#ff0000"}},
	}
	if _, err := pm.Store().SaveGraph(graph, rev); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	session := mcpSession(t, url, "")

	result := call(t, session, "get_graph_outline", nil, nil)
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"positions", "nodeStyles", "#ff0000"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the outline carried %q: %s", leaked, raw)
		}
	}

	var outline Outline
	call(t, session, "get_graph_outline", nil, &outline)
	if outline.Total != 2 || len(outline.Edges) != 1 {
		t.Fatalf("outline = %+v, want two nodes and one edge", outline)
	}
}

// A thousand nodes pasted into a context window is not understanding, it is the
// appearance of it. Past the threshold the outline asks for a narrower question.
func TestAnOversizedBoardIsSummarisedRatherThanDumped(t *testing.T) {
	url, pm := liveServer(t)
	graph, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for index := range outlineThreshold + 5 {
		graph.Nodes = append(graph.Nodes, &engine.Node{
			ID:    "bulk-" + itoa(index),
			Title: "Bulk task",
		})
	}
	if _, err := pm.Store().SaveGraph(graph, rev); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	session := mcpSession(t, url, "")

	var outline Outline
	call(t, session, "get_graph_outline", nil, &outline)
	if len(outline.Nodes) != 0 {
		t.Fatalf("%d nodes were returned for an oversized board", len(outline.Nodes))
	}
	if !strings.Contains(outline.Summary, "Narrow it") {
		t.Fatalf("summary = %q, want it to ask for a narrower question", outline.Summary)
	}

	// And narrowing works.
	call(t, session, "get_graph_outline", map[string]any{"assignee": "claude"}, &outline)
	if len(outline.Nodes) != 1 || outline.Nodes[0].ID != "build" {
		t.Fatalf("narrowed outline = %+v, want only build", outline.Nodes)
	}
}

func TestACycleIsReportedAsAProblemToFix(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")
	linked := call(t, session, "link_nodes", map[string]any{"from": "build", "to": "design"}, nil)
	if linked.IsError {
		t.Fatalf("the cycle-making link was refused: %q", errorText(linked))
	}

	var checked validateOutput
	call(t, session, "validate_graph", nil, &checked)
	if len(checked.Issues) == 0 {
		t.Fatal("a dependency cycle was not reported")
	}
	if !strings.Contains(checked.Note, "ready queue") {
		t.Fatalf("note = %q, want it to say why the problem matters", checked.Note)
	}
}

func TestSearchFindsANodeByItsText(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var found searchOutput
	call(t, session, "search_nodes", map[string]any{"query": "shape"}, &found)
	if found.Total == 0 {
		t.Fatal("search found nothing for text that is in a node body")
	}
	for _, hit := range found.Results {
		if hit.NodeID == "design" {
			return
		}
	}
	t.Fatalf("results = %+v, want the design node", found.Results)
}

func TestATooShortSearchIsRefusedWithAReason(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "search_nodes", map[string]any{"query": "a"}, nil)
	if !result.IsError || !strings.Contains(errorText(result), "two characters") {
		t.Fatalf("a one-character search was not refused clearly: %q", errorText(result))
	}
}

// itoa avoids pulling strconv into the test for one call.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
