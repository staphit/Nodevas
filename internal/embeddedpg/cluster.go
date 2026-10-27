package embeddedpg

import (
	"context"
	"log/slog"
	"path/filepath"
	"syscall"
	"time"
)

// Options describes one workspace's cluster.
type Options struct {
	// Home is the embedded root; the cluster lives in Home/clusters/ID.
	Home string
	// ID is the workspace's database id (<workspace>/.vised/database-id).
	ID string
	// Binaries is an installed, verified binaries directory.
	Binaries string
	// Workspace is recorded in workspace.txt for an operator.
	Workspace string
	// Logger receives db.embedded_* events; nil is slog.Default.
	Logger *slog.Logger
	// ReadyTimeout bounds a start; zero is 60s for a first start, 30s after.
	ReadyTimeout time.Duration
}

// RunInfo is run.json: who runs the cluster and where.
type RunInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	Version   string `json:"version"`
	StartedAt string `json:"startedAt"`
}

// Cluster is a started (or startable) embedded server.
type Cluster struct {
	opts Options
}

// ClusterDir is where cluster id lives under home.
func ClusterDir(home, id string) string { return filepath.Join(home, "clusters", id) }

// New prepares a cluster; nothing happens until Start.
func New(opts Options) *Cluster { return &Cluster{opts: opts} }

// Start takes the owner lock, initdbs on first use, recovers a stale
// postmaster.pid, starts the postmaster on 127.0.0.1 and waits until the app
// role can run SELECT 1.
func (c *Cluster) Start(ctx context.Context) error { return ErrNotImplemented }

// Stop shuts the postmaster down fast and releases the owner lock.
func (c *Cluster) Stop(ctx context.Context) error { return ErrNotImplemented }

// URL is the app role's connection string (the non-superuser nodevas).
func (c *Cluster) URL() string { return "" }

// AdminURL is the cluster administrator's connection string.
func (c *Cluster) AdminURL() string { return "" }

// Port is the loopback port the postmaster listens on.
func (c *Cluster) Port() int { return 0 }

// PID is the running postmaster's process id.
func (c *Cluster) PID() int { return 0 }

// Dir is ClusterDir(Home, ID).
func (c *Cluster) Dir() string { return ClusterDir(c.opts.Home, c.opts.ID) }

// DataDir is the PostgreSQL data directory.
func (c *Cluster) DataDir() string { return filepath.Join(c.Dir(), "data") }

// Exited reports a postmaster supervision gave up on: it died more often than
// the restart budget allows. Serve cancels itself when it fires.
func (c *Cluster) Exited() <-chan error { return nil }

// abandon drops supervision and the owner lock without stopping the
// postmaster, leaving the orphan a crashed serve on macOS would leave. Tests
// only.
func (c *Cluster) abandon() {}

// Lock takes cluster id's owner lock without starting anything. It returns
// ErrClusterBusy when another holder has it.
func Lock(home, id string) (release func(), err error) { return nil, ErrNotImplemented }

// Discover finds a cluster another live process owns: its lock is held and
// run.json names a live pid. It returns the app URL to connect to.
func Discover(home, id string) (info RunInfo, url string, ok bool, err error) {
	return RunInfo{}, "", false, ErrNotImplemented
}

// Recovery is what RecoverStalePID did.
type Recovery string

const (
	// RecoveryNone: no postmaster.pid.
	RecoveryNone Recovery = "none"
	// RecoveryDeadPID: the pid is dead; PostgreSQL clears the file itself.
	RecoveryDeadPID Recovery = "dead-pid"
	// RecoveryUnrelatedPID: the pid is alive but is not our postmaster (pid
	// reuse); the file was removed under the owner lock.
	RecoveryUnrelatedPID Recovery = "unrelated-pid"
	// RecoveryOrphanStopped: our own orphaned postmaster was stopped.
	RecoveryOrphanStopped Recovery = "orphan-stopped"
)

// RecoverStalePID inspects dataDir/postmaster.pid before a start. The caller
// holds the owner lock.
func RecoverStalePID(ctx context.Context, binaries, dataDir string) (Recovery, error) {
	return "", ErrNotImplemented
}

// WriteSecretFile writes data readable by the current user only: mode 0600 on
// Unix, a protected DACL naming only the current user's SID on Windows.
func WriteSecretFile(path string, data []byte) error { return ErrNotImplemented }

// spawnAttrs is how the postmaster is started: its own process group (Setpgid
// on Unix, CREATE_NEW_PROCESS_GROUP|CREATE_NO_WINDOW on Windows), so a
// terminal Ctrl+C reaches serve and not PostgreSQL.
func spawnAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
