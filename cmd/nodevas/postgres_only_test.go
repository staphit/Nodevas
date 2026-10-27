package main

// Serve and the db subcommand on PostgreSQL only: the real binary, built once
// per test run, against internal/testpg (or the embedded cluster, for the
// tests that need the real pinned binaries).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
	"nodevas/internal/workspace"
)

var (
	binaryOnce sync.Once
	binaryDir  string
	binaryPath string
	binaryErr  error
)

func TestMain(m *testing.M) {
	code := testpg.Main(m)
	if binaryDir != "" {
		_ = os.RemoveAll(binaryDir)
	}
	os.Exit(code)
}

// nodevasBinary builds this command once per test run.
func nodevasBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		binaryDir, binaryErr = os.MkdirTemp("", "nodevas-cmd-test-")
		if binaryErr != nil {
			return
		}
		binaryPath = filepath.Join(binaryDir, "nodevas")
		if runtime.GOOS == "windows" {
			binaryPath += ".exe"
		}
		out, err := exec.Command("go", "build", "-tags", "nomsgpack", "-o", binaryPath, ".").CombinedOutput()
		if err != nil {
			binaryErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binaryErr != nil {
		t.Fatal(binaryErr)
	}
	return binaryPath
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// isolatedEnv keeps the child off the developer's real config, workspace
// catalog and PostgreSQL home.
func isolatedEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	config := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "NODEVAS_PG_HOME":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "APPDATA="+config, "LOCALAPPDATA="+config, "XDG_CONFIG_HOME="+config,
		"HOME="+config, "USERPROFILE="+config, "NODEVAS_PG_HOME="+filepath.Join(config, "pg"))
	return append(env, extra...)
}

type serveProcess struct {
	cmd    *exec.Cmd
	output *syncBuffer
	stdin  io.WriteCloser
	base   string
	done   chan error
}

func startServe(t *testing.T, root string, env []string, args ...string) *serveProcess {
	t.Helper()
	port := freePort(t)
	all := append([]string{"serve", "--project", root, "--port", fmt.Sprint(port), "--no-runner"}, args...)
	cmd := exec.Command(nodevasBinary(t), all...)
	cmd.Env = env
	output := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &serveProcess{cmd: cmd, output: output, stdin: stdin, base: fmt.Sprintf("http://127.0.0.1:%d", port), done: make(chan error, 1)}
	go func() { process.done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-process.done:
		case <-time.After(10 * time.Second):
		}
	})
	return process
}

// ready waits for the server to answer, or for it to exit.
func (p *serveProcess) ready(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			p.done <- err
			t.Fatalf("serve exited (%v) before answering:\n%s", err, p.output.String())
		default:
		}
		response, err := http.Get(p.base + "/api/projects")
		if err == nil {
			response.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("serve did not answer within %s:\n%s", timeout, p.output.String())
}

// exit waits for the process to end and returns its exit code.
func (p *serveProcess) exit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case err := <-p.done:
		p.done <- err
		if p.cmd.ProcessState == nil {
			t.Fatalf("no process state: %v", err)
		}
		return p.cmd.ProcessState.ExitCode()
	case <-time.After(timeout):
		t.Fatalf("serve still running after %s:\n%s", timeout, p.output.String())
	}
	return -1
}

