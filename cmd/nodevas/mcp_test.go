package main

import (
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"nodevas/internal/runnerwire"
)

// A runner passes its run's proxy URL, which carries a secret, in a file:
// a command line is readable by every user on the machine.
func TestMCPServerFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-server")
	if err := os.WriteFile(path, []byte("http://127.0.0.1:41234/r/secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := func(args ...string) (string, error) {
		fs, f := newMCPFlags(flag.ContinueOnError)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return mcpServerURL(fs, f)
	}
	if got, err := parse("--server-file", path, "--project", "p"); err != nil || got != "http://127.0.0.1:41234/r/secret" {
		t.Errorf("--server-file: %q, %v", got, err)
	}
	if got, err := parse("--server", "http://127.0.0.1:5666"); err != nil || got != "http://127.0.0.1:5666" {
		t.Errorf("--server: %q, %v", got, err)
	}
	if _, err := parse("--server", "http://127.0.0.1:5666", "--server-file", path); err == nil {
		t.Error("--server with --server-file was taken")
	}
	if _, err := parse("--server-file", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing --server-file was taken")
	}
}

func TestMCPArgsForPrefersTheRunsCommandLine(t *testing.T) {
	own := []string{"--server", "http://127.0.0.1:5666", "--actor", "mcp:antigravity"}
	run := []string{"--server", "http://127.0.0.1:41234/r/secret", "--project", "p", "--agent-role", "worker",
		"--actor", "run:antigravity", "--session-name", "repo-antigravity-default-abc123", "--toolset", "run"}
	cases := []struct {
		name, env string
		want      []string
	}{
		{"unset", "", own},
		{"the run's", `["mcp","--server","http://127.0.0.1:41234/r/secret","--project","p","--agent-role","worker",` +
			`"--actor","run:antigravity","--session-name","repo-antigravity-default-abc123","--toolset","run"]`, run},
		{"no flags", `["mcp"]`, []string{}},
		{"not mcp", `["serve","--server","x"]`, own},
		{"not JSON", `--server x`, own},
		{"not strings", `["mcp", 1]`, own},
		{"an unknown flag", `["mcp","--frobnicate"]`, own},
		{"a stray argument", `["mcp","--server","x","extra"]`, own},
	}
	for _, tc := range cases {
		if got := mcpArgsFor(own, tc.env); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: mcpArgsFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Inside a global agent's run there is no board: `nodevas mcp` refuses,
// whichever config started it, when the run's environment says so or when it
// starts in the run's worktree (a CLI that clears its MCP servers'
// environment still starts them there).
func TestNoBoardReason(t *testing.T) {
	root := t.TempDir()
	mkdir := func(parts ...string) string {
		t.Helper()
		dir := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The repository's worktree admin dirs, one with the runner's marker.
	marked := mkdir("repo", ".git", "worktrees", "run-1")
	write(filepath.Join(marked, runnerwire.NoBoardMarker), "run-1")
	plainAdmin := mkdir("repo", ".git", "worktrees", "run-2")
	// A global run's worktree, a node run's, one whose .git is relative,
	// a plain checkout inside the marked one, and a folder with no git.
	global := mkdir("worktrees", "run-1")
	write(filepath.Join(global, ".git"), "gitdir: "+filepath.ToSlash(marked)+"\n")
	deep := mkdir("worktrees", "run-1", "src", "pkg")
	node := mkdir("worktrees", "run-2")
	write(filepath.Join(node, ".git"), "gitdir: "+plainAdmin+"\n")
	relative := mkdir("rel", "wt")
	write(filepath.Join(relative, ".git"), "gitdir: ../../repo/.git/worktrees/run-1\n")
	nested := mkdir("worktrees", "run-1", "vendor", "lib")
	mkdir("worktrees", "run-1", "vendor", "lib", ".git")
	plain := mkdir("repo")
	nowhere := mkdir("nothing", "here")

	none := func(string) string { return "" }
	set := func(name string) string {
		if name == runnerwire.NoBoardEnv {
			return "run-9"
		}
		return ""
	}
	cases := []struct {
		name   string
		getenv func(string) string
		wd     string
		refuse bool
	}{
		{"the run's environment", set, nowhere, true},
		{"the environment, set to spaces", func(name string) string {
			if name == runnerwire.NoBoardEnv {
				return "  "
			}
			return ""
		}, nowhere, false},
		{"a global run's worktree", none, global, true},
		{"deep in it", none, deep, true},
		{"a node run's worktree", none, node, false},
		{"a relative gitdir", none, relative, true},
		{"a checkout of its own inside one", none, nested, false},
		{"a plain checkout", none, plain, false},
		{"no git at all", none, nowhere, false},
		{"no working directory", none, "", false},
	}
	for _, tc := range cases {
		reason := noBoardReason(tc.getenv, tc.wd)
		if (reason != "") != tc.refuse {
			t.Errorf("%s: noBoardReason = %q, want refused %v", tc.name, reason, tc.refuse)
		}
	}
	if reason := noBoardReason(none, global); !strings.Contains(reason, filepath.Join(marked, runnerwire.NoBoardMarker)) {
		t.Errorf("the reason does not name the marker to remove: %q", reason)
	}
}

// The marker in a real worktree's git directory, where git itself says it is.
func TestNoBoardReasonInARealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "commit", "-q", "--allow-empty", "-m", "first")
	wt := filepath.Join(t.TempDir(), "run-x")
	run(repo, "worktree", "add", "-q", "-b", "agent/x-run", wt)
	none := func(string) string { return "" }
	if reason := noBoardReason(none, wt); reason != "" {
		t.Fatalf("an unmarked worktree refused: %q", reason)
	}
	gitDir := run(wt, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(gitDir, runnerwire.NoBoardMarker), []byte("run-x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := noBoardReason(none, wt); reason == "" {
		t.Fatal("a marked worktree served")
	}
	if reason := noBoardReason(none, repo); reason != "" {
		t.Fatalf("the main checkout refused: %q", reason)
	}
}

// mcpServeHelperEnv re-runs this test binary as `nodevas mcp`.
const mcpServeHelperEnv = "NODEVAS_MCP_SERVE_HELPER"

func TestMCPServeHelper(t *testing.T) {
	server := os.Getenv(mcpServeHelperEnv)
	if server == "" {
		return
	}
	mcpServe([]string{"--server", server})
	os.Exit(0)
}

// Refused before anything else: nothing on stdout (the JSON-RPC stream), a
// line on stderr, exit 2, and no request to the server, whatever the run's
// own command line (NODEVAS_RUN_MCP) says.
func TestMCPServeRefusesInsideAGlobalRun(t *testing.T) {
	var hits atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "no", http.StatusTeapot)
	}))
	defer hub.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPServeHelper$")
	cmd.Env = append(os.Environ(), mcpServeHelperEnv+"="+hub.URL, runnerwire.NoBoardEnv+"=run-1",
		runMCPEnv+`=["mcp","--server","`+hub.URL+`"]`)
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("exit = %v; stderr %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "global agent") || !strings.Contains(stderr.String(), runnerwire.NoBoardEnv) {
		t.Errorf("stderr = %q", stderr.String())
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the server got %d requests", n)
	}
}
