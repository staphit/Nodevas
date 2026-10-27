// Package dbconn decides which PostgreSQL a workspace uses: the server named
// by NODEVAS_DATABASE_URL (or _FILE), or the workspace's own embedded cluster
// (internal/embeddedpg), started on first use, shared by every Open in the
// process and stopped when the last one is released. A CLI command run beside
// a serve that already owns the cluster connects to it as a client instead.
package dbconn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"nodevas/internal/embeddedpg"
	"nodevas/internal/workspace"
)

// The environment variables that name an external server. They mirror
// db.URLEnv and db.URLFileEnv (this package sits below internal/db).
const (
	URLEnv     = "NODEVAS_DATABASE_URL"
	URLFileEnv = URLEnv + "_FILE"
)

// RequireURLEnv refuses the embedded cluster (set in the container image,
// where the database is always an external server).
const RequireURLEnv = "NODEVAS_REQUIRE_DATABASE_URL"

// DatabaseIDFile is <workspace>/.vised/database-id: the random id that names
// the workspace's cluster, so moving the folder keeps its database.
const DatabaseIDFile = "database-id"

// ErrNotImplemented is no longer returned; it is kept for the tests that
// assert a refusal is a real one and not a stub.
var ErrNotImplemented = errors.New("dbconn: not implemented")

// Options are Resolve's inputs, injectable for tests.
type Options struct {
	// Root is the workspace directory.
	Root string
	// Lookup reads the environment; nil is os.LookupEnv.
	Lookup func(string) (string, bool)
	// PGHome overrides the embedded root (NODEVAS_PG_HOME).
	PGHome string
	// Binaries overrides the binaries directory (NODEVAS_PG_BINARIES).
	Binaries string
	// Progress receives a first-run download's progress; nil is stderr.
	Progress io.Writer
}

// Resolved is a database to connect to and the hold on it.
type Resolved struct {
	// URL is the connection string. Never log it; see db.DescribeURL.
	URL string
	// Embedded is true for the workspace's own cluster.
	Embedded bool
	// Client is true when another process owns the embedded cluster.
	Client bool
	// DataDir is the embedded cluster's data directory, "" for external.
	DataDir string
	// Port is the embedded cluster's port, 0 for external.
	Port int
	// Exited fires when this process's embedded server died and could not be
	// restarted; nil otherwise.
	Exited <-chan error
	// Release drops this hold; the last release in the process stops a
	// cluster this process started.
	Release func() error
}

