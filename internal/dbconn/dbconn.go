// Package dbconn decides which PostgreSQL a workspace uses: the server named
// by NODEVAS_DATABASE_URL (or _FILE), or the workspace's own embedded cluster
// (internal/embeddedpg), started on first use, shared by every Open in the
// process and stopped when the last one is released. A CLI command run beside
// a serve that already owns the cluster connects to it as a client instead.
//
// RED PHASE: compile-only stubs.
package dbconn

import (
	"errors"
	"os"
)

// RequireURLEnv refuses the embedded cluster (set in the container image,
// where the database is always an external server).
const RequireURLEnv = "NODEVAS_REQUIRE_DATABASE_URL"

// DatabaseIDFile is <workspace>/.vised/database-id: the random id that names
// the workspace's cluster, so moving the folder keeps its database.
const DatabaseIDFile = "database-id"

// ErrNotImplemented is what every stub returns.
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
	// Release drops this hold; the last release in the process stops a
	// cluster this process started.
	Release func() error
}

// Resolve is ResolveWith the process environment.
func Resolve(root string) (Resolved, error) {
	return ResolveWith(Options{Root: root, Lookup: os.LookupEnv})
}

// ResolveWith picks the database for opts.Root.
func ResolveWith(opts Options) (Resolved, error) { return Resolved{}, ErrNotImplemented }

// DatabaseID reads the workspace's database id, creating it on first use.
func DatabaseID(root string) (string, error) { return "", ErrNotImplemented }

// Detach gives a copied workspace a new, empty cluster: a new database id.
// The old cluster is left alone for the original workspace.
func Detach(root string) (string, error) { return "", ErrNotImplemented }
