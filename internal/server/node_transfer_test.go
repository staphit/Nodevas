package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// transferFixture builds a workspace with a populated "main" project and an
// empty "other" one. main holds two linked nodes with everything a node can
// carry — document, attachment, subpage, position, style, plan, assignee —
// plus a third node that stays behind and depends on one of them.
func transferFixture(t *testing.T) (*Server, *project.ProjectManager) {
	t.Helper()
	pm := projectManagerForTest(t)
	if err := pm.Activate("other", true, ""); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	if err := pm.Activate("main", false, ""); err != nil {
		t.Fatalf("switch back to main: %v", err)
	}

	ratio := 0.25
	graph := &engine.Graph{
		Version: 1,
		Users:   []engine.User{{ID: "alice", Name: "Alice"}},
		Flags:   map[string]any{"cat_found": false, "unused": true},
		Nodes: []*engine.Node{
			{ID: "node-0001", Title: "First", Kind: "task", Assignee: "alice",
				Effects: []engine.Effect{{Set: "cat_found = true"}}},
			{ID: "node-0002", Title: "Second", Kind: "task",
				Requires: "node-0001 and flag(cat_found == true)"},
			{ID: "node-0003", Title: "Stays", Kind: "task"},
		},
		Edges: []*engine.Edge{
			{From: "node-0001", To: "node-0002"},
			{From: "node-0002", To: "node-0003"},
		},
		UI: &engine.UIState{
			Positions: map[string]engine.Position{
				"node-0001": {X: 4, Y: 1},
				"node-0002": {X: 5, Y: 1},
				"node-0003": {X: 6, Y: 1},
			},
			NodeStyles:    map[string]engine.NodeStyle{"node-0001": {Width: 200, Color: "#123456"}},
			Plans:         map[string][]engine.PlanMilestone{"node-0001": {{Date: "2026-01-02", Status: "custom-plan"}}},
			PlanStatuses:  []engine.PlanStatusDefinition{{ID: "custom-plan", Label: "審稿"}},
			Gates:         map[string]engine.GatePlacement{"node-0002": {Ratio: &ratio}},
			EdgeLabels:    map[string]engine.EdgeLabelPlacement{"node-0001->node-0002": {Ratio: 0.5}},
			WireVertices:  map[string][]engine.Position{"node-0001->node-0002": {{X: 1, Y: 2}}},
			TimelineOrder: []string{"node-0001", "node-0002", "node-0003"},
		},
	}
	st := pm.Store()
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.GraphPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, st.NodePath("node-0001"),
		"# First\n\n![shot](/api/nodes/node-0001/files/shot.png)\n")
	writeFixtureFile(t, st.NodePath("node-0002"), "# Second\n")
	writeFixtureFile(t, st.NodePath("node-0003"), "# Stays\n")
	writeFixtureFile(t, filepath.Join(st.NodeFilesDir("node-0001"), "shot.png"), "PNGDATA")
	writeFixtureFile(t, st.NodePagesManifestPath("node-0001"),
		`{"pages":[{"id":"page-a","title":"Notes","format":"md"}]}`)
	writeFixtureFile(t, st.NodePagePath("node-0001", "page-a", "md"),
		"see /api/nodes/node-0001/files/shot.png\n")

	return serverForTest(t, pm, realtime.NewHub(), nil), pm
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type transferResponse struct {
	OK         bool              `json:"ok"`
	Mode       string            `json:"mode"`
	IDs        map[string]string `json:"ids"`
	Order      []string          `json:"order"`
	Warnings   []string          `json:"warnings"`
	TrashFiles []string          `json:"trashFiles"`
	Error      string            `json:"error"`
}

func postTransfer(t *testing.T, server *Server, body string) (int, transferResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/transfer", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var decoded transferResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", response.Body, err)
	}
	return response.Code, decoded
}

