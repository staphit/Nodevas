package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"nodevas/internal/project"
	"nodevas/internal/store"
)

// Config records which backend a workspace backs up to. It lives in the
// app config directory, not the workspace: the workspace is the thing users
// hand to a colleague or sync to a cloud drive, and a backup target — especially
// a host Path — is machine-local configuration, not project content. It
// carries nothing sensitive so the API can return it as-is.
type Config struct {
	// Kind selects the backend: "" (none) or "folder".
	Kind string `json:"kind"`
	// Folder is the directory the folder backend writes to.
	Folder string `json:"folder,omitempty"`
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

	mu      sync.Mutex // guards the config file
	stateMu sync.Mutex // serializes persisted sync manifest reads and writes

	// backupMu serializes scheduled, manual, and shutdown pushes. It is a
	// channel rather than a bare mutex so shutdown can stop waiting when its
	// bounded context expires.
	backupMu chan struct{}
}

func NewManager(pm *project.ProjectManager) *RemoteManager {
	return &RemoteManager{
		pm:       pm,
		backupMu: make(chan struct{}, 1),
	}
}

var ErrFolderRemote = errors.New("folder backup is unavailable on a remotely accessible server")

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
	if !knownKind(cfg.Kind) {
		// A config written by an older build can name a backend this one no
		// longer has (the retired Google Drive backend saved "drive"). Treat it
		// as "no remote configured" rather than failing every backup call; the
		// next save from the backup dialog overwrites it.
		log.Printf("remote: ignoring unsupported backend %q in %s", cfg.Kind, m.ConfigPath())
		return Config{}
	}
	return cfg
}

// knownKind reports whether kind names a backend BuildRemote can construct.
func knownKind(kind string) bool {
	return kind == "" || kind == "folder"
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
	default:
		return nil, fmt.Errorf("unknown remote backend %q", cfg.Kind)
	}
}
