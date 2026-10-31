package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
)

// storyBoard adds a note, a review, a decision and a custom type to the live
// server's graph.
func storyBoard(t *testing.T, pm *project.ProjectManager) {
	t.Helper()
	g, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	countsInProgress := true
	g.NodeTypes = append(g.NodeTypes, engine.NodeTypeDef{
		ID: "custom-type-x", Label: "Beat", AgentMode: engine.AgentDispatch, Completion: engine.CompletionAnyone,
		CountsInProgress: &countsInProgress,
	})
	g.Nodes = append(g.Nodes,
		&engine.Node{ID: "note-01", Title: "角色卡：林曉", Kind: "note"},
		&engine.Node{ID: "review", Title: "Review the draft", Kind: "gate"},
		&engine.Node{ID: "pick", Title: "Pick an ending", Kind: "choice"},
	)
	if _, err := pm.Store().SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	_, noteRev, _ := pm.Store().LoadNodeContent("note-01")
	if _, _, err := pm.Store().SaveNodeContent(identity.Local, "note-01",
		"---\nid: note-01\nsummary: 林曉怕水，左手有疤。\n---\n\n林曉的完整設定。\n", noteRev); err != nil {
		t.Fatal(err)
	}
}

func TestASubgraphIsCreatedInOneCall(t *testing.T) {
	url, pm := liveServer(t)
	storyBoard(t, pm)
	session := mcpSession(t, url, "")

	var created createSubgraphOutput
	result := call(t, session, "create_subgraph", map[string]any{
		"nodes": []map[string]any{
			{"key": "s1", "title": "第一場", "kind": "scene", "body": "Draft it.", "refs": []string{"note-01"}},
			{"key": "s2", "title": "第二場", "kind": "scene", "dependsOn": []string{"s1"}, "refs": []string{"note-01"}},
			{"key": "s3", "title": "第三場", "kind": "custom-type-x", "dependsOn": []string{"s2", "design"}},
		},
	}, &created)
	if result.IsError {
		t.Fatal(errorText(result))
	}
	if len(created.IDs) != 3 || len(created.Created) != 3 ||
		created.Note != "Created 3 nodes, 3 dependencies and 2 references." {
		t.Fatalf("created = %+v", created)
	}
	s1, s2, s3 := created.IDs["s1"], created.IDs["s2"], created.IDs["s3"]

	var queue readyTasksOutput
	call(t, session, "get_ready_tasks", map[string]any{"limit": 200}, &queue)
	ready := map[string]bool{}
	for _, task := range queue.Tasks {
		ready[task.ID] = true
	}
	if !ready[s1] || ready[s2] || ready[s3] {
		t.Fatalf("only the root should be ready: %v", ready)
	}

	var child getNodeOutput
	call(t, session, "get_node", map[string]any{"id": s2}, &child)
	if !slices.Equal(child.Upstream, []string{s1}) {
		t.Fatalf("upstream = %v", child.Upstream)
	}
	if len(child.References) != 1 || child.References[0].ID != "note-01" ||
		child.References[0].Summary != "林曉怕水，左手有疤。" || child.Completion != "anyone" {
		t.Fatalf("references = %+v, completion %q", child.References, child.Completion)
	}
	var note getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "note-01"}, &note)
	if !slices.Equal(note.ReferencedBy, []string{s1, s2}) || note.Completion != "none" {
		t.Fatalf("referencedBy = %v, completion %q", note.ReferencedBy, note.Completion)
	}
	var outline Outline
	call(t, session, "get_graph_outline", map[string]any{"limit": 200}, &outline)
	for _, node := range outline.Nodes {
		if node.ID == s1 && !slices.Equal(node.Refs, []string{"note-01"}) {
			t.Fatalf("outline refs = %v", node.Refs)
		}
	}

	bad := call(t, session, "create_subgraph", map[string]any{
		"nodes": []map[string]any{{"key": "a", "title": "A", "dependsOn": []string{"nope"}}},
	}, nil)
	if !bad.IsError || !strings.Contains(errorText(bad), "nodes[0].dependsOn[0]") {
		t.Fatalf("unknown dependency: %q", errorText(bad))
	}
	badKind := call(t, session, "create_subgraph", map[string]any{
		"nodes": []map[string]any{{"key": "a", "title": "A", "kind": "custom-type-nope"}},
	}, nil)
	if !badKind.IsError || !strings.Contains(errorText(badKind), "get_graph_outline nodeTypes") {
		t.Fatalf("unknown kind: %q", errorText(badKind))
	}
}

