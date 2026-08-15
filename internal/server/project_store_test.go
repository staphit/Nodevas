package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

// twoProjectServer returns a server whose workspace holds the active "main"
// project plus a second "other" project carrying one recognisable node.
func twoProjectServer(t *testing.T) (*Server, *project.ProjectManager) {
	t.Helper()
	pm := projectManagerForTest(t)
	if err := pm.Activate("other", true, ""); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	otherRoot := pm.Store().Root()
	graph := &engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "only-in-other", Title: "Only in other"}},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRoot, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pm.Activate("main", false, ""); err != nil {
		t.Fatalf("switch back to main: %v", err)
	}
	return serverForTest(t, pm, realtime.NewHub(), nil), pm
}

// A Store serializes writes with its own mutex, so a second Store over the
// same directory would serialize nothing. Every lookup must hand back one.
func TestStoreForReturnsOneInstancePerProject(t *testing.T) {
	_, pm := twoProjectServer(t)

	first, err := pm.StoreFor("other")
	if err != nil {
		t.Fatalf("StoreFor: %v", err)
	}
	second, err := pm.StoreFor("other")
	if err != nil {
		t.Fatalf("StoreFor again: %v", err)
	}
	if first != second {
		t.Fatalf("StoreFor returned two Store instances for one directory")
	}

	active, err := pm.StoreFor("main")
	if err != nil {
		t.Fatalf("StoreFor active: %v", err)
	}
	if active != pm.Store() {
		t.Fatalf("the active project's store is not the cached one")
	}
}

func TestStoreForRejectsUnknownProject(t *testing.T) {
	_, pm := twoProjectServer(t)

	if _, err := pm.StoreFor("nope"); err == nil {
		t.Fatal("StoreFor accepted a project that does not exist")
	}
	if _, err := pm.StoreFor("../escape"); err == nil {
		t.Fatal("StoreFor accepted a path outside the workspace")
	}
}

func TestRequestProjectReadsAnotherProjectWithoutSwitchingActive(t *testing.T) {
	server, pm := twoProjectServer(t)
	activeBefore := pm.Store().Root()

	for _, mode := range []string{"query", "header"} {
		target := "/api/graph"
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if mode == "query" {
			request = httptest.NewRequest(http.MethodGet, target+"?project=other", nil)
		} else {
			request.Header.Set(httpx.ProjectHeader, "other")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", mode, response.Code, response.Body)
		}
		var decoded struct {
			Graph *engine.Graph `json:"graph"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s: decode: %v", mode, err)
		}
		if decoded.Graph == nil || decoded.Graph.NodeByID("only-in-other") == nil {
			t.Fatalf("%s: read the active project instead of %q", mode, "other")
		}
	}

	if pm.Store().Root() != activeBefore {
		t.Fatalf("active project changed to %q", pm.Store().Root())
	}
}

// The write path has to land in the named project too, not in the active one.
func TestRequestProjectWritesToNamedProject(t *testing.T) {
	server, pm := twoProjectServer(t)

	body := strings.NewReader(`{"id":"added","title":"Added","body":"hi"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/nodes?project=other", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	other, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := other.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("added") == nil {
		t.Fatal("the node was not written to the named project")
	}
	activeGraph, _, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if activeGraph.NodeByID("added") != nil {
		t.Fatal("the node leaked into the active project")
	}
}

// An unknown project fails before the handler runs, so no request ever falls
// back to the active project by accident.
func TestRequestProjectRejectsUnknownProjectWithBadRequest(t *testing.T) {
	server, _ := twoProjectServer(t)

	request := httptest.NewRequest(http.MethodGet, "/api/graph?project=missing", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

// The project-management endpoints keep their own ?project= meaning: export
// accepts a grouping folder, which has no graph.yaml of its own.
func TestRequestProjectLeavesProjectEndpointsAlone(t *testing.T) {
	server, pm := twoProjectServer(t)
	if err := os.MkdirAll(filepath.Join(pm.Workspace(), "folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/projects/export?project=folder", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusBadRequest &&
		strings.Contains(response.Body.String(), "graph.yaml") {
		t.Fatalf("the project middleware hijacked an export scope: %s", response.Body)
	}
}

func TestEvictStoresDropsSubtree(t *testing.T) {
	_, pm := twoProjectServer(t)

	before, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	pm.EvictStores(pm.Workspace())
	after, err := pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("evictStores kept a store for a directory under the evicted root")
	}
}

// runtimeIsUnix reports whether file mode bits mean anything here: Windows
// does not carry POSIX permissions.
func runtimeIsUnix() bool { return runtime.GOOS != "windows" }
