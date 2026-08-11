package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func projectManagerForTest(t *testing.T) *project.ProjectManager {
	t.Helper()
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectManager: %v", err)
	}
	t.Cleanup(pm.StopWatcher)
	return pm
}

// serverForTest builds a Server and makes sure its background loops are stopped
// before the test's temporary workspace is removed. New starts the notifier and
// the scheduled backup loop, and only Shutdown closes the channel they watch;
// without this a finished test leaves a backup loop writing into a directory
// t.TempDir is trying to delete, which fails the run under load.
//
// The ordering here is the point, and t.Cleanup runs LIFO. This registration
// lands after projectManagerForTest has registered the t.TempDir removal, so
// Shutdown runs before it — the loops are gone by the time the tree goes away.
// It lands before any t.Cleanup a caller adds for its own httptest.Server, so
// that one runs first, which is what Shutdown's doc comment asks for: stop
// accepting new work, then stop the loops. Reordering this reintroduces a
// failure that only shows up when the whole suite runs.
func serverForTest(
	t *testing.T,
	pm *project.ProjectManager,
	hub *realtime.Hub,
	webFS fs.FS,
) *Server {
	t.Helper()
	server := New(pm, hub, webFS)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shut the server down: %v", err)
		}
	})
	return server
}

func postProjectMoveForTest(
	t *testing.T,
	pm *project.ProjectManager,
	name string,
	newParent string,
	newName string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"name":      name,
		"newParent": newParent,
		"newName":   newName,
	})
	if err != nil {
		t.Fatalf("marshal move request: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/move",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	projectapi.New(pm).PostProjectMove(ginContext(response, request))
	return response
}

func TestPostProjectMoveRenamesActiveProject(t *testing.T) {
	pm := projectManagerForTest(t)

	response := postProjectMoveForTest(t, pm, "main", "", "重新命名")
	if response.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "重新命名", "graph.yaml")); err != nil {
		t.Fatalf("renamed project graph: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "main")); !os.IsNotExist(err) {
		t.Fatalf("old project path still exists or stat failed: %v", err)
	}
	active, err := pm.ActiveProject()
	if err != nil {
		t.Fatalf("activeProject after rename: %v", err)
	}
	if active.Name != "重新命名" {
		t.Fatalf("active project = %q, want %q", active.Name, "重新命名")
	}
}

func TestPostProjectMoveRenamesGroupingFolder(t *testing.T) {
	pm := projectManagerForTest(t)
	oldChild := filepath.Join(pm.Workspace(), "Stellaris", "節點")
	if err := os.MkdirAll(oldChild, 0o755); err != nil {
		t.Fatalf("create grouping folder: %v", err)
	}

	response := postProjectMoveForTest(t, pm, "Stellaris", "", "星海")
	if response.Code != http.StatusOK {
		t.Fatalf("rename folder status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "星海", "節點")); err != nil {
		t.Fatalf("renamed child folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Stellaris")); !os.IsNotExist(err) {
		t.Fatalf("old grouping folder still exists or stat failed: %v", err)
	}
}

func TestPostProjectMoveRejectsRenameCollision(t *testing.T) {
	pm := projectManagerForTest(t)
	if err := os.Mkdir(filepath.Join(pm.Workspace(), "existing"), 0o755); err != nil {
		t.Fatalf("create collision folder: %v", err)
	}

	response := postProjectMoveForTest(t, pm, "main", "", "existing")
	if response.Code != http.StatusConflict {
		t.Fatalf("collision status = %d, body = %s", response.Code, response.Body.String())
	}
}
