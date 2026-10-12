// Per-node write permission, exercised through the HTTP surface: the agent
// class arrives as a header, withAuth turns it into an actor, and the store's
// refusal comes back as a 403 the caller can act on.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
)

func humanOnlyNodeServer(t *testing.T) (*Server, *project.ProjectManager) {
	t.Helper()
	srv, pm := twoProjectServer(t)
	if _, err := pm.Store().CreateNode(&engine.Node{
		ID: "sealed", Title: "Sealed", WriteAccess: engine.WriteAccessHumanOnly,
	}, ""); err != nil {
		t.Fatalf("create human-only node: %v", err)
	}
	return srv, pm
}

func postSealedStatus(t *testing.T, handler http.Handler, header string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/sealed/status",
		strings.NewReader(`{"status":"done","by":"someone"}`))
	request.Header.Set("Content-Type", "application/json")
	if header != "" {
		request.Header.Set(auth.AgentRoleHeaderName, header)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWorkerActorIsRefusedOnAHumanOnlyNode(t *testing.T) {
	srv, _ := humanOnlyNodeServer(t)
	srv.auth = staticActorAuth{actor: identity.Actor{
		ID: "local", Name: "local", Role: identity.RoleAdmin, Agent: identity.AgentWorker,
	}}
	response := postSealedStatus(t, srv.Handler(), "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body.String(), "human-only") {
		t.Fatalf("body = %s, want the human-only denial message", response.Body)
	}
}

func TestAgentRoleHeaderBecomesTheActorClass(t *testing.T) {
	srv, _ := humanOnlyNodeServer(t)
	handler := srv.Handler() // loopback default: auth.LocalOnly reads the header

	worker := postSealedStatus(t, handler, "worker")
	if worker.Code != http.StatusForbidden ||
		!strings.Contains(worker.Body.String(), "human-only") {
		t.Fatalf("worker header status = %d, want 403 with denial; body=%s",
			worker.Code, worker.Body)
	}
	unknown := postSealedStatus(t, handler, "supervisor")
	if unknown.Code != http.StatusUnauthorized ||
		!strings.Contains(unknown.Body.String(), "worker") {
		t.Fatalf("unknown header status = %d, want actionable 401; body=%s",
			unknown.Code, unknown.Body)
	}
	human := postSealedStatus(t, handler, "")
	if human.Code != http.StatusOK {
		t.Fatalf("human status = %d, want 200; body=%s", human.Code, human.Body)
	}
}
