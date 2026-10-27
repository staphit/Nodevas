package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/db"
)

// The standalone path end to end: serve with no database URL starts the
// workspace's embedded PostgreSQL, answers, and on --stop-on-stdin-close
// stops it cleanly (no postmaster left, no postmaster.pid).
func TestEmbeddedServeStartsAnswersAndStopsCleanly(t *testing.T) {
	binaries := realPGBinaries(t)
	env := isolatedEnv(t, "NODEVAS_PG_BINARIES="+binaries, db.URLEnv+"=", db.URLFileEnv+"=")
	root := t.TempDir()
	server := startServe(t, root, env, "--stop-on-stdin-close")
	server.ready(t, 2*time.Minute)
	pgHome := envValue(env, "NODEVAS_PG_HOME")
	pid := embeddedRun(t, pgHome)
	if !postmasterAlive(pid) {
		t.Fatalf("postmaster %d is not running while serve answers", pid)
	}
	if err := server.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := server.exit(t, time.Minute); code != 0 {
		t.Fatalf("exit code %d:\n%s", code, server.output.String())
	}
	if postmasterAlive(pid) {
		t.Fatalf("postmaster %d outlived serve", pid)
	}
	if matches, _ := filepath.Glob(filepath.Join(pgHome, "clusters", "*", "data", "postmaster.pid")); len(matches) != 0 {
		t.Fatalf("the cluster was not shut down cleanly: %v", matches)
	}
	if _, err := os.Stat(db.PathFor(root)); !os.IsNotExist(err) {
		t.Fatal("a SQLite file was created")
	}
}
