package remoteapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/remote"
)

type currentActorAuth struct {
	actor    identity.Actor
	revision string
	exists   bool
}

func (a currentActorAuth) Authenticate(*http.Request) (identity.Actor, error) { return a.actor, nil }
func (currentActorAuth) NeedsCSRF() bool                                      { return true }
func (currentActorAuth) Remote() bool                                         { return true }
func (a currentActorAuth) CurrentActor(context.Context, string) (identity.Actor, string, bool) {
	return a.actor, a.revision, a.exists
}

func remoteSecurityAPI(t *testing.T) *API {
	t.Helper()
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	pm.SetRemote(true)
	return New(pm, remote.NewManager(pm), auth.LocalOnly{})
}

func driveOwnerContext(actor identity.Actor) *gin.Context {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest("GET", "/api/remote/drive/folders", nil)
	context.Request = request.WithContext(auth.WithActor(request.Context(), actor))
	return context
}

func TestDriveOwnerGateRejectsAnotherAccountAndUnownedToken(t *testing.T) {
	api := remoteSecurityAPI(t)
	store := api.remotes.DriveTokens()
	if err := store.Save(remote.DriveToken{RefreshToken: "refresh", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	other := driveOwnerContext(identity.Actor{ID: "other", Role: identity.RoleAdmin})
	if api.ensureDriveOwner(other) {
		t.Fatal("another administrator was allowed to use the owner's Drive token")
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(remote.DriveToken{RefreshToken: "unowned"}); err != nil {
		t.Fatal(err)
	}
	unowned := driveOwnerContext(identity.Actor{ID: "admin", Role: identity.RoleAdmin})
	if api.ensureDriveOwner(unowned) {
		t.Fatal("an unowned token was usable without reconnecting")
	}
}

func TestDriveOwnerGateAllowsOwnerAndUnconnectedSetup(t *testing.T) {
	api := remoteSecurityAPI(t)
	actor := identity.Actor{ID: "owner", Role: identity.RoleAdmin}
	if !api.ensureDriveOwner(driveOwnerContext(actor)) {
		t.Fatal("an administrator could not configure an unconnected Drive backend")
	}
	if err := api.remotes.DriveTokens().Save(remote.DriveToken{
		RefreshToken: "refresh", OwnerID: actor.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if !api.ensureDriveOwner(driveOwnerContext(actor)) {
		t.Fatal("the token owner was rejected")
	}
}

func TestOAuthCallbackDiesWithAdminRevisionOrRoleChange(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		role     identity.Role
		revision string
		exists   bool
	}{
		{name: "password changed", role: identity.RoleAdmin, revision: "rev-2", exists: true},
		{name: "demoted", role: identity.RoleMember, revision: "rev-1", exists: true},
		{name: "removed", role: identity.RoleAdmin, revision: "rev-1", exists: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			api := remoteSecurityAPI(t)
			api.auth = currentActorAuth{
				actor:    identity.Actor{ID: "owner", Role: testCase.role},
				revision: testCase.revision,
				exists:   testCase.exists,
			}
			state, _, err := api.remotes.NewOAuthState("owner", "rev-1")
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodGet,
				"/api/remote/drive/callback?code=code&state="+state, nil)
			api.getDriveCallback(context)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
			}
		})
	}
}