func legacyFixture(t *testing.T, root string) {
	t.Helper()
	source := filepath.Join("..", "..", "internal", "legacysqlite", "testdata", "legacy", "nodevas-0025.db")
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, workspace.DataDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(db.PathFor(root)+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(t *testing.T, url, table string) int {
	t.Helper()
	handle, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	var n int
	if err := handle.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// 62.
func TestServeImportsLegacyDBBeforeServing(t *testing.T) {
	url := testpg.UseEnv(t)
	root := t.TempDir()
	legacyFixture(t, root)
	server := startServe(t, root, isolatedEnv(t, db.URLEnv+"="+url, db.WaitEnv+"=0s"))
	server.ready(t, 90*time.Second)
	// The fixture holds two accounts and a setting that lives only in its WAL.
	if n := countRows(t, url, "accounts"); n != 2 {
		t.Fatalf("accounts after serve started = %d, want the 2 imported:\n%s", n, server.output.String())
	}
	if n := countRows(t, url, "legacy_imports"); n != 1 {
		t.Fatalf("legacy_imports = %d", n)
	}
	if _, err := os.Stat(db.PathFor(root) + ".imported"); err != nil {
		t.Fatalf("the old file was not renamed: %v", err)
	}
}

func garbageDB(t *testing.T, root string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Join(root, workspace.DataDir), 0o700)
	if err := os.WriteFile(db.PathFor(root), bytes.Repeat([]byte("not a database "), 512), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 63.
func TestServeFailsClosedOnImportError(t *testing.T) {
	url := testpg.UseEnv(t)
	root := t.TempDir()
	garbageDB(t, root)
	server := startServe(t, root, isolatedEnv(t, db.URLEnv+"="+url, db.WaitEnv+"=0s"))
	if code := server.exit(t, 60*time.Second); code == 0 {
		t.Fatal("serve exited 0 with an unimportable database")
	}
	out := server.output.String()
	for _, want := range []string{"NODEVAS_SKIP_SQLITE_IMPORT=1", "nodevas db import-sqlite --report"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not mention %q:\n%s", want, out)
		}
	}
	if n := countRows(t, url, "accounts"); n != 0 {
		t.Fatalf("a failed import left %d accounts", n)
	}
}

func TestSkipImportEnv(t *testing.T) {
	url := testpg.UseEnv(t)
	root := t.TempDir()
	garbageDB(t, root)
	server := startServe(t, root, isolatedEnv(t, db.URLEnv+"="+url, db.WaitEnv+"=0s", "NODEVAS_SKIP_SQLITE_IMPORT=1"))
	server.ready(t, 90*time.Second)
	if _, err := os.Stat(db.PathFor(root)); err != nil {
		t.Fatalf("a skipped import must leave the file alone: %v", err)
	}
}

// 64.
func TestMainBuildDoesNotLinkSQLite(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "nosqliteimport,nomsgpack", "nodevas/cmd/nodevas").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -tags nosqliteimport: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "modernc.org/sqlite") {
		t.Error("the nosqliteimport build still links modernc.org/sqlite")
	}
	out, err = exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "nodevas/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		importer, _, _ := strings.Cut(line, " ")
		if strings.Contains(line, "modernc.org/sqlite") && importer != "nodevas/internal/legacysqlite" {
			t.Errorf("%s imports modernc.org/sqlite; only internal/legacysqlite may", importer)
		}
	}
}

// 65. The desktop app and nodevas-dev stop serve by closing its stdin: on
// Windows there is no SIGTERM, and a kill skips the database's shutdown.
func TestStopOnStdinCloseStopsGracefully(t *testing.T) {
	url := testpg.UseEnv(t)
	root := t.TempDir()
	server := startServe(t, root, isolatedEnv(t, db.URLEnv+"="+url, db.WaitEnv+"=0s"), "--stop-on-stdin-close")
	server.ready(t, 90*time.Second)
	if err := server.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := server.exit(t, 40*time.Second); code != 0 {
		t.Fatalf("exit code %d after stdin closed:\n%s", code, server.output.String())
	}
}

// 67.
func TestRequireDatabaseURLMessage(t *testing.T) {
	root := t.TempDir()
	server := startServe(t, root, isolatedEnv(t, "NODEVAS_REQUIRE_DATABASE_URL=1", db.URLEnv+"=", db.URLFileEnv+"="))
	if code := server.exit(t, 30*time.Second); code == 0 {
		t.Fatal("serve exited 0 without a database")
	}
	out := server.output.String()
	for _, want := range []string{"NODEVAS_DATABASE_URL", "NODEVAS_DATABASE_URL_FILE"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %s:\n%s", want, out)
		}
	}
	if _, err := os.Stat(db.PathFor(root)); !os.IsNotExist(err) {
		t.Fatal("a SQLite file was created")
	}
}

