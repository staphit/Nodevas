package remote

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"nodevas/internal/identity"
	"nodevas/internal/project"
)

// fixedActors is an account directory frozen at one answer, which is what the
// consent rules actually depend on: what the directory says at the moment the
// redirect comes back, not what it said when the flow started.
type fixedActors struct {
	actor    identity.Actor
	revision string
	exists   bool
}

func (d fixedActors) CurrentActor(context.Context, string) (identity.Actor, string, bool) {
	return d.actor, d.revision, d.exists
}

// driveAuthManager is a networked workspace with an OAuth client configured, so
// the consent rules are the only thing standing between a request and Google.
func driveAuthManager(t *testing.T) *RemoteManager {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv("NODEVAS_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	pm, err := project.NewManagerAt(t.TempDir(), nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	pm.SetRemote(true)
	manager := NewManager(pm)
	if err := manager.SaveDriveCredentials("client-id", "client-secret"); err != nil {
		t.Fatal(err)
	}
	return manager
}

func pendingStateFor(t *testing.T, manager *RemoteManager, actorID, revision string) string {
	t.Helper()
	state, _, err := manager.NewOAuthState(actorID, revision)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestConsentRefusesAPendingStateWhoseActorIsNoLongerAnAdministrator(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		actors  fixedActors
		wantErr error
	}{
		{
			name:    "demoted after starting the flow",
			actors:  fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleMember}, revision: "rev-1", exists: true},
			wantErr: ErrDriveOAuthExpired,
		},
		{
			name:    "removed after starting the flow",
			actors:  fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleAdmin}, revision: "rev-1", exists: false},
			wantErr: ErrDriveOAuthExpired,
		},
		{
			// A deployment whose authenticator cannot look an account up has no
			// way to confirm the actor, which is a refusal rather than a pass.
			name:    "no directory to ask",
			wantErr: ErrDriveOAuthExpired,
		},
	} {
		manager := driveAuthManager(t)
		state := pendingStateFor(t, manager, "owner", "rev-1")
		var actors ActorDirectory
		if testCase.actors.actor.ID != "" {
			actors = testCase.actors
		}
		_, err := manager.CompleteDriveAuth(context.Background(), DriveCallbackRequest{
			Actors: actors,
			Code:   "the-code",
			State:  state,
			Host:   "nodevas.example",
		})
		if !errors.Is(err, testCase.wantErr) {
			t.Errorf("%s: CompleteDriveAuth returned %v, want %v", testCase.name, err, testCase.wantErr)
		}
		if fault := DriveFaultOf(err); fault != DriveFaultForbidden {
			t.Errorf("%s: fault = %d, want forbidden", testCase.name, fault)
		}
	}
}

func TestConsentRefusesAPendingStateIssuedAgainstAnOlderIdentityRevision(t *testing.T) {
	manager := driveAuthManager(t)
	state := pendingStateFor(t, manager, "owner", "rev-1")
	_, err := manager.CompleteDriveAuth(context.Background(), DriveCallbackRequest{
		// Still an administrator, but the account changed underneath the flow —
		// a password reset is what invalidates a redirect somebody else kept.
		Actors: fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleAdmin}, revision: "rev-2", exists: true},
		Code:   "the-code",
		State:  state,
		Host:   "nodevas.example",
	})
	if !errors.Is(err, ErrDriveOAuthExpired) {
		t.Fatalf("CompleteDriveAuth returned %v, want %v", err, ErrDriveOAuthExpired)
	}

	// A state minted without any revision at all cannot be revalidated, so it
	// is refused even when the directory would vouch for the actor now.
	stale := pendingStateFor(t, manager, "owner", "")
	_, err = manager.CompleteDriveAuth(context.Background(), DriveCallbackRequest{
		Actors: fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleAdmin}, exists: true},
		Code:   "the-code",
		State:  stale,
		Host:   "nodevas.example",
	})
	if !errors.Is(err, ErrDriveOAuthExpired) {
		t.Fatalf("revisionless state returned %v, want %v", err, ErrDriveOAuthExpired)
	}
}

func TestConsentRefusesACallbackThatNamesNobody(t *testing.T) {
	manager := driveAuthManager(t)
	admin := fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleAdmin}, revision: "rev-1", exists: true}
	for _, testCase := range []struct {
		name    string
		request DriveCallbackRequest
	}{
		{name: "unknown state", request: DriveCallbackRequest{Code: "the-code", State: "never-issued"}},
		{name: "no code", request: DriveCallbackRequest{State: pendingStateFor(t, manager, "owner", "rev-1")}},
		{name: "actorless state", request: DriveCallbackRequest{
			Code: "the-code", State: pendingStateFor(t, manager, "", "rev-1"),
		}},
	} {
		testCase.request.Actors = admin
		testCase.request.Host = "nodevas.example"
		_, err := manager.CompleteDriveAuth(context.Background(), testCase.request)
		if !errors.Is(err, ErrDriveOAuthCallback) {
			t.Errorf("%s: CompleteDriveAuth returned %v, want %v", testCase.name, err, ErrDriveOAuthCallback)
		}
		if fault := DriveFaultOf(err); fault != DriveFaultRequest {
			t.Errorf("%s: fault = %d, want request", testCase.name, fault)
		}
	}
}

