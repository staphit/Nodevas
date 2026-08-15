package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nodevas/internal/project"
	"nodevas/internal/store"
)

// Config records which backend a workspace backs up to. It lives in the
// app config directory, not the workspace: the workspace is the thing users
// hand to a colleague or sync to Drive, and a backup target — especially a
// host Path — is machine-local configuration, not project content. Secrets
// (the Drive OAuth client and token) live encrypted in separate files; this
// one carries nothing sensitive so the API can return it as-is.
type Config struct {
	// Kind selects the backend: "" (none), "folder", or "drive".
	Kind string `json:"kind"`
	// Folder is the directory the folder backend writes to.
	Folder string `json:"folder,omitempty"`
	// DriveFolderID scopes Drive operations to one parent folder; "" is the
	// account root.
	DriveFolderID string `json:"driveFolderId,omitempty"`
	// AutoBackup turns on the scheduled push loop.
	AutoBackup bool `json:"autoBackup,omitempty"`
	// IntervalHours is how often the scheduler pushes; <1 falls back to the
	// default.
	IntervalHours int `json:"intervalHours,omitempty"`
	// RetainBundles is how many of this app's own bundles survive a prune. 0
	// (or absent) means the default; a negative value disables pruning entirely
	// so nothing is ever deleted; anything between 1 and the floor is clamped
	// up. See retainCount and pruneBundles in backup.go.
	RetainBundles int `json:"retainBundles,omitempty"`
}

// RemoteManager owns the workspace's backup configuration and builds the active
// RemoteStore on demand. It is attached to the Server like the Notifier.
type RemoteManager struct {
	pm *project.ProjectManager

	mu      sync.Mutex
	states  map[string]oauthState // pending OAuth states, cleared on callback
	stateMu sync.Mutex            // serializes persisted sync manifest reads and writes

	// backupMu serializes scheduled, manual, and shutdown pushes. It is a
	// channel rather than a bare mutex so shutdown can stop waiting when its
	// bounded context expires.
	backupMu chan struct{}
}

func NewManager(pm *project.ProjectManager) *RemoteManager {
	return &RemoteManager{
		pm:       pm,
		states:   map[string]oauthState{},
		backupMu: make(chan struct{}, 1),
	}
}

const (
	// oauthStateTTL bounds how long an in-flight consent request stays valid.
	oauthStateTTL = 10 * time.Minute

	// Pending consent requests are untrusted, authenticated input. Keep both a
	// per-actor and process-wide ceiling so repeatedly opening the consent flow
	// cannot grow the state map until its TTL sweep runs.
	maxOAuthStatesPerActor = 8
	maxOAuthStatesTotal    = 256
)

var (
	ErrOAuthStateLimit = errors.New("too many pending OAuth requests")
	ErrFolderRemote    = errors.New("folder backup is unavailable on a remotely accessible server")
)

// oauthState is everything about an in-flight consent request that must stay on
// the server. The PKCE verifier is here rather than in the redirect because a
// verifier the browser has seen proves nothing; the actor is here because the
// callback arrives without a session cookie, so this entry is the only record
// of who asked for the flow.
type oauthState struct {
	Issued        time.Time
	Verifier      string
	ActorID       string
	ActorRevision string
}

// NewOAuthState mints a single-use state token tying the consent redirect back
// to the request that started it (CSRF protection for the OAuth flow), and the
// PKCE verifier that will be replayed at the token exchange. actorID may be
// empty on a loopback server, where there is only one possible actor.
func (m *RemoteManager) NewOAuthState(actorID string, actorRevision ...string) (state, verifier string, err error) {
	state, err = randomToken()
	if err != nil {
		return "", "", err
	}
	// A 256-bit URL-safe token is already a valid RFC 7636 code_verifier, so
	// the state and the verifier come from the same generator.
	verifier, err = randomToken()
	if err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepStatesLocked()
	if len(m.states) >= maxOAuthStatesTotal {
		return "", "", ErrOAuthStateLimit
	}
	actorStates := 0
	for _, entry := range m.states {
		if entry.ActorID == actorID {
			actorStates++
		}
	}
	if actorStates >= maxOAuthStatesPerActor {
		return "", "", ErrOAuthStateLimit
	}
	revision := ""
	if len(actorRevision) > 0 {
		revision = actorRevision[0]
	}
	m.states[state] = oauthState{
		Issued: time.Now(), Verifier: verifier, ActorID: actorID, ActorRevision: revision,
	}
	return state, verifier, nil
}

// ConsumeOAuthState reports whether state was one we issued and not yet used,
// removing it either way, and hands back what the callback needs to finish:
// the PKCE verifier and the actor that started the flow.
func (m *RemoteManager) ConsumeOAuthState(state string) (oauthState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepStatesLocked()
	entry, ok := m.states[state]
	delete(m.states, state)
	if !ok || time.Since(entry.Issued) > oauthStateTTL {
		return oauthState{}, false
	}
	return entry, true
}

func (m *RemoteManager) sweepStatesLocked() {
	for state, entry := range m.states {
		if time.Since(entry.Issued) > oauthStateTTL {
			delete(m.states, state)
		}
	}
}