// A copy has to arrive whole: content, attachments, subpages, layout, style,
// plan, and the project-level definitions those lean on.
func TestTransferCopyCarriesTheWholeNode(t *testing.T) {
	server, pm := transferFixture(t)

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0001","node-0002"],"mode":"copy"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	if len(result.IDs) != 2 || len(result.Order) != 2 {
		t.Fatalf("ids = %v, order = %v", result.IDs, result.Order)
	}
	firstID, secondID := result.IDs["node-0001"], result.IDs["node-0002"]

	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	first := graph.NodeByID(firstID)
	if first == nil || first.Title != "First" {
		t.Fatalf("first node did not arrive: %+v", first)
	}
	if first.Assignee != "alice" || len(graph.Users) != 1 {
		t.Fatalf("assignee did not travel: assignee = %q users = %+v", first.Assignee, graph.Users)
	}
	if _, ok := graph.Flags["cat_found"]; !ok {
		t.Fatalf("the flag an effect writes to was not carried: %+v", graph.Flags)
	}
	if _, ok := graph.Flags["unused"]; ok {
		t.Fatalf("an unrelated flag came along: %+v", graph.Flags)
	}

	// Requires is retargeted at the copies, never left pointing at the source.
	second := graph.NodeByID(secondID)
	if second == nil || !strings.Contains(second.Requires, firstID) {
		t.Fatalf("requires was not retargeted: %+v", second)
	}
	if strings.Contains(second.Requires, "node-0001") && firstID != "node-0001" {
		t.Fatalf("requires still names the source node: %q", second.Requires)
	}
	if !strings.Contains(second.Requires, "cat_found") {
		t.Fatalf("the flag comparison was lost: %q", second.Requires)
	}

	// The edge inside the selection survives; the one leaving it does not.
	edges := 0
	for _, edge := range graph.Edges {
		if edge.From == firstID && edge.To == secondID {
			edges++
		}
	}
	if edges != 1 || len(graph.Edges) != 1 {
		t.Fatalf("edges = %+v", graph.Edges)
	}

	if graph.UI == nil {
		t.Fatal("no ui state was carried")
	}
	if _, ok := graph.UI.Positions[firstID]; !ok {
		t.Fatalf("position was not carried: %+v", graph.UI.Positions)
	}
	// Relative layout is what matters, not absolute coordinates.
	if delta := graph.UI.Positions[secondID].X - graph.UI.Positions[firstID].X; delta != 1 {
		t.Fatalf("relative layout changed: delta = %v", delta)
	}
	if graph.UI.NodeStyles[firstID].Color != "#123456" {
		t.Fatalf("style was not carried: %+v", graph.UI.NodeStyles)
	}
	if len(graph.UI.Plans[firstID]) != 1 {
		t.Fatalf("plan was not carried: %+v", graph.UI.Plans)
	}
	if len(graph.UI.PlanStatuses) != 1 || graph.UI.PlanStatuses[0].ID != "custom-plan" {
		t.Fatalf("the plan's custom status was not defined here: %+v", graph.UI.PlanStatuses)
	}
	if _, ok := graph.UI.Gates[secondID]; !ok {
		t.Fatalf("dependency-gate placement was not carried: %+v", graph.UI.Gates)
	}
	if _, ok := graph.UI.EdgeLabels[firstID+"->"+secondID]; !ok {
		t.Fatalf("edge label key was not remapped: %+v", graph.UI.EdgeLabels)
	}
	if _, ok := graph.UI.WireVertices[firstID+"->"+secondID]; !ok {
		t.Fatalf("wire vertex key was not remapped: %+v", graph.UI.WireVertices)
	}
	if len(graph.UI.TimelineOrder) != 2 {
		t.Fatalf("timeline order = %+v", graph.UI.TimelineOrder)
	}

	// Files: document, attachment and subpage, with links pointing at the copy.
	document, err := os.ReadFile(other.NodePath(firstID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), "/api/nodes/"+firstID+"/files/shot.png") {
		t.Fatalf("attachment link was not retargeted: %s", document)
	}
	if _, err := os.Stat(filepath.Join(other.NodeFilesDir(firstID), "shot.png")); err != nil {
		t.Fatalf("attachment was not copied: %v", err)
	}
	page, err := os.ReadFile(other.NodePagePath(firstID, "page-a", "md"))
	if err != nil {
		t.Fatalf("subpage was not copied: %v", err)
	}
	if !strings.Contains(string(page), "/api/nodes/"+firstID+"/files/") {
		t.Fatalf("subpage link was not retargeted: %s", page)
	}

	// A copy leaves the source project exactly as it was.
	sourceGraph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceGraph.Nodes) != 3 {
		t.Fatalf("copy changed the source project: %d nodes", len(sourceGraph.Nodes))
	}
}

// A cut is a copy plus a soft delete: the nodes leave the source for its trash.
func TestTransferCutRemovesFromSource(t *testing.T) {
	server, pm := transferFixture(t)

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0001","node-0002"],"mode":"cut"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	if len(result.TrashFiles) != 2 {
		t.Fatalf("trash files = %+v", result.TrashFiles)
	}

	sourceGraph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if sourceGraph.NodeByID("node-0001") != nil || sourceGraph.NodeByID("node-0002") != nil {
		t.Fatal("cut left the nodes in the source project")
	}
	if sourceGraph.NodeByID("node-0003") == nil {
		t.Fatal("cut removed a node outside the selection")
	}
	trash, err := pm.Store().ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) < 2 {
		t.Fatalf("cut nodes are not recoverable from trash: %+v", trash)
	}

	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("target project = %d nodes", len(graph.Nodes))
	}
}

