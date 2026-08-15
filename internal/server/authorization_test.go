package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

type staticActorAuth struct{ actor identity.Actor }

func (a staticActorAuth) Authenticate(*http.Request) (identity.Actor, error) { return a.actor, nil }
func (staticActorAuth) NeedsCSRF() bool                                      { return false }
func (staticActorAuth) Remote() bool                                         { return true }

func TestMemberCanEditProjectsButCannotAdministerServer(t *testing.T) {
	srv, _ := twoProjectServer(t)
	srv.auth = staticActorAuth{actor: identity.Actor{
		ID: "member-1", Name: "member", Role: identity.RoleMember,
	}}
	handler := srv.Handler()

	for _, path := range []string{
		"/api/notify/settings",
		"/api/audit",
		"/api/audit/health",
		"/api/remote/config",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want 403; body=%s", path, response.Code, response.Body)
		}
	}
	auditAcknowledge := httptest.NewRequest(http.MethodPost, "/api/audit/health/acknowledge",
		strings.NewReader(`{"expectedFallbackEvents":0}`))
	auditAcknowledge.Header.Set("Content-Type", "application/json")
	auditAcknowledgeResponse := httptest.NewRecorder()
	handler.ServeHTTP(auditAcknowledgeResponse, auditAcknowledge)
	if auditAcknowledgeResponse.Code != http.StatusForbidden ||
		!strings.Contains(auditAcknowledgeResponse.Body.String(), "administrator access required") {
		t.Fatalf("audit acknowledgement status = %d, want generic admin 403; body=%s",
			auditAcknowledgeResponse.Code, auditAcknowledgeResponse.Body)
	}
	projectOpen := httptest.NewRequest(http.MethodPost, "/api/projects/open",
		strings.NewReader(`{"name":"main"}`))
	projectOpen.Header.Set("Content-Type", "application/json")
	projectOpenResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectOpenResponse, projectOpen)
	if projectOpenResponse.Code != http.StatusForbidden {
		t.Fatalf("project administration status = %d, want 403", projectOpenResponse.Code)
	}

	workspace := httptest.NewRequest(http.MethodPost, "/api/workspaces/add",
		strings.NewReader(`{"path":"C:\\sensitive"}`))
	workspace.Header.Set("Content-Type", "application/json")
	workspaceResponse := httptest.NewRecorder()
	handler.ServeHTTP(workspaceResponse, workspace)
	if workspaceResponse.Code != http.StatusForbidden {
		t.Fatalf("workspace admin status = %d, want 403", workspaceResponse.Code)
	}

	edit := httptest.NewRequest(http.MethodPost, "/api/nodes",
		strings.NewReader(`{"id":"member-edit","title":"Member edit","body":""}`))
	edit.Header.Set("Content-Type", "application/json")
	editResponse := httptest.NewRecorder()
	handler.ServeHTTP(editResponse, edit)
	if editResponse.Code != http.StatusCreated {
		t.Fatalf("member edit status = %d, want 201; body=%s", editResponse.Code, editResponse.Body)
	}
}

func TestAdministratorCanReadServerSettings(t *testing.T) {
	srv, _ := twoProjectServer(t)
	srv.auth = staticActorAuth{actor: identity.Actor{
		ID: "admin-1", Name: "admin", Role: identity.RoleAdmin,
	}}
	handler := srv.Handler()
	for _, path := range []string{"/api/notify/settings", "/api/remote/config"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, response.Code, response.Body)
		}
	}
}
