package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	projectapi "nodevas/internal/httpapi/project"
	"os"
	"path/filepath"
	"testing"
)

func TestPostProjectImportPathCreatesProjectFromEmptyFolder(t *testing.T) {
	pm := projectManagerForTest(t)
	source := t.TempDir()
	body, err := json.Marshal(map[string]string{
		"path": source,
		"name": "空白工作區",
	})
	if err != nil {
		t.Fatalf("marshal import request: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/import-path",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	projectapi.New(pm).PostProjectImportPath(ginContext(response, request))

	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}
	destination := filepath.Join(pm.Workspace(), "空白工作區")
	if _, err := os.Stat(filepath.Join(destination, "graph.yaml")); err != nil {
		t.Fatalf("empty project graph: %v", err)
	}
	nodes, err := os.ReadDir(filepath.Join(destination, "nodes"))
	if err != nil {
		t.Fatalf("empty project nodes directory: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("empty project has %d node files, want 0", len(nodes))
	}
	active, err := pm.ActiveProject()
	if err != nil {
		t.Fatalf("activeProject after empty import: %v", err)
	}
	if active.Name != "空白工作區" {
		t.Fatalf("active project = %q, want %q", active.Name, "空白工作區")
	}
}