func TestReferencesRoundTripThroughLinkNodes(t *testing.T) {
	url, pm := liveServer(t)
	storyBoard(t, pm)
	session := mcpSession(t, url, "")

	var linked linkNodesOutput
	call(t, session, "link_nodes", map[string]any{"from": "note-01", "to": "build", "relation": "reference"}, &linked)
	if linked.Note != "build now uses note-01 as a reference." {
		t.Fatalf("note = %q", linked.Note)
	}
	var node getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "build"}, &node)
	if len(node.References) != 1 || node.References[0].Title != "角色卡：林曉" || node.References[0].TypeLabel != "Note" {
		t.Fatalf("references = %+v", node.References)
	}
	// A reference never blocks: build still waits on design only.
	if !slices.Equal(node.Upstream, []string{"design"}) {
		t.Fatalf("upstream = %v", node.Upstream)
	}
	call(t, session, "link_nodes", map[string]any{"from": "note-01", "to": "build", "relation": "reference", "remove": true}, &linked)
	var after getNodeOutput
	call(t, session, "get_node", map[string]any{"id": "build"}, &after)
	if len(after.References) != 0 {
		t.Fatalf("references after remove = %+v", after.References)
	}
	for _, args := range []map[string]any{
		{"from": "note-01", "to": "build", "relation": "optional"},
		{"from": "build", "to": "build", "relation": "reference"},
	} {
		if result := call(t, session, "link_nodes", args, nil); !result.IsError {
			t.Fatalf("%v was accepted", args)
		}
	}
}

func TestAgentsAreToldWhatTheirTypesAllow(t *testing.T) {
	url, pm := liveServer(t)
	storyBoard(t, pm)
	worker := roleSession(t, url, "worker")

	var queue readyTasksOutput
	call(t, worker, "get_ready_tasks", map[string]any{"limit": 200}, &queue)
	for _, task := range queue.Tasks {
		if task.Kind == "gate" || task.Kind == "choice" {
			t.Fatalf("an agent was offered a %s: %+v", task.Kind, task)
		}
	}
	if queue.ForPeople != 2 || !strings.Contains(queue.Summary, "2 more ready node(s) need a person") {
		t.Fatalf("forPeople = %d, summary %q", queue.ForPeople, queue.Summary)
	}

	refused := call(t, worker, "set_node_status", map[string]any{"id": "review", "status": "done"}, nil)
	if !refused.IsError || !strings.Contains(errorText(refused), "Only a person") ||
		!strings.Contains(errorText(refused), "release_task") {
		t.Fatalf("set_node_status on a review: %q", errorText(refused))
	}
	client, err := NewClient(ClientOptions{Server: url, AgentRole: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	var apiErr *APIError
	if err := client.SetStatus(context.Background(), "review", "done", "", ""); !errors.As(err, &apiErr) ||
		apiErr.Code != CodeNodeTypePeopleOnly {
		t.Fatalf("code = %v", err)
	}

	var created createNodeOutput
	if result := call(t, worker, "create_node", map[string]any{"title": "Beat", "kind": "custom-type-x"}, &created); result.IsError {
		t.Fatalf("custom type: %s", errorText(result))
	}
	unknown := call(t, worker, "create_node", map[string]any{"title": "Beat", "kind": "custom-type-nope"}, nil)
	if !unknown.IsError || !strings.Contains(errorText(unknown), "get_graph_outline nodeTypes") {
		t.Fatalf("unknown type: %q", errorText(unknown))
	}
}

func TestValidateCountsHintsApart(t *testing.T) {
	url, pm := liveServer(t)
	g, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.NodeByID("build").Kind = "bug"
	if _, err := pm.Store().SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	var out validateOutput
	call(t, mcpSession(t, url, ""), "validate_graph", map[string]any{}, &out)
	if out.Note != "No problems found. (1 hint(s))" || len(out.Issues) != 1 || out.Issues[0].Severity != "info" {
		t.Fatalf("validate = %+v", out)
	}
}

func TestGuideExplainsTypeRefusals(t *testing.T) {
	for code, want := range map[string]string{
		CodeNodeTypePeopleOnly:   "Only a person",
		CodeNodeTypeNoStatus:     "no status",
		CodeNodeTypeForbidsAgent: "get_ready_tasks",
		CodeUnknownNodeType:      "nodeTypes",
		CodeAgentRestoreDenied:   "Ask a person",
	} {
		err := guide(&APIError{Code: code, Message: "refused."})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != code || !strings.Contains(apiErr.Message, want) {
			t.Errorf("%s: %v", code, err)
		}
	}
}

func TestSearchNodesAnswersAWholeChineseQuestion(t *testing.T) {
	url, pm := liveServer(t)
	awaitSearchIndex(t, pm)
	storyBoard(t, pm)
	session := mcpSession(t, url, "")
	// The note's body reaches the built index in the background, a debounce
	// and one pass after the save returns, so the answer is awaited.
	var out searchOutput
	for began := time.Now(); ; {
		out = searchOutput{}
		result := call(t, session, "search_nodes", map[string]any{"query": "林曉怕不怕水？她的手有什麼特徵"}, &out)
		if result.IsError {
			t.Fatal(errorText(result))
		}
		if len(out.Results) > 0 || time.Since(began) > 10*time.Second {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(out.Results) == 0 || out.Results[0].NodeID != "note-01" || out.Results[0].Snippet == "" ||
		out.Results[0].Match != "terms" || !strings.Contains(out.Note, "No exact phrase match") {
		t.Fatalf("search = %+v", out)
	}
}