// A cut that cannot complete must fail before anything is written, otherwise
// the user is left with the nodes in both projects.
func TestTransferCutRefusesWhenSourceStillDependsOnTheSelection(t *testing.T) {
	server, pm := transferFixture(t)
	// node-0003 stays behind; make it depend on a node being cut.
	st := pm.Store()
	graph, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph.NodeByID("node-0003").Requires = "node-0002"
	if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
		t.Fatal(err)
	}

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0002"],"mode":"cut"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	targetGraph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(targetGraph.Nodes) != 0 {
		t.Fatalf("a refused cut still wrote to the target: %+v", targetGraph.Nodes)
	}
}

// A dependency on a node left behind cannot be kept and cannot be silently
// half-removed, so it is dropped and the user is told.
func TestTransferDropsDependenciesLeftBehindWithAWarning(t *testing.T) {
	server, pm := transferFixture(t)

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0002"],"mode":"copy"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("dropping a dependency was not reported")
	}
	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	copied := graph.NodeByID(result.IDs["node-0002"])
	if copied == nil {
		t.Fatal("the node did not arrive")
	}
	if copied.Requires != "" {
		t.Fatalf("a dangling dependency was kept: %q", copied.Requires)
	}
}

// The lifecycle stamps are part of the node: a moved node keeps its history.
func TestTransferCarriesLifecycleHistory(t *testing.T) {
	server, pm := transferFixture(t)
	if _, err := pm.Store().SetStatus("node-0001", engine.StatusStarted, "test", "開工"); err != nil {
		t.Fatal(err)
	}
	// Occupy the id the copy would otherwise inherit, so a stale node id in
	// the journal is visible instead of coincidentally correct.
	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.CreateNode(&engine.Node{ID: "node-0001", Title: "Existing"}, ""); err != nil {
		t.Fatal(err)
	}

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0001","node-0002"],"mode":"cut"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	state, err := other.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	newID := result.IDs["node-0001"]
	if state.Nodes[newID] == nil || state.Nodes[newID].Status != engine.StatusStarted {
		t.Fatalf("status did not travel: %+v", state.Nodes)
	}
	for _, event := range state.History {
		if event.Node == "node-0001" {
			t.Fatalf("a journal event kept the source node id: %+v", event)
		}
	}
}

// Node ids are per-project, so a collision is normal and must be resolved by
// renaming the incoming node — never by overwriting what is already there.
func TestTransferRenamesOnIDCollision(t *testing.T) {
	server, pm := transferFixture(t)
	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.CreateNode(&engine.Node{ID: "node-0001", Title: "Existing"}, ""); err != nil {
		t.Fatal(err)
	}

	code, result := postTransfer(t, server,
		`{"target":"other","ids":["node-0001"],"mode":"copy"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	if result.IDs["node-0001"] == "node-0001" {
		t.Fatal("the incoming node reused the id of an existing one")
	}
	graph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("node-0001").Title != "Existing" {
		t.Fatal("the existing node was overwritten")
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(graph.Nodes))
	}
}

func TestTransferRejectsSameProject(t *testing.T) {
	server, _ := transferFixture(t)

	code, result := postTransfer(t, server,
		`{"target":"main","ids":["node-0001"],"mode":"copy"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
}

func TestTransferRejectsUnknownTargetAndNodes(t *testing.T) {
	server, _ := transferFixture(t)

	if code, _ := postTransfer(t, server,
		`{"target":"nope","ids":["node-0001"],"mode":"copy"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown target: status = %d", code)
	}
	if code, _ := postTransfer(t, server,
		`{"target":"other","ids":["missing"],"mode":"copy"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown node: status = %d", code)
	}
	if code, _ := postTransfer(t, server,
		`{"target":"other","ids":[],"mode":"copy"}`); code != http.StatusBadRequest {
		t.Fatalf("empty selection: status = %d", code)
	}
}

// The source may be named explicitly, so a client showing project B can paste
// a selection that was cut from project A.
func TestTransferAcceptsAnExplicitSource(t *testing.T) {
	server, pm := transferFixture(t)
	if err := pm.Activate("other", false, ""); err != nil {
		t.Fatal(err)
	}

	code, result := postTransfer(t, server,
		`{"source":"main","target":"other","ids":["node-0001"],"mode":"copy"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %+v", code, result)
	}
	graph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID(result.IDs["node-0001"]) == nil {
		t.Fatal("the node did not land in the active target project")
	}
}
