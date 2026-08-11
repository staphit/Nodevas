package project

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"

	"github.com/gin-gonic/gin"
)

// searchTestWorkspace creates a workspace holding one project with count
// nodes, every one of which matches "needle".
func searchTestWorkspace(t *testing.T, count int) *projectdomain.ProjectManager {
	t.Helper()
	workspace := t.TempDir()
	root := filepath.Join(workspace, "proj")
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := &engine.Graph{Version: 1}
	for i := range count {
		id := fmt.Sprintf("n%03d", i)
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: id, Title: "Node " + id})
		body := fmt.Sprintf("body %d with a needle in it and 中文说明", i)
		if err := os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := projectdomain.NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return pm
}

func runSearch(t *testing.T, api *API, query string) []projectdomain.SearchResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/search?q="+url.QueryEscape(query), nil)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	api.GetSearch(context)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Results []projectdomain.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Results
}

func TestSearchResultsStayCappedAtOneHundred(t *testing.T) {
	api := New(searchTestWorkspace(t, 150))
	results := runSearch(t, api, "needle")
	if len(results) != 100 {
		t.Fatalf("results = %d, want the cap of 100", len(results))
	}
	for _, result := range results {
		if result.Kind != "node" {
			t.Fatalf("unexpected result kind %q", result.Kind)
		}
	}
	// Warm the index and run it again: the cap is not an artefact of the cold
	// build path.
	if second := runSearch(t, api, "needle"); len(second) != 100 {
		t.Fatalf("warm results = %d, want 100", len(second))
	}
}

func TestSearchShapeAndCJKQueriesSurviveTheIndex(t *testing.T) {
	api := New(searchTestWorkspace(t, 3))

	results := runSearch(t, api, "中文说明")
	if len(results) != 3 {
		t.Fatalf("CJK query returned %d results", len(results))
	}
	for _, result := range results {
		if result.Kind != "node" || result.NodeID == "" || result.Title == "" || result.Snippet == "" {
			t.Fatalf("result lost part of its shape: %+v", result)
		}
		if result.Project != "proj" {
			t.Fatalf("result project = %q", result.Project)
		}
	}

	// A project-name hit still reports the host path locally...
	local := runSearch(t, api, "proj")
	found := false
	for _, result := range local {
		if result.Kind == "project" {
			found = true
			if result.Snippet == "" {
				t.Fatal("a local project hit lost its path snippet")
			}
		}
	}
	if !found {
		t.Fatal("project-name matching stopped working")
	}
}

// The path-redaction gate on project hits is a security fix; searching must
// not be a way around it.
func TestRemoteSearchStillRedactsHostPaths(t *testing.T) {
	pm := searchTestWorkspace(t, 2)
	workspace := pm.Workspace()
	pm.SetRemote(true)
	for _, result := range runSearch(t, New(pm), "proj") {
		if result.Kind == "project" && result.Snippet != "" {
			t.Fatalf("remote search leaked a host path: %q", result.Snippet)
		}
		if strings.Contains(result.Snippet, workspace) {
			t.Fatalf("remote search leaked the workspace path: %q", result.Snippet)
		}
	}
}
