package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"nodevas/demo"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/project"
)

func postProjectExampleForTest(
	t *testing.T,
	pm *project.ProjectManager,
	example string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"example": example})
	if err != nil {
		t.Fatalf("marshal example request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/projects/example", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	projectapi.New(pm).PostProjectExample(ginContext(response, request))
	return response
}

func TestDemoEmbedsTheExamples(t *testing.T) {
	names := demo.Names()
	want := []string{"ai-harness-engineering", "daily-work-schedule", "novel-writing"}
	if len(names) != len(want) {
		t.Fatalf("examples = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("examples = %v, want %v", names, want)
		}
	}
}

func TestPostProjectExampleCopiesAndOpens(t *testing.T) {
	pm := projectManagerForTest(t)

	response := postProjectExampleForTest(t, pm, "novel-writing")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Name   string `json:"name"`
		Active string `json:"active"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Name != "novel-writing" || result.Active != "novel-writing" {
		t.Fatalf("result = %+v", result)
	}

	root := filepath.Join(pm.Workspace(), "novel-writing")
	for _, rel := range []string{
		"graph.yaml",
		filepath.Join("nodes", "node-0001.md"),
		filepath.Join("run", "state.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("example file %s: %v", rel, err)
		}
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "demo", "novel-writing", "graph.yaml"))
	if err != nil {
		t.Fatalf("read source graph: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "graph.yaml"))
	if err != nil {
		t.Fatalf("read copied graph: %v", err)
	}
	// The copy's plan dates move to today (internal/project/example.go);
	// apart from them it is the example.
	undated := func(data []byte) []byte {
		return regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`).ReplaceAll(data, []byte("DATE"))
	}
	if !bytes.Equal(undated(got), undated(want)) {
		t.Fatal("copied graph.yaml differs from the example")
	}
	if pm.Store().Root() != root {
		t.Fatalf("active root = %q, want %q", pm.Store().Root(), root)
	}

	// A second open gives a second copy rather than refusing or overwriting.
	again := postProjectExampleForTest(t, pm, "novel-writing")
	if again.Code != http.StatusOK {
		t.Fatalf("second open: status = %d, body = %s", again.Code, again.Body.String())
	}
	if err := json.Unmarshal(again.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Name != "novel-writing-2" {
		t.Fatalf("second copy named %q, want novel-writing-2", result.Name)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "novel-writing-2", "graph.yaml")); err != nil {
		t.Fatalf("second copy: %v", err)
	}
}

func TestPostProjectExampleRefusesUnknownNames(t *testing.T) {
	pm := projectManagerForTest(t)
	for _, example := range []string{"no-such-example", "../demo", "novel-writing/nodes", "."} {
		response := postProjectExampleForTest(t, pm, example)
		if response.Code != http.StatusNotFound {
			t.Errorf("%q: status = %d, want 404 (body %s)", example, response.Code, response.Body.String())
		}
	}
	if response := postProjectExampleForTest(t, pm, ""); response.Code != http.StatusBadRequest {
		t.Errorf("empty example: status = %d, want 400", response.Code)
	}
	entries, err := os.ReadDir(pm.Workspace())
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "main" && entry.IsDir() && entry.Name()[0] != '.' {
			t.Errorf("refused example left %q behind", entry.Name())
		}
	}
}
