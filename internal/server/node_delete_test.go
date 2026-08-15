package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/audit"
	"nodevas/internal/db"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

// deleteTestServer builds a project whose nodes cover the cases a batch delete
// has to get right: a plain chain, a dependency, and a node behind a gate.
func deleteTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	pm := projectManagerForTest(t)
	root := pm.Store().Root()
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "alpha", Title: "Alpha"},
			{ID: "beta", Title: "Beta"},
			{ID: "gamma", Title: "Gamma"},
			{ID: "needs-alpha", Title: "Needs alpha", Requires: "alpha"},
		},
		Edges: []*engine.Edge{
			{From: "alpha", To: "beta"},
			{From: "beta", To: "gamma"},
		},
		UI: &engine.UIState{
			Positions: map[string]engine.Position{
				"alpha": {X: 0, Y: 0}, "beta": {X: 100, Y: 0},
				"gamma": {X: 200, Y: 0}, "needs-alpha": {X: 300, Y: 0},
			},
			TimelineOrder: []string{"alpha", "beta", "gamma", "needs-alpha"},
			NodeStyles:    map[string]engine.NodeStyle{"beta": {Shape: "diamond"}},
			EdgeLabels:    map[string]engine.EdgeLabelPlacement{"alpha->beta": {Ratio: 0.5}},
			WireVertices: map[string][]engine.Position{
				"beta->gamma": {{X: 150, Y: 40}},
			},
		},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		body := "---\nid: " + node.ID + "\n---\n\n# " + node.Title + "\n"
		if err := os.WriteFile(
			filepath.Join(root, "nodes", node.ID+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return serverForTest(t, pm, realtime.NewHub(), nil), root
}

func postDeleteNodes(t *testing.T, server *Server, ids []string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/delete", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestDeleteNodesRemovesTheWholeSelection(t *testing.T) {
	server, root := deleteTestServer(t)

	response := postDeleteNodes(t, server, []string{"beta", "gamma"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	var decoded struct {
		TrashFiles []string `json:"trashFiles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TrashFiles) != 2 {
		t.Fatalf("trash files = %v, want one per node", decoded.TrashFiles)
	}

	graph, _, err := server.store(nil).LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("beta") != nil || graph.NodeByID("gamma") != nil {
		t.Error("the deleted nodes are still in the graph")
	}
	if graph.NodeByID("alpha") == nil {
		t.Error("an unrelated node was removed")
	}
	if len(graph.Edges) != 0 {
		t.Errorf("edges = %+v, want the ones touching a deleted node gone", graph.Edges)
	}
	for _, id := range []string{"beta", "gamma"} {
		if _, ok := graph.UI.Positions[id]; ok {
			t.Errorf("%s still has a canvas position", id)
		}
		if _, err := os.Stat(filepath.Join(root, "nodes", id+".md")); !os.IsNotExist(err) {
			t.Errorf("%s.md is still on disk", id)
		}
	}
	if _, ok := graph.UI.NodeStyles["beta"]; ok {
		t.Error("beta still has a card style")
	}
	if len(graph.UI.EdgeLabels) != 0 || len(graph.UI.WireVertices) != 0 {
		t.Errorf("stale wire decorations survived: %+v %+v",
			graph.UI.EdgeLabels, graph.UI.WireVertices)
	}
	if len(graph.UI.TimelineOrder) != 2 {
		t.Errorf("timeline order = %v", graph.UI.TimelineOrder)
	}

	// Both documents are recoverable.
	trash, err := server.store(nil).ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 2 {
		t.Fatalf("trash = %+v, want both documents", trash)
	}
}

// Deleting a node something still depends on prunes that dependency instead
// of refusing: the deletion is an editing gesture, and the surviving node's
// expression is repaired for it.
func TestDeleteNodesPrunesSurvivingDependencies(t *testing.T) {
	server, root := deleteTestServer(t)

	response := postDeleteNodes(t, server, []string{"alpha", "beta"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	graph, _, err := server.store(nil).LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("nodes = %d, want gamma and needs-alpha left", len(graph.Nodes))
	}
	survivor := graph.NodeByID("needs-alpha")
	if survivor == nil {
		t.Fatal("needs-alpha should have survived")
	}
	if survivor.Requires != "" {
		t.Errorf("requires = %q, want the removed reference pruned", survivor.Requires)
	}
	for _, id := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(root, "nodes", id+".md")); !os.IsNotExist(err) {
			t.Errorf("%s.md is still on disk", id)
		}
	}
}

// Deleting a node together with the one that depends on it is fine — the
// dependency goes away with it.
func TestDeleteNodesAllowsDeletingADependencyPair(t *testing.T) {
	server, _ := deleteTestServer(t)

	response := postDeleteNodes(t, server, []string{"alpha", "needs-alpha"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	graph, _, err := server.store(nil).LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("nodes = %d, want beta and gamma left", len(graph.Nodes))
	}
}

func TestDeleteNodesRejectsBadInput(t *testing.T) {
	server, _ := deleteTestServer(t)

	if got := postDeleteNodes(t, server, []string{}).Code; got != http.StatusBadRequest {
		t.Errorf("empty ids status = %d, want 400", got)
	}
	if got := postDeleteNodes(t, server, []string{"../escape"}).Code; got != http.StatusBadRequest {
		t.Errorf("invalid id status = %d, want 400", got)
	}
	if got := postDeleteNodes(t, server, []string{"ghost"}).Code; got != http.StatusBadRequest {
		t.Errorf("unknown id status = %d, want 400", got)
	}
	// A repeated id is the same request as a single one, not an error.
	if got := postDeleteNodes(t, server, []string{"gamma", "gamma"}).Code; got != http.StatusOK {
		t.Errorf("duplicate id status = %d, want 200", got)
	}
}

// The single-node route now runs through the batch path; it must behave
// exactly as it did.
func TestDeleteNodeStillWorksThroughTheBatchPath(t *testing.T) {
	server, root := deleteTestServer(t)

	request := httptest.NewRequest(http.MethodDelete, "/api/nodes/gamma", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	var decoded struct {
		TrashFile string `json:"trashFile"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(decoded.TrashFile, "-gamma.md") {
		t.Errorf("trash file = %q", decoded.TrashFile)
	}
	if _, err := os.Stat(filepath.Join(root, "nodes", "gamma.md")); !os.IsNotExist(err) {
		t.Error("gamma.md is still on disk")
	}
}

func TestCommittedDeleteWithPendingCleanupStillSucceedsBroadcastsAndAudits(t *testing.T) {
	server, root := deleteTestServer(t)
	database, err := db.Open(server.pm.Workspace())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server.UseAudit(audit.New(database))
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	watcher := joinRoom(t, httpServer, "main")

	// The source file can be removed, but the draft is an unsafe symlink. This
	// deterministically fails only after graph.yaml commits and proves the HTTP
	// and event contracts do not turn that success back into an error.
	draftDir := filepath.Join(root, ".vised", "drafts")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-draft.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(draftDir, "gamma.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/nodes/gamma", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("committed delete status = %d, body = %s", response.Code, response.Body)
	}
	var body struct {
		OK             bool   `json:"ok"`
		TrashFile      string `json:"trashFile"`
		CleanupPending bool   `json:"cleanupPending"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || !body.CleanupPending || body.TrashFile == "" {
		t.Fatalf("committed delete response = %+v", body)
	}
	watcher.await("graph-changed", nil)

	entries, err := server.audit.Query(context.Background(), audit.Filter{Project: root})
	if err != nil {
		t.Fatal(err)
	}
	var audited bool
	for _, entry := range entries {
		audited = audited || entry.Action == "DELETE nodes/gamma"
	}
	if !audited {
		t.Fatalf("committed delete was not audited: %+v", entries)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside" {
		t.Fatalf("cleanup followed the draft symlink: %q, %v", data, err)
	}
}

func TestDeleteHandlersFailClosedWithSanitizedUnavailableQueue(t *testing.T) {
	tests := map[string]func(t *testing.T, server *Server) (*httptest.ResponseRecorder, func()){
		"node": func(t *testing.T, server *Server) (*httptest.ResponseRecorder, func()) {
			request := httptest.NewRequest(http.MethodDelete, "/api/nodes/gamma", nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			return response, func() {
				graph, _, err := server.store(nil).LoadGraph()
				if err != nil || graph.NodeByID("gamma") == nil {
					t.Fatalf("unavailable cleanup queue changed graph: graph=%+v err=%v", graph, err)
				}
			}
		},
		"page": func(t *testing.T, server *Server) (*httptest.ResponseRecorder, func()) {
			page, _, _, err := server.store(nil).CreateNodePage("alpha", "Notes", "md")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodDelete, "/api/nodes/alpha/pages/"+page.ID, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			return response, func() {
				pages, err := server.store(nil).ListNodePages("alpha")
				if err != nil || len(pages) != 1 || pages[0].ID != page.ID {
					t.Fatalf("unavailable cleanup queue changed manifest: pages=%+v err=%v", pages, err)
				}
			}
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			server, root := deleteTestServer(t)
			queueDir := filepath.Join(root, ".vised", "cleanup")
			if err := os.MkdirAll(queueDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(queueDir, "delete-corrupt.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}

			response, assertUnchanged := call(t, server)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			if body := response.Body.String(); !strings.Contains(body, "Service Unavailable") || strings.Contains(body, "delete-corrupt") || strings.Contains(body, root) {
				t.Fatalf("cleanup queue response was not sanitized: %s", body)
			}
			assertUnchanged()
		})
	}
}
