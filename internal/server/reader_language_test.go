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

func TestTransferWarningsSpeakTheReadersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, _ := readerLanguageFixture(t)
			// Bravo requires Alpha, which stays behind.
			response := askIn(t, server, language, http.MethodPost, "/api/nodes/transfer",
				`{"target":"other","ids":["bravo"],"mode":"copy"}`)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			var result struct {
				Warnings []string `json:"warnings"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Warnings) != 1 {
				t.Fatalf("warnings = %q, want the dropped dependency", result.Warnings)
			}
			warning := result.Warnings[0]
			if !strings.Contains(warning, "Bravo") {
				t.Fatalf("warning %q does not name the node", warning)
			}
			if chinese := strings.HasPrefix(language, "zh"); hasCJK(warning) != chinese {
				t.Fatalf("warning for %s reads %q", language, warning)
			}
		})
	}
}

// Dragging an actual record before the stamp it follows is refused, and the
// refusal is in the reader's language.
func TestServerRefusalsSpeakTheReadersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, pm := readerLanguageFixture(t)
			st := pm.Store()
			if _, err := st.SetStatus("alpha", engine.StatusInProgress, "tester", ""); err != nil {
				t.Fatal(err)
			}
			state, err := st.SetStatus("alpha", engine.StatusDone, "tester", "")
			if err != nil {
				t.Fatal(err)
			}
			done := state.History[len(state.History)-1]
			response := askIn(t, server, language, http.MethodPost, "/api/events/move",
				`{"id":"`+done.ID+`","t":"2001-01-01T00:00:00Z"}`)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			var refusal struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
				t.Fatal(err)
			}
			if chinese := strings.HasPrefix(language, "zh"); hasCJK(refusal.Error) != chinese {
				t.Fatalf("refusal for %s reads %q", language, refusal.Error)
			}
		})
	}
}

// Creating a project whose name is taken says so in the reader's language.
func TestProjectNameTakenSpeaksTheReadersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, _ := readerLanguageFixture(t)
			response := askIn(t, server, language, http.MethodPost, "/api/projects/open",
				`{"name":"main","create":true}`)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			var refusal struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(refusal.Error, "main") {
				t.Fatalf("refusal %q does not name the project", refusal.Error)
			}
			if chinese := strings.HasPrefix(language, "zh"); hasCJK(refusal.Error) != chinese {
				t.Fatalf("refusal for %s reads %q", language, refusal.Error)
			}
			// Chinese quotes a name in 「」, not half-width ASCII quotes.
			if strings.HasPrefix(language, "zh") && !strings.Contains(refusal.Error, "「main」") {
				t.Fatalf("zh refusal %q does not quote the name in 「」", refusal.Error)
			}
		})
	}
}

// A graph.yaml that does not parse is the reader's to fix, not the server's
// failure: the board's reads answer 422 naming the line, in the reader's
// language, and a write refuses the same way.
func TestUnreadableGraphNamesTheLineInTheReadersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, pm := readerLanguageFixture(t)
			if err := os.WriteFile(pm.Store().GraphPath(),
				[]byte("version: 1\nnodes:\n  - id: [broken\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// The line the unclosed bracket is on (yaml.v3 itself says 2,
			// counting its parser errors from zero).
			line := "line 3 "
			if strings.HasPrefix(language, "zh") {
				line = "第 3 行"
			}
			for _, request := range []struct{ method, path, body string }{
				{http.MethodGet, "/api/graph", ""},
				{http.MethodGet, "/api/state", ""},
				{http.MethodPost, "/api/nodes", `{"title":"New"}`},
			} {
				response := askIn(t, server, language, request.method, request.path, request.body)
				if response.Code < 400 || response.Code >= 500 {
					t.Fatalf("%s %s: status = %d, body = %s", request.method, request.path, response.Code, response.Body)
				}
				var refusal struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(refusal.Error, "graph.yaml") || !strings.Contains(refusal.Error, line) {
					t.Fatalf("%s %s: %q does not name the file and line", request.method, request.path, refusal.Error)
				}
				if chinese := strings.HasPrefix(language, "zh"); hasCJK(refusal.Error) != chinese {
					t.Fatalf("%s %s for %s reads %q", request.method, request.path, language, refusal.Error)
				}
			}
			// Nothing was written over the file the reader has to fix.
			data, err := os.ReadFile(pm.Store().GraphPath())
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "version: 1\nnodes:\n  - id: [broken\n" {
				t.Fatalf("graph.yaml was rewritten: %q", data)
			}
		})
	}
}