// URLFromEnv returns the configured external server, or "" for none.
// URLEnv wins over URLFileEnv.
func URLFromEnv(lookup func(string) (string, bool)) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if raw, _ := lookup(URLEnv); strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}
	path, _ := lookup(URLFileEnv)
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("%s: %w", URLFileEnv, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Resolve is ResolveWith the process environment.
func Resolve(root string) (Resolved, error) {
	return ResolveWith(Options{Root: root, Lookup: os.LookupEnv})
}

// holder is this process's one start of a cluster.
type holder struct {
	cluster *embeddedpg.Cluster
	refs    int
}

var (
	mu      sync.Mutex
	holders = map[string]*holder{}
)

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// ResolveWith picks the database for opts.Root.
func ResolveWith(opts Options) (Resolved, error) {
	lookup := opts.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	url, err := URLFromEnv(lookup)
	if err != nil {
		return Resolved{}, err
	}
	if url != "" {
		return Resolved{URL: url, Release: func() error { return nil }}, nil
	}
	if value, _ := lookup(RequireURLEnv); truthy(value) {
		return Resolved{}, fmt.Errorf("no database configured: set %s to a postgres:// URL or %s to a file holding one "+
			"(%s is set, so the embedded PostgreSQL is not used)", URLEnv, URLFileEnv, RequireURLEnv)
	}

	home := opts.PGHome
	if home == "" {
		if value, _ := lookup(embeddedpg.HomeEnv); strings.TrimSpace(value) != "" {
			home = strings.TrimSpace(value)
		} else if home, err = embeddedpg.DefaultHome(); err != nil {
			return Resolved{}, err
		}
	}
	if home, err = filepath.Abs(home); err != nil {
		return Resolved{}, err
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return Resolved{}, err
	}
	id, err := DatabaseID(root)
	if err != nil {
		return Resolved{}, err
	}
	key := home + "|" + id

	mu.Lock()
	defer mu.Unlock()
	if held := holders[key]; held != nil {
		held.refs++
		return resolvedFor(held, key), nil
	}

	// Another process may own the cluster: a serve this command runs beside,
	// or a copy of this workspace that carries the same id.
	deadline := time.Now().Add(90 * time.Second)
	for {
		state, info, url, err := embeddedpg.Probe(home, id)
		if err != nil {
			return Resolved{}, err
		}
		if state == embeddedpg.StateFree {
			break
		}
		if owner := embeddedpg.Workspace(home, id); owner != "" && !samePath(owner, root) {
			return Resolved{}, fmt.Errorf("%w: its database belongs to %s, which is open in another process; "+
				"if %s is a copy of it, run `nodevas db detach --project %q` to give the copy a database of its own",
				embeddedpg.ErrClusterBusy, owner, root, root)
		}
		if state == embeddedpg.StateRunning {
			return Resolved{URL: url, Embedded: true, Client: true,
				DataDir: filepath.Join(embeddedpg.ClusterDir(home, id), "data"), Port: info.Port,
				Release: func() error { return nil }}, nil
		}
		// Its owner is starting it; wait for the server to come up.
		if time.Now().After(deadline) {
			return Resolved{}, fmt.Errorf("%w: another process holds %s but no server came up",
				embeddedpg.ErrClusterBusy, embeddedpg.ClusterDir(home, id))
		}
		time.Sleep(250 * time.Millisecond)
	}

	binaries, err := binariesFor(opts, lookup, home)
	if err != nil {
		return Resolved{}, err
	}
	cluster := embeddedpg.New(embeddedpg.Options{Home: home, ID: id, Binaries: binaries, Workspace: root})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := cluster.Start(ctx); err != nil {
		if errors.Is(err, embeddedpg.ErrClusterBusy) {
			return Resolved{}, fmt.Errorf("%w; run `nodevas db detach --project %q` if this workspace is a copy of another", err, root)
		}
		return Resolved{}, err
	}
	held := &holder{cluster: cluster, refs: 1}
	holders[key] = held
	return resolvedFor(held, key), nil
}

func resolvedFor(held *holder, key string) Resolved {
	var once sync.Once
	return Resolved{
		URL: held.cluster.URL(), Embedded: true, DataDir: held.cluster.DataDir(), Port: held.cluster.Port(),
		Exited: held.cluster.Exited(),
		Release: func() error {
			var err error
			once.Do(func() { err = release(held, key) })
			return err
		},
	}
}

func release(held *holder, key string) error {
	mu.Lock()
	held.refs--
	last := held.refs <= 0
	if last {
		delete(holders, key)
	}
	mu.Unlock()
	if !last {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return held.cluster.Stop(ctx)
}

// binariesFor is a verified binaries directory: NODEVAS_PG_BINARIES (the
// desktop bundle) or the pinned download under home.
func binariesFor(opts Options, lookup func(string) (string, bool), home string) (string, error) {
	target, err := embeddedpg.CurrentTarget()
	if err != nil {
		return "", err
	}
	dir := opts.Binaries
	if dir == "" {
		if value, _ := lookup(embeddedpg.BinariesEnv); strings.TrimSpace(value) != "" {
			dir = strings.TrimSpace(value)
		}
	}
	if dir != "" {
		if err := embeddedpg.VerifyBinaries(dir, target); err != nil {
			return "", fmt.Errorf("%s: %w", embeddedpg.BinariesEnv, err)
		}
		return dir, nil
	}
	offline, _ := lookup(embeddedpg.OfflineEnv)
	mirror, _ := lookup(embeddedpg.MirrorEnv)
	progress := opts.Progress
	if progress == nil {
		progress = os.Stderr
	}
	return embeddedpg.Install(context.Background(), embeddedpg.InstallOptions{
		Home: home, Target: target, Offline: truthy(offline), Mirror: strings.TrimSpace(mirror), Progress: progress,
	})
}

var idShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

func idPath(root string) string {
	return filepath.Join(root, workspace.DataDir, DatabaseIDFile)
}

// DatabaseID reads the workspace's database id, creating it on first use.
func DatabaseID(root string) (string, error) {
	path := idPath(root)
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if idShape.MatchString(id) {
			return id, nil
		}
		return "", fmt.Errorf("%s does not hold a database id; delete it to start a new database", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	id := newID()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return DatabaseID(root) // another process made it first
	}
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(id + "\n")
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", err
	}
	return id, nil
}

// Detach gives a copied workspace a new, empty cluster: a new database id.
// The old cluster is left alone for the original workspace.
func Detach(root string) (string, error) {
	path := idPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	id := newID()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return id, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return p
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(clean(a), clean(b))
	}
	return clean(a) == clean(b)
}
