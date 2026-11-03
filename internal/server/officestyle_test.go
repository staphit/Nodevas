package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/store"
)

// The office style travels with the graph through PUT /api/graph: a person
// sets it and everyone watching hears graph-changed; an agent may not.
func TestOfficeStyleIsAPersonsToSet(t *testing.T) {
	httpServer, server := hubTestServer(t)
	watcher := joinRoom(t, httpServer, "main")
	handler := httpServer.Config.Handler

	put := func(style string, agent bool) *httptest.ResponseRecorder {
		t.Helper()
		g, rev, err := server.pm.Store().LoadGraph()
		if err != nil {
			t.Fatal(err)
		}
		g.OfficeStyle = style
		body, err := json.Marshal(map[string]any{"graph": g, "baseRev": rev})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/graph", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		if agent {
			request.Header.Set(auth.AgentRoleHeaderName, string(identity.AgentOrchestrator))
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := put(engine.OfficeStyleStudy, true)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), store.CodeOfficeStyleAgentEdit) {
		t.Fatalf("agent setting the style: %d %s", response.Code, response.Body)
	}
	watcher.awaitNone("graph-changed", 100*time.Millisecond)

	if response := put("Space", false); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown style: %d %s", response.Code, response.Body)
	}

	if response := put(engine.OfficeStyleStudy, false); response.Code != http.StatusOK {
		t.Fatalf("a person setting the style: %d %s", response.Code, response.Body)
	}
	watcher.await("graph-changed", nil)
	g, _, err := server.pm.Store().LoadGraph()
	if err != nil || g.OfficeStyle != engine.OfficeStyleStudy {
		t.Fatalf("stored style = %q, %v", g.OfficeStyle, err)
	}

	// GET /api/graph carries it, and the project list resolves it.
	request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, request)
	if !strings.Contains(got.Body.String(), `"officeStyle":"study"`) {
		t.Fatalf("GET /api/graph = %s", got.Body)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	got = httptest.NewRecorder()
	handler.ServeHTTP(got, request)
	if !strings.Contains(got.Body.String(), `"officeStyle":"study","officeStyleSet":true`) {
		t.Fatalf("GET /api/projects = %s", got.Body)
	}
}
