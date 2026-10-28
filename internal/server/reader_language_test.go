package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
)

// The hub writes what it says in the reader's language: the browser sends
// that as Accept-Language, so English readers never get a Chinese sentence
// and Chinese readers never get an English one.

// readerLanguageFixture is a "main" project holding Alpha and Bravo, Bravo
// requiring Alpha, beside an empty "other" project.
func readerLanguageFixture(t *testing.T) (*Server, *project.ProjectManager) {
	t.Helper()
	pm := projectManagerForTest(t)
	if err := pm.Activate("other", true, "", ""); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	if err := pm.Activate("main", false, "", ""); err != nil {
		t.Fatalf("switch back to main: %v", err)
	}
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "alpha", Title: "Alpha", Kind: "task"},
			{ID: "bravo", Title: "Bravo", Kind: "task", Requires: "alpha"},
		},
		Edges: []*engine.Edge{{From: "alpha", To: "bravo"}},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pm.Store().GraphPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return serverForTest(t, pm, realtime.NewHub(), nil), pm
}

// askIn sends one request from a reader whose browser asks for language.
func askIn(t *testing.T, server *Server, language, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept-Language", language)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

// hasCJK reports whether text holds a Chinese character or full-width
// punctuation, which an English reader cannot be expected to read.
func hasCJK(text string) bool {
	for _, r := range text {
		if (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

func TestDuplicateNamesTheCopyInTheReadersLanguage(t *testing.T) {
	for language, want := range map[string]string{
		"en-US,en;q=0.9": "Alpha copy",
		"zh-TW,zh;q=0.9": "Alpha（副本）",
	} {
		t.Run(language, func(t *testing.T) {
			server, pm := readerLanguageFixture(t)
			response := askIn(t, server, language, http.MethodPost, "/api/nodes/alpha/duplicate", "")
			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			graph, _, err := pm.Store().LoadGraph()
			if err != nil {
				t.Fatal(err)
			}
			duplicate := graph.NodeByID(created.ID)
			if duplicate == nil {
				t.Fatalf("copy %q is not in the graph", created.ID)
			}
			if duplicate.Title != want {
				t.Fatalf("copy title = %q, want %q", duplicate.Title, want)
			}
		})
	}
}