// ConfigPath is the per-workspace config file in the app config directory,
// keyed by a hash of the workspace Path so two workspaces never clash.
func (m *RemoteManager) ConfigPath() string {
	sum := sha256.Sum256([]byte(filepath.Clean(m.pm.Workspace())))
	name := "remote-" + hex.EncodeToString(sum[:])[:16] + ".json"
	return filepath.Join(m.pm.CatalogRoot(), "secrets", name)
}

func (m *RemoteManager) loadLocked() Config {
	var cfg Config
	data, err := os.ReadFile(m.ConfigPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("remote: ignoring malformed %s: %v", m.ConfigPath(), err)
		return Config{}
	}
	return cfg
}

func (m *RemoteManager) Load() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked()
}

func (m *RemoteManager) saveLocked(cfg Config) error {
	Path := m.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := store.WriteMetaFile(Path, data); err != nil {
		return err
	}
	return os.Chmod(Path, 0o600)
}

func (m *RemoteManager) Save(cfg Config) error {
	if err := m.ValidateConfig(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(cfg)
}

// ValidateConfig applies backend policy at the storage boundary. HTTP handlers
// also reject host paths, but local configuration can survive a restart in
// remote mode; enforcing this again here prevents that configuration from ever
// becoming an active folder backend.
func (m *RemoteManager) ValidateConfig(cfg Config) error {
	if cfg.Kind == "folder" && m.pm != nil && m.pm.IsRemote() {
		return ErrFolderRemote
	}
	return nil
}

// DriveTokens is the encrypted OAuth token Store for this workspace.
func (m *RemoteManager) DriveTokens() *driveTokenStore {
	return NewDriveTokenStore(m.pm.CatalogRoot(), m.pm.Workspace())
}

// DriveCredentials stores the OAuth application credential outside the
// workspace. It is shared by workspaces in this app profile because the OAuth
// client identifies the Nodevas installation, not a project.
func (m *RemoteManager) DriveCredentials() *driveCredentialStore {
	return NewDriveCredentialStore(m.pm.CatalogRoot())
}

// DriveClientCreds returns the encrypted OAuth client configured for this app.
func (m *RemoteManager) DriveClientCreds() (oauthCreds, error) {
	stored, storedErr := m.DriveCredentials().Load()
	if storedErr == nil && stored.ClientID != "" && stored.ClientSecret != "" {
		return stored, nil
	}
	if storedErr != nil {
		return oauthCreds{}, storedErr
	}
	return oauthCreds{}, fmt.Errorf("Google Drive OAuth client credentials are not configured")
}

// DriveCredentialStatus reports only whether credentials exist and where they
// came from. It never returns the client secret to the browser.
func (m *RemoteManager) DriveCredentialStatus() (configured bool, source string) {
	stored, err := m.DriveCredentials().Load()
	if err == nil && stored.ClientID != "" && stored.ClientSecret != "" {
		return true, "app"
	}
	return false, "none"
}

func (m *RemoteManager) SaveDriveCredentials(clientID, clientSecret string) error {
	return m.DriveCredentials().Save(clientID, clientSecret)
}

func (m *RemoteManager) ClearDriveCredentials() error {
	return m.DriveCredentials().Clear()
}

// Store builds the RemoteStore for the current config, or nil when no backend
// is configured. A misconfigured backend returns an error so the endpoint can
// report it instead of silently doing nothing.
func (m *RemoteManager) Store() (RemoteStore, error) {
	cfg := m.Load()
	return m.BuildRemote(cfg)
}

func (m *RemoteManager) BuildRemote(cfg Config) (RemoteStore, error) {
	if err := m.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	switch cfg.Kind {
	case "":
		return nil, nil
	case "folder":
		Remote, err := NewFolderRemote(cfg.Folder)
		if err != nil {
			return nil, err
		}
		return Remote, nil
	case "drive":
		creds, err := m.DriveClientCreds()
		if err != nil {
			return nil, err
		}
		return newDriveRemote(creds, m.DriveTokens(), cfg.DriveFolderID), nil
	default:
		return nil, fmt.Errorf("unknown remote backend %q", cfg.Kind)
	}
}

// ListDriveFolders lists Drive folders without requiring the current backup
// config to already be set to Drive. This lets the picker browse folders before
// the user chooses and saves the final DriveFolderID.
func (m *RemoteManager) ListDriveFolders(ctx context.Context, parentID string) ([]DriveFolderRef, error) {
	creds, err := m.DriveClientCreds()
	if err != nil {
		return nil, err
	}
	return newDriveRemote(creds, m.DriveTokens(), "").ListFolders(ctx, parentID)
}

// ListDriveBundles lists Nodevas snapshots in a Drive folder without changing
// the current backup configuration. The connect UI uses this while the user
// is still browsing, before a folder is committed as the backup target.
func (m *RemoteManager) ListDriveBundles(ctx context.Context, folderID string) ([]RemoteRef, error) {
	creds, err := m.DriveClientCreds()
	if err != nil {
		return nil, err
	}
	return newDriveRemote(creds, m.DriveTokens(), folderID).ListBundles(ctx, folderID)
}

// randomToken returns a URL-safe token with 256 bits of entropy. Backup keeps
// its own copy rather than importing the auth package: an OAuth state token is
// not an identity, and storage should not depend on authentication.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
