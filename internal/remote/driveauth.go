package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nodevas/internal/identity"
)

// The Drive consent flow is the one place where a shared workspace takes hold
// of somebody's personal cloud account, so the rules about it — who may start
// it, whose token the result becomes, and who may take it away — live here
// rather than in the HTTP handler that happens to expose them. The handler
// decodes a request and renders a status; everything that can be got wrong is
// below, where a test can reach it without a router.

// ActorDirectory answers who an account is right now. The consent flow needs
// that because Google's redirect can land long after the request that started
// it, by which time the actor may have been demoted, removed, or had their
// credentials changed under them.
type ActorDirectory interface {
	CurrentActor(ctx context.Context, id string) (identity.Actor, string, bool)
}

// DriveFault is the class of a consent failure. It exists so a transport can
// choose a status code without restating the rule that produced the failure.
type DriveFault int

const (
	DriveFaultInternal DriveFault = iota
	DriveFaultRequest
	DriveFaultForbidden
	DriveFaultConflict
	DriveFaultUpstream
)

// DriveAuthFailure pairs a consent failure with its class.
type DriveAuthFailure struct {
	Fault DriveFault
	Err   error
}

func (e *DriveAuthFailure) Error() string { return e.Err.Error() }
func (e *DriveAuthFailure) Unwrap() error { return e.Err }

// DriveFaultOf classifies err. An error nobody labelled counts as internal,
// because an unclassified failure is a bug here rather than a caller mistake.
func DriveFaultOf(err error) DriveFault {
	var failure *DriveAuthFailure
	if errors.As(err, &failure) {
		return failure.Fault
	}
	return DriveFaultInternal
}

func driveFail(fault DriveFault, err error) error {
	return &DriveAuthFailure{Fault: fault, Err: err}
}

var (
	ErrDriveAdminRequired = errors.New("administrator access required")
	ErrDriveOAuthCallback = errors.New("invalid OAuth callback")
	ErrDriveOAuthExpired  = errors.New("OAuth authorization expired; sign in and try again")
	// ErrDriveOwnedByAnother and ErrDriveDisconnectOwner are the same rule seen
	// from two sides, and stay two messages because only one of them can offer
	// the way out ("ask that account to disconnect").
	ErrDriveOwnedByAnother  = errors.New("此工作區的 Google Drive 連線屬於其他帳號，請先由該帳號中斷連線")
	ErrDriveDisconnectOwner = errors.New("此工作區的 Google Drive 連線屬於其他帳號")
	ErrDriveNoRefreshToken  = errors.New("Google returned no refresh token; remove Nodevas at " +
		"myaccount.google.com/permissions and connect again")
)

// DriveAuthRequest is a request to open the consent screen: who is asking, and
// how their browser reached this server.
type DriveAuthRequest struct {
	// Actors may be nil when the deployment cannot look an account up, which on
	// a networked server is itself a refusal.
	Actors  ActorDirectory
	ActorID string
	// Source is the raw UI entry point from the query string.
	Source string
	Host   string
	Secure bool
}

// DriveCallbackRequest is Google's redirect back. It carries no identity of its
// own — see CompleteDriveAuth.
type DriveCallbackRequest struct {
	Actors ActorDirectory
	// Error is Google's own refusal, passed through so the user sees it.
	Error  string
	Code   string
	State  string
	Source string
	Host   string
	Secure bool
	// Client redeems the code. Tests point it at an httptest server; a zero
	// value gets a bounded default.
	Client *http.Client
}

// networked reports whether more than one account can reach this server. A
// loopback server has a single actor, so the ownership rules below all collapse
// to "yes" there.
func (m *RemoteManager) networked() bool {
	return m.pm != nil && m.pm.IsRemote()
}

// driveOAuthSource keeps the post-consent UI context in the redirect URI. Only
// the known UI entry point survives; anything else is dropped rather than
// echoed, because it ends up in a URL the browser is told to follow.
func driveOAuthSource(raw string) string {
	if strings.TrimSpace(raw) == "workspace" {
		return "workspace"
	}
	return ""
}

// driveRedirectURI is the callback Google returns to. It is derived from the
// request so a loopback dev server and a deployed hostname both work; it must
// match a redirect URI registered on the OAuth client.
func driveRedirectURI(host string, secure bool, source string) string {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	redirect := scheme + "://" + host + "/api/remote/drive/callback"
	if source == "workspace" {
		redirect += "?source=workspace"
	}
	return redirect
}

// driveOAuthActor is the identity recorded in the pending OAuth state. It is
// only meaningful on a networked server: a loopback server has a single actor,
// so recording one there would put an owner on desktop tokens that nothing
// checks and that a later account-enabled run would have to explain.
func (m *RemoteManager) driveOAuthActor(actorID string) string {
	if !m.networked() {
		return ""
	}
	return actorID
}

// driveOAuthIdentity returns the actor to bind a pending state to, plus the
// revision of that account as it stands now. An empty revision means the actor
// could not be confirmed as an administrator, which the callers treat as a
// refusal on a networked server.
func (m *RemoteManager) driveOAuthIdentity(
	ctx context.Context, actors ActorDirectory, actorID string,
) (string, string) {
	actorID = m.driveOAuthActor(actorID)
	if actorID == "" {
		return "", ""
	}
	if actors != nil {
		actor, revision, exists := actors.CurrentActor(ctx, actorID)
		if exists && actor.IsAdmin() {
			return actorID, revision
		}
	}
	return actorID, ""
}