func TestOnlyTheConnectingAccountMayDisconnectDrive(t *testing.T) {
	manager := driveAuthManager(t)
	if err := manager.DriveTokens().Save(DriveToken{RefreshToken: "refresh", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.DisconnectDrive("intruder"); !errors.Is(err, ErrDriveDisconnectOwner) {
		t.Fatalf("DisconnectDrive by another account returned %v", err)
	}
	if fault := DriveFaultOf(manager.DisconnectDrive("intruder")); fault != DriveFaultConflict {
		t.Fatalf("fault = %d, want conflict", fault)
	}
	if !manager.DriveTokens().Connected() {
		t.Fatal("a refused disconnect cleared the token anyway")
	}
	if err := manager.DisconnectDrive("owner"); err != nil {
		t.Fatalf("the connecting account could not disconnect: %v", err)
	}
	if manager.DriveTokens().Connected() {
		t.Fatal("the token survived its owner's disconnect")
	}
}

func TestConsentRefusesAnAccountThatDidNotConnectTheWorkspace(t *testing.T) {
	manager := driveAuthManager(t)
	if err := manager.DriveTokens().Save(DriveToken{RefreshToken: "refresh", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	admin := fixedActors{actor: identity.Actor{ID: "intruder", Role: identity.RoleAdmin}, revision: "rev-1", exists: true}
	_, err := manager.BeginDriveAuth(context.Background(), DriveAuthRequest{
		Actors:  admin,
		ActorID: "intruder",
		Host:    "nodevas.example",
	})
	if !errors.Is(err, ErrDriveOwnedByAnother) {
		t.Fatalf("BeginDriveAuth returned %v, want %v", err, ErrDriveOwnedByAnother)
	}
	if fault := DriveFaultOf(err); fault != DriveFaultConflict {
		t.Fatalf("fault = %d, want conflict", fault)
	}
}

func TestConsentRefusesANonAdministratorBeforeTheConsentScreen(t *testing.T) {
	manager := driveAuthManager(t)
	_, err := manager.BeginDriveAuth(context.Background(), DriveAuthRequest{
		Actors:  fixedActors{actor: identity.Actor{ID: "member", Role: identity.RoleMember}, revision: "rev-1", exists: true},
		ActorID: "member",
		Host:    "nodevas.example",
	})
	if !errors.Is(err, ErrDriveAdminRequired) {
		t.Fatalf("BeginDriveAuth returned %v, want %v", err, ErrDriveAdminRequired)
	}
	if fault := DriveFaultOf(err); fault != DriveFaultForbidden {
		t.Fatalf("fault = %d, want forbidden", fault)
	}
}

func TestConsentCarriesOnlyAKnownSourceIntoTheRedirect(t *testing.T) {
	manager := driveAuthManager(t)
	admin := fixedActors{actor: identity.Actor{ID: "owner", Role: identity.RoleAdmin}, revision: "rev-1", exists: true}
	for _, testCase := range []struct {
		source string
		want   string
	}{
		{source: "workspace", want: "https://nodevas.example/api/remote/drive/callback?source=workspace"},
		{source: " workspace ", want: "https://nodevas.example/api/remote/drive/callback?source=workspace"},
		{source: "", want: "https://nodevas.example/api/remote/drive/callback"},
		{source: "settings", want: "https://nodevas.example/api/remote/drive/callback"},
		{source: "https://evil.example/steal", want: "https://nodevas.example/api/remote/drive/callback"},
		{source: "workspace&redirect_uri=https://evil.example", want: "https://nodevas.example/api/remote/drive/callback"},
	} {
		consent, err := manager.BeginDriveAuth(context.Background(), DriveAuthRequest{
			Actors:  admin,
			ActorID: "owner",
			Source:  testCase.source,
			Host:    "nodevas.example",
			Secure:  true,
		})
		if err != nil {
			t.Fatalf("source %q: %v", testCase.source, err)
		}
		parsed, err := url.Parse(consent)
		if err != nil {
			t.Fatalf("source %q: %v", testCase.source, err)
		}
		if got := parsed.Query().Get("redirect_uri"); got != testCase.want {
			t.Errorf("source %q: redirect_uri = %q, want %q", testCase.source, got, testCase.want)
		}
		if testCase.want == "https://nodevas.example/api/remote/drive/callback" &&
			strings.Contains(consent, "evil.example") {
			t.Errorf("source %q leaked into the consent URL", testCase.source)
		}
	}
}

func TestALoopbackWorkspaceRecordsNoDriveOwner(t *testing.T) {
	manager := driveAuthManager(t)
	manager.pm.SetRemote(false)
	// The desktop server has one actor and no account directory, so neither the
	// admin check nor the ownership check may stand in its way.
	consent, err := manager.BeginDriveAuth(context.Background(), DriveAuthRequest{
		ActorID: "local",
		Host:    "127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("BeginDriveAuth on a loopback server: %v", err)
	}
	parsed, err := url.Parse(consent)
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := manager.ConsumeOAuthState(parsed.Query().Get("state"))
	if !ok {
		t.Fatal("the consent URL carried no state we issued")
	}
	if pending.ActorID != "" {
		t.Fatalf("a loopback consent recorded owner %q", pending.ActorID)
	}
}
