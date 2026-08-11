package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/project"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func postProjectCopyForTest(
	t *testing.T,
	pm *project.ProjectManager,
	name string,
	newName string,
	newParent string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name":      name,
		"newName":   newName,
		"newParent": newParent,
		"open":      true,
	})
	if err != nil {
		t.Fatalf("marshal copy request: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/copy",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	projectapi.New(pm).PostProjectCopy(ginContext(response, request))
	return response
}

func TestPostProjectCopyDuplicatesAndOpens(t *testing.T) {
	pm := projectManagerForTest(t)
	source := filepath.Join(pm.Workspace(), "main")
	if err := os.MkdirAll(filepath.Join(source, "nodes"), 0o755); err != nil {
		t.Fatalf("nodes dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "nodes", "a.md"), []byte("# A"), 0o644); err != nil {
		t.Fatalf("write node: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, store.DataDir, "drafts"), 0o755); err != nil {
		t.Fatalf("drafts dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(source, store.DataDir, "drafts", "a.md"), []byte("draft"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	response := postProjectCopyForTest(t, pm, "main", "main 副本", "")
	if response.Code != http.StatusOK {
		t.Fatalf("copy status = %d, body = %s", response.Code, response.Body.String())
	}

	copyRoot := filepath.Join(pm.Workspace(), "main 副本")
	if _, err := os.Stat(filepath.Join(copyRoot, "graph.yaml")); err != nil {
		t.Fatalf("copied graph.yaml: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(copyRoot, "nodes", "a.md"))
	if err != nil || string(data) != "# A" {
		t.Fatalf("copied node = %q, err = %v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(copyRoot, store.DataDir)); !os.IsNotExist(err) {
		t.Fatalf("%s should not be copied: %v", store.DataDir, err)
	}
	if _, err := os.Stat(filepath.Join(source, "graph.yaml")); err != nil {
		t.Fatalf("source project must stay intact: %v", err)
	}
	active, err := pm.ActiveProject()
	if err != nil {
		t.Fatalf("activeProject after copy: %v", err)
	}
	if active.Name != "main 副本" {
		t.Fatalf("active project = %q, want %q", active.Name, "main 副本")
	}
}

func TestPostProjectCopyRejectsExistingName(t *testing.T) {
	pm := projectManagerForTest(t)
	if err := os.MkdirAll(filepath.Join(pm.Workspace(), "taken"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	response := postProjectCopyForTest(t, pm, "main", "taken", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s",
			response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestPostProjectCopyRejectsNestedTarget(t *testing.T) {
	pm := projectManagerForTest(t)

	response := postProjectCopyForTest(t, pm, "main", "inner", "main")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s",
			response.Code, http.StatusBadRequest, response.Body.String())
	}
}
