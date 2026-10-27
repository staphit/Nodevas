package embeddedpg

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
)

// StampFile is written into an installed binaries directory: the version,
// target and archive hash it was extracted from.
const StampFile = "nodevas-pg.json"

// Environment knobs.
const (
	HomeEnv     = "NODEVAS_PG_HOME"
	BinariesEnv = "NODEVAS_PG_BINARIES"
	OfflineEnv  = "NODEVAS_PG_OFFLINE"
	MirrorEnv   = "NODEVAS_PG_MIRROR"
)

// InstallOptions says where and how to install a target's binaries.
type InstallOptions struct {
	// Home is the embedded root (NODEVAS_PG_HOME).
	Home string
	// Target is the archive to install.
	Target Target
	// Mirror replaces the URLs' base; the hash is still enforced.
	Mirror string
	// Offline forbids downloading.
	Offline bool
	// Archive installs from a local file instead of downloading.
	Archive string
	// Client fetches the archive; nil is http.DefaultClient.
	Client *http.Client
	// Progress receives the human progress line; nil is silent.
	Progress io.Writer
}

// DefaultHome is NODEVAS_PG_HOME or the per-OS default: %LOCALAPPDATA%\nodevas\pg
// on Windows, ~/Library/Application Support/nodevas/pg on macOS,
// $XDG_CONFIG_HOME/nodevas/pg elsewhere.
func DefaultHome() (string, error) { return "", ErrNotImplemented }

// DistDir is where a target's binaries are installed under home.
func DistDir(home string, target Target) string {
	return filepath.Join(home, "dist", PinnedVersion+"-"+target.Name)
}

// Install makes sure the target's verified binaries are under opts.Home and
// returns their directory. A second call finds them and downloads nothing.
func Install(ctx context.Context, opts InstallOptions) (string, error) {
	return "", ErrNotImplemented
}

// VerifyBinaries checks a preinstalled binaries directory (NODEVAS_PG_BINARIES,
// a desktop bundle) against the manifest: its stamp must name PinnedVersion,
// the target and the target's SHA-256, and bin/postgres must exist.
func VerifyBinaries(dir string, target Target) error { return ErrNotImplemented }

// OfflineError explains a download that could not happen and what to do
// instead: retry online, fetch the URL elsewhere and install it with
// `nodevas db install-postgres --archive`, or set NODEVAS_DATABASE_URL.
func OfflineError(target Target, cause error) error { return ErrNotImplemented }
