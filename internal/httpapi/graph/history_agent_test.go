package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/store"
)

func TestAnAgentRestoringGraphYAMLIsForbidden(t *testing.T) {
	api, st := graphTestAPI(t)
	current, err := os.ReadFile(st.GraphPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteVersioned(st.GraphPath(), current); err != nil {
		t.Fatal(err)
	}
	versions, err := st.ListHistory("graph.yaml")
	if err != nil || len(versions) == 0 {
		t.Fatalf("history: %v %v", versions, err)
	}
	body, _ := json.Marshal(map[string]string{"path": "graph.yaml", "version": versions[0].Name})
	request := httptest.NewRequest(http.MethodPost, "/api/history/restore", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	agent := identity.Local
	agent.Agent = identity.AgentOrchestrator
	request = request.WithContext(auth.WithActor(request.Context(), agent))
	response := httptest.NewRecorder()
	api.postHistoryRestore(ginContext(response, request))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), store.CodeAgentRestoreDenied) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}
