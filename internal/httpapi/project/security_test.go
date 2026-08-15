package project

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func securityTestManager(t *testing.T) *projectdomain.ProjectManager {
	t.Helper()
	pm, err := projectdomain.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return pm
}

func securityTestContext(response http.ResponseWriter, request *http.Request) *gin.Context {
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	return context
}

func TestRemoteProjectCatalogRedactsHostPathsAndOtherWorkspaces(t *testing.T) {
	pm := securityTestManager(t)
	primary := pm.Workspace()
	sibling := t.TempDir()
	if err := pm.AddWorkspaceRoot(sibling); err != nil {
		t.Fatal(err)
	}
	pm.SetRemote(true)

	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	response := httptest.NewRecorder()
	New(pm).GetProjects(securityTestContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), primary) || strings.Contains(response.Body.String(), sibling) {
		t.Fatalf("remote catalog leaked a host path: %s", response.Body.String())
	}
	var payload struct {
		Workspace  string                        `json:"workspace"`
		Workspaces []projectdomain.WorkspaceInfo `json:"workspaces"`
		Projects   []projectdomain.ProjectInfo   `json:"projects"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Workspace != "" {
		t.Fatalf("workspace path = %q, want redacted", payload.Workspace)
	}
	if len(payload.Workspaces) != 1 || !payload.Workspaces[0].Active || payload.Workspaces[0].Path != "" {
		t.Fatalf("remote workspaces = %+v", payload.Workspaces)
	}
	for _, info := range payload.Projects {
		if info.Path != "" {
			t.Fatalf("project %q leaked path %q", info.Name, info.Path)
		}
	}
}

// Search is registered outside the admin group, so any authenticated member of
// a networked server can reach it. It must scrub host paths exactly like the
// catalog does, or the weaker route becomes the way to map the server's disk.
func TestRemoteSearchRedactsHostPaths(t *testing.T) {
	pm := securityTestManager(t)
	primary := pm.Workspace()
	pm.SetRemote(true)

	request := httptest.NewRequest(http.MethodGet, "/api/search?q=main", nil)
	response := httptest.NewRecorder()
	New(pm).GetSearch(securityTestContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), primary) {
		t.Fatalf("remote search leaked a host path: %s", response.Body.String())
	}
	var payload struct {
		Results []projectdomain.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range payload.Results {
		if result.Kind != "project" {
			continue
		}
		found = true
		if result.Snippet != "" {
			t.Fatalf("project result %q leaked snippet %q", result.Project, result.Snippet)
		}
	}
	if !found {
		t.Fatal("no project result; the redaction assertion tested nothing")
	}
}

// The desktop case is the other half of the contract: a local user owns the
// filesystem, and the path is genuinely useful there.
func TestLocalSearchKeepsHostPaths(t *testing.T) {
	pm := securityTestManager(t)

	request := httptest.NewRequest(http.MethodGet, "/api/search?q=main", nil)
	response := httptest.NewRecorder()
	New(pm).GetSearch(securityTestContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Results []projectdomain.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, result := range payload.Results {
		if result.Kind == "project" && result.Snippet == "" {
			t.Fatalf("local search dropped the path for %q", result.Project)
		}
	}
}

func TestRemoteProjectMoveCannotCrossWorkspaceBoundary(t *testing.T) {
	pm := securityTestManager(t)
	primary := pm.Workspace()
	sibling := t.TempDir()
	if err := pm.AddWorkspaceRoot(sibling); err != nil {
		t.Fatal(err)
	}
	pm.SetRemote(true)
	body, err := json.Marshal(map[string]string{
		"name":         "main",
		"newWorkspace": sibling,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/projects/move", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(pm).PostProjectMove(securityTestContext(response, request))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(primary, "main", "graph.yaml")); err != nil {
		t.Fatalf("source project changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sibling, "main")); !os.IsNotExist(err) {
		t.Fatalf("destination was created: %v", err)
	}
}

func TestRemoteProjectOpenRedactsRootPath(t *testing.T) {
	pm := securityTestManager(t)
	pm.SetRemote(true)
	request := httptest.NewRequest(http.MethodPost, "/api/projects/open",
		strings.NewReader(`{"name":"main"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(pm).PostProjectOpen(securityTestContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["root"] != "" || strings.Contains(response.Body.String(), pm.Workspace()) {
		t.Fatalf("project open leaked host path: %s", response.Body)
	}
}