// DriveOwnedByAnother reports whether this workspace's Drive link belongs to a
// different account. The token file is keyed by workspace alone, so without
// this check any account on a networked server could re-point — or unlink — a
// workspace another account connected.
func (m *RemoteManager) DriveOwnedByAnother(actorID string) bool {
	actorID = m.driveOAuthActor(actorID)
	if !m.networked() {
		return false
	}
	existing, err := m.DriveTokens().Load()
	if err != nil {
		// An unreadable token is not evidence of an owner; reconnecting is the
		// only way out of it.
		return false
	}
	return existing.RefreshToken != "" && existing.OwnerID != "" && existing.OwnerID != actorID
}

// BeginDriveAuth authorizes a consent request and returns the Google URL the
// browser must be sent to. The state token it mints ties the callback back to
// this request and carries the PKCE verifier and the requesting actor that the
// callback — which has neither — will need.
func (m *RemoteManager) BeginDriveAuth(ctx context.Context, req DriveAuthRequest) (string, error) {
	creds, err := m.DriveClientCreds()
	if err != nil {
		return "", driveFail(DriveFaultRequest, err)
	}
	actorID, actorRevision := m.driveOAuthIdentity(ctx, req.Actors, req.ActorID)
	if m.networked() && actorRevision == "" {
		return "", driveFail(DriveFaultForbidden, ErrDriveAdminRequired)
	}
	// Refuse before the consent screen rather than after it: the save would
	// fail anyway, and a user should not hand Google consent for nothing.
	if m.DriveOwnedByAnother(actorID) {
		return "", driveFail(DriveFaultConflict, ErrDriveOwnedByAnother)
	}
	state, verifier, err := m.NewOAuthState(actorID, actorRevision)
	if err != nil {
		return "", driveFail(DriveFaultInternal, err)
	}
	source := driveOAuthSource(req.Source)
	return creds.AuthCodeURL(driveRedirectURI(req.Host, req.Secure, source), state, verifier), nil
}

// CompleteDriveAuth finishes the flow: it exchanges the code for a token,
// stores it encrypted, and returns where the browser should land.
//
// The request that gets here carries no identity of its own — a cross-site
// redirect from Google does not send the SameSite=Strict session cookie — so
// the single-use state token is what authenticates the exchange, and the state
// entry is the only record of who the resulting token belongs to. That is why
// the account behind it is re-checked here rather than taken on trust: the
// entry was written minutes ago by a request nobody can replay.
func (m *RemoteManager) CompleteDriveAuth(ctx context.Context, req DriveCallbackRequest) (string, error) {
	if req.Error != "" {
		return "", driveFail(DriveFaultRequest,
			fmt.Errorf("Google authorization failed: %s", req.Error))
	}
	pending, ok := m.ConsumeOAuthState(req.State)
	if req.Code == "" || !ok {
		return "", driveFail(DriveFaultRequest, ErrDriveOAuthCallback)
	}
	// On a networked server an entry that cannot name an actor is refused
	// outright: there is nobody to record as the token's owner.
	if m.networked() && pending.ActorID == "" {
		return "", driveFail(DriveFaultRequest, ErrDriveOAuthCallback)
	}
	if m.networked() {
		actor, revision, exists := identity.Actor{}, "", false
		if req.Actors != nil {
			actor, revision, exists = req.Actors.CurrentActor(ctx, pending.ActorID)
		}
		if !exists || !actor.IsAdmin() || pending.ActorRevision == "" ||
			revision != pending.ActorRevision {
			return "", driveFail(DriveFaultForbidden, ErrDriveOAuthExpired)
		}
	}
	if m.DriveOwnedByAnother(pending.ActorID) {
		return "", driveFail(DriveFaultConflict, ErrDriveOwnedByAnother)
	}
	source := driveOAuthSource(req.Source)
	creds, err := m.DriveClientCreds()
	if err != nil {
		return "", driveFail(DriveFaultRequest, err)
	}
	client := req.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	tok, err := ExchangeAuthCode(
		ctx, client, creds, req.Code,
		driveRedirectURI(req.Host, req.Secure, source), pending.Verifier,
	)
	if err != nil {
		return "", driveFail(DriveFaultUpstream, err)
	}
	if tok.RefreshToken == "" {
		return "", driveFail(DriveFaultUpstream, ErrDriveNoRefreshToken)
	}
	tok.OwnerID = pending.ActorID
	if err := m.DriveTokens().Save(tok); err != nil {
		return "", driveFail(DriveFaultInternal, err)
	}
	destination := "/?drive=connected"
	if source == "workspace" {
		destination += "&source=workspace"
	}
	return destination, nil
}

// DisconnectDrive forgets the stored token. Only the account that connected may
// do so: otherwise disconnect-then-connect would be a way around the ownership
// check on the callback.
func (m *RemoteManager) DisconnectDrive(actorID string) error {
	if m.DriveOwnedByAnother(actorID) {
		return driveFail(DriveFaultConflict, ErrDriveDisconnectOwner)
	}
	if err := m.DriveTokens().Clear(); err != nil {
		return driveFail(DriveFaultInternal, err)
	}
	return nil
}
