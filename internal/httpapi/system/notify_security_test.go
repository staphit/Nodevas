package system

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/notify"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"testing"

	"github.com/gin-gonic/gin"
)

type remoteNotifyAuth struct {
	actor identity.Actor
}

func (a remoteNotifyAuth) Authenticate(*http.Request) (identity.Actor, error) {
	return a.actor, nil
}
func (remoteNotifyAuth) NeedsCSRF() bool { return true }
func (remoteNotifyAuth) Remote() bool    { return true }

func notifySecurityManager(t *testing.T) *project.ProjectManager {
	t.Helper()
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return pm
}

func notifySecurityContext(response http.ResponseWriter, request *http.Request) *gin.Context {
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	return context
}

func TestRemoteNotifyAdministrationRequiresAdminRole(t *testing.T) {
	pm := notifySecurityManager(t)
	notifier := notify.NewNotifier(pm)
	member := identity.Actor{ID: "member-1", Name: "member", Role: identity.RoleMember}
	api := New(pm, notifier, remoteNotifyAuth{actor: member})

	request := httptest.NewRequest(http.MethodGet, "/api/notify/settings", nil)
	response := httptest.NewRecorder()
	api.getNotifySettings(notifySecurityContext(response, request))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}

	admin := identity.Actor{ID: "admin-1", Name: "admin", Role: identity.RoleAdmin}
	api = New(pm, notifier, remoteNotifyAuth{actor: admin})
	response = httptest.NewRecorder()
	api.getNotifySettings(notifySecurityContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestNotifySettingsEndpointRequiresNewPasswordForNewRelay(t *testing.T) {
	pm := notifySecurityManager(t)
	notifier := notify.NewNotifier(pm)
	settings := notify.DefaultNotifySettings()
	settings.SMTPHost = "smtp.example.com"
	settings.SMTPUser = "mailer"
	settings.SMTPPass = "original-password"
	settings.From = "nodevas@example.com"
	if err := notifier.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	settings.SMTPHost = "attacker.example.com"
	settings.SMTPPass = ""
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/notify/settings", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(pm, notifier, auth.LocalOnly{}).putNotifySettings(notifySecurityContext(response, request))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	kept := notifier.Settings()
	if kept.SMTPHost != "smtp.example.com" || kept.SMTPPass != "original-password" {
		t.Fatalf("rejected endpoint update changed credentials: host=%q pass=%q", kept.SMTPHost, kept.SMTPPass)
	}
}
