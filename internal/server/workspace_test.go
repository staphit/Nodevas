package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/project"
	"os"
	"path/filepath"
	"testing"
)

// resolvedTempDir is t.TempDir() with symlinks resolved, matching how the
// server canonicalizes every workspace root. On macOS t.TempDir() sits under
// /var, a symlink to /private/var; comparing a raw temp path against the
// server's resolved root would spuriously fail.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return real
}

func postWorkspacePathForTest(
	t *testing.T,
	pm *project.ProjectManager,
	endpoint string,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("marshal workspace request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	switch endpoint {
	case "/api/workspaces/add":
		projectapi.New(pm).PostWorkspaceAdd(ginContext(response, request))
	case "/api/workspaces/open":
		projectapi.New(pm).PostWorkspaceOpen(ginContext(response, request))
	case "/api/workspaces/remove":
		projectapi.New(pm).PostWorkspaceRemove(ginContext(response, request))
	default:
		t.Fatalf("unknown endpoint %q", endpoint)
	}
	return response
}

func TestWorkspaceRemoveKeepsFilesAndSwitchesToRemainingRoot(t *testing.T) {
	pm := projectManagerForTest(t)
	primary := pm.Workspace()
	sibling := resolvedTempDir(t)

	addResponse := postWorkspacePathForTest(t, pm, "/api/workspaces/add", sibling)
	if addResponse.Code != http.StatusOK {
		t.Fatalf("add workspace status = %d, body = %s", addResponse.Code, addResponse.Body.String())
	}
	marker := filepath.Join(sibling, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	removeResponse := postWorkspacePathForTest(t, pm, "/api/workspaces/remove", sibling)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("remove workspace status = %d, body = %s", removeResponse.Code, removeResponse.Body.String())
	}
	if !project.WorkspacePathEqual(pm.Workspace(), primary) {
		t.Fatalf("active workspace = %q, want %q", pm.Workspace(), primary)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("removed workspace changed disk file: data=%q err=%v", data, err)
	}
	infos, err := pm.WorkspaceInfos()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || !project.WorkspacePathEqual(infos[0].Path, primary) {
		t.Fatalf("workspace infos after remove = %+v", infos)
	}
}

func TestWorkspaceRemoveRejectsLastWorkspace(t *testing.T) {
	pm := projectManagerForTest(t)
	response := postWorkspacePathForTest(
		t,
		pm,
		"/api/workspaces/remove",
		pm.Workspace(),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("remove last status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestWorkspaceRemoveAllowsInitialWorkspace(t *testing.T) {
	pm := projectManagerForTest(t)
	initial := pm.Workspace()
	sibling := resolvedTempDir(t)
	if response := postWorkspacePathForTest(t, pm, "/api/workspaces/add", sibling); response.Code != http.StatusOK {
		t.Fatalf("add workspace status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := postWorkspacePathForTest(t, pm, "/api/workspaces/open", initial); response.Code != http.StatusOK {
		t.Fatalf("open initial status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := postWorkspacePathForTest(t, pm, "/api/workspaces/remove", initial); response.Code != http.StatusOK {
		t.Fatalf("remove initial status = %d, body = %s", response.Code, response.Body.String())
	}
	if !project.WorkspacePathEqual(pm.Workspace(), sibling) {
		t.Fatalf("active workspace = %q, want %q", pm.Workspace(), sibling)
	}
	if _, err := os.Stat(initial); err != nil {
		t.Fatalf("initial workspace was changed on disk: %v", err)
	}
}

func TestWorkspaceCatalogAddsSiblingAndSwitchesRoots(t *testing.T) {
	pm := projectManagerForTest(t)
	primary := pm.Workspace()
	sibling := resolvedTempDir(t)

	addResponse := postWorkspacePathForTest(
		t,
		pm,
		"/api/workspaces/add",
		sibling,
	)
	if addResponse.Code != http.StatusOK {
		t.Fatalf("add workspace status = %d, body = %s", addResponse.Code, addResponse.Body.String())
	}
	if !project.WorkspacePathEqual(pm.Workspace(), sibling) {
		t.Fatalf("active workspace = %q, want %q", pm.Workspace(), sibling)
	}
	if _, err := os.Stat(filepath.Join(sibling, "main", "graph.yaml")); err != nil {
		t.Fatalf("new workspace default project: %v", err)
	}
	infos, err := pm.WorkspaceInfos()
	if err != nil {
		t.Fatalf("workspaceInfos: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("workspace count = %d, want 2", len(infos))
	}
	if !infos[1].Active || infos[1].Projects != 1 {
		t.Fatalf("sibling workspace info = %+v", infos[1])
	}

	openResponse := postWorkspacePathForTest(
		t,
		pm,
		"/api/workspaces/open",
		primary,
	)
	if openResponse.Code != http.StatusOK {
		t.Fatalf("open workspace status = %d, body = %s", openResponse.Code, openResponse.Body.String())
	}
	if !project.WorkspacePathEqual(pm.Workspace(), primary) {
		t.Fatalf("active workspace = %q, want %q", pm.Workspace(), primary)
	}
}

func TestProjectMovesAcrossWorkspaceRootsAndOpensDestination(t *testing.T) {
	pm := projectManagerForTest(t)
	primary := pm.Workspace()
	sibling := resolvedTempDir(t)

	addResponse := postWorkspacePathForTest(
		t,
		pm,
		"/api/workspaces/add",
		sibling,
	)
	if addResponse.Code != http.StatusOK {
		t.Fatalf("add workspace status = %d, body = %s", addResponse.Code, addResponse.Body.String())
	}
	openResponse := postWorkspacePathForTest(
		t,
		pm,
		"/api/workspaces/open",
		primary,
	)
	if openResponse.Code != http.StatusOK {
		t.Fatalf("open primary status = %d, body = %s", openResponse.Code, openResponse.Body.String())
	}
	renameResponse := postProjectMoveForTest(t, pm, "main", "", "Story")
	if renameResponse.Code != http.StatusOK {
		t.Fatalf("rename source status = %d, body = %s", renameResponse.Code, renameResponse.Body.String())
	}
	notePath := filepath.Join(primary, "Story", "nodes", "note.md")
	if err := os.WriteFile(notePath, []byte("# Keep me\n"), 0o644); err != nil {
		t.Fatalf("write source note: %v", err)
	}

	body, err := json.Marshal(map[string]string{
		"name":         "Story",
		"newWorkspace": sibling,
	})
	if err != nil {
		t.Fatalf("marshal cross-workspace move: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/move",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	projectapi.New(pm).PostProjectMove(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("cross-workspace move status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(primary, "Story")); !os.IsNotExist(err) {
		t.Fatalf("source still exists or stat failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(sibling, "Story", "nodes", "note.md")); err != nil {
		t.Fatalf("read moved note: %v", err)
	} else if string(data) != "# Keep me\n" {
		t.Fatalf("moved note content = %q", data)
	}
	if !project.WorkspacePathEqual(pm.Workspace(), sibling) {
		t.Fatalf("active workspace = %q, want %q", pm.Workspace(), sibling)
	}
	active, err := pm.ActiveProject()
	if err != nil {
		t.Fatalf("activeProject after cross-workspace move: %v", err)
	}
	if active.Name != "Story" {
		t.Fatalf("active project = %q, want Story", active.Name)
	}
}