func runNodevas(t *testing.T, env []string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(nodevasBinary(t), args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	timer := time.AfterFunc(2*time.Minute, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("%v: %v", args, err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// 68.
func TestDBStatusEmbeddedAndExternal(t *testing.T) {
	t.Run("external", func(t *testing.T) {
		url := testpg.UseEnv(t)
		out, code := runNodevas(t, isolatedEnv(t, db.URLEnv+"="+url), "", "db", "status", "--project", plainDir(t))
		if code != 0 {
			t.Fatalf("db status exited %d:\n%s", code, out)
		}
		host := strings.SplitN(strings.SplitN(url, "@", 2)[1], "?", 2)[0]
		for _, want := range []string{"mode: external", "server: postgres://" + host} {
			if !strings.Contains(out, want) {
				t.Errorf("status lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(strings.ToLower(out), "sqlite") || strings.Contains(out, "search index:") {
			t.Fatalf("status still describes a SQLite file:\n%s", out)
		}
		if strings.Contains(out, "test@") || strings.Contains(out, ":test") {
			t.Fatalf("status prints the password:\n%s", out)
		}
	})
	t.Run("embedded", func(t *testing.T) {
		binaries := realPGBinaries(t)
		out, code := runNodevas(t, isolatedEnv(t, "NODEVAS_PG_BINARIES="+binaries, db.URLEnv+"="), "",
			"db", "status", "--project", plainDir(t))
		if code != 0 {
			t.Fatalf("db status exited %d:\n%s", code, out)
		}
		for _, want := range []string{"mode: embedded", "data dir: ", "port: ", "version: 17.11.1"} {
			if !strings.Contains(out, want) {
				t.Errorf("status lacks %q:\n%s", want, out)
			}
		}
	})
}

// plainDir is a temporary directory whose path does not carry the test's
// name, so an output assertion cannot match the path by accident.
func plainDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func realPGBinaries(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("NODEVAS_PG_BINARIES")
	if dir == "" {
		if os.Getenv("NODEVAS_PG_REAL") == "1" {
			t.Fatal("NODEVAS_PG_BINARIES must name the pinned binaries in this job")
		}
		t.Skip("set NODEVAS_PG_BINARIES to run against the real embedded PostgreSQL")
	}
	return dir
}

func embeddedRun(t *testing.T, pgHome string) (pid int) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(pgHome, "clusters", "*", "run.json"))
	if len(matches) != 1 {
		t.Fatalf("run.json files: %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	return info.PID
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && key == name {
			return value
		}
	}
	return ""
}

// 66. log.Fatalf skips defers; every fatal after the cluster starts must stop
// it first.
func TestFatalAfterClusterStartStopsCluster(t *testing.T) {
	binaries := realPGBinaries(t)
	env := isolatedEnv(t, "NODEVAS_PG_BINARIES="+binaries, db.URLEnv+"=")
	root := t.TempDir()
	server := startServe(t, root, env, "--trusted-proxy", "not-an-address")
	if code := server.exit(t, 2*time.Minute); code == 0 {
		t.Fatal("serve accepted a bad --trusted-proxy")
	}
	pgHome := envValue(env, "NODEVAS_PG_HOME")
	pid := embeddedRun(t, pgHome)
	deadline := time.Now().Add(10 * time.Second)
	for postmasterAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("postmaster %d outlived a fatal serve", pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
	matches, _ := filepath.Glob(filepath.Join(pgHome, "clusters", "*", "data", "postmaster.pid"))
	if len(matches) != 0 {
		t.Fatalf("the cluster was killed, not stopped: %v", matches)
	}
}

// 69. A CLI command beside a running serve connects to its cluster instead of
// starting a second one.
func TestUserAddAttachesToRunningServeCluster(t *testing.T) {
	binaries := realPGBinaries(t)
	env := isolatedEnv(t, "NODEVAS_PG_BINARIES="+binaries, db.URLEnv+"=")
	root := t.TempDir()
	server := startServe(t, root, env)
	server.ready(t, 2*time.Minute)
	out, code := runNodevas(t, env, "correct horse battery staple\n",
		"user", "add", "--project", root, "--user", "alice", "--role", "admin", "--password-stdin")
	if code != 0 {
		t.Fatalf("user add exited %d:\n%s", code, out)
	}
	out, code = runNodevas(t, env, "", "user", "list", "--project", root)
	if code != 0 || !strings.Contains(out, "alice") {
		t.Fatalf("user list (%d):\n%s", code, out)
	}
	matches, _ := filepath.Glob(filepath.Join(envValue(env, "NODEVAS_PG_HOME"), "clusters", "*"))
	if len(matches) != 1 {
		t.Fatalf("clusters: %v; the CLI started its own", matches)
	}
}

// 70.
func TestCopyToPostgresRemoved(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = writer
	usage()
	os.Stderr = saved
	writer.Close()
	text, _ := io.ReadAll(reader)
	out := string(text)
	for _, gone := range []string{"copy-to-postgres", "falls back to SQLite"} {
		if strings.Contains(out, gone) {
			t.Errorf("usage still mentions %q", gone)
		}
	}
	for _, want := range []string{"import-sqlite", "install-postgres", "dump", "--stop-on-stdin-close"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	code := 0
	if out, code = runNodevas(t, isolatedEnv(t), "", "db", "copy-to-postgres", "--project", t.TempDir()); code != 2 {
		t.Errorf("db copy-to-postgres exited %d, want the usage exit 2:\n%s", code, out)
	}
}
