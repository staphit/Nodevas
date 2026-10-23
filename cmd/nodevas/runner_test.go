package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

const cliTestToken = runnerwire.TokenPrefix + "abcdefghijklmnopqrstuvwxyz012345"

type stubCommands struct{}

func (stubCommands) LookPath(name string) (string, error) {
	if name == "claude" {
		return "/bin/claude", nil
	}
	return "", exec.ErrNotFound
}

func (stubCommands) Run(context.Context, string, ...string) (string, error) {
	return "9.9.9 (Claude Code)\n", nil
}

func runRunner(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runnerCmd(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunnerLoginReadsSecretsFromStdin(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "runner.json")

	code, _, stderr := runRunner(t, cliTestToken+"\naccess-secret\n",
		"login", "--hub", "https://hub.example/", "--access-client-id", "cid.access", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("login exit %d: %s", code, stderr)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hub != "https://hub.example" || cfg.Token != cliTestToken ||
		cfg.AccessClientID != "cid.access" || cfg.AccessClientSecret != "access-secret" {
		t.Fatalf("config = %+v", cfg)
	}

	// A token without a trailing newline (echo -n, a paste) still counts.
	code, _, stderr = runRunner(t, cliTestToken, "login", "--hub", "http://127.0.0.1:5666", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("login without newline exit %d: %s", code, stderr)
	}
	if cfg, _ = runner.LoadConfig(cfgPath); cfg.AccessClientID != "" || cfg.AccessClientSecret != "" {
		t.Fatalf("login without --access-client-id kept the old service token: %+v", cfg)
	}
}

func TestRunnerLoginRefusesBadInput(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	cases := []struct {
		stdin string
		args  []string
	}{
		{"not-a-token\n", []string{"--hub", "https://hub.example"}},
		{cliTestToken + "\n", []string{"--hub", "http://hub.example"}}, // http off loopback
		{cliTestToken + "\n", []string{"--hub", "https://hub.example", "--access-client-id", "cid"}},
		{"", []string{"--hub", "https://hub.example"}},
	}
	for _, tc := range cases {
		args := append([]string{"login", "--config", cfgPath}, tc.args...)
		if code, _, _ := runRunner(t, tc.stdin, args...); code == 0 {
			t.Errorf("login %v with stdin %q succeeded", tc.args, tc.stdin)
		}
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("a refused login wrote the config")
	}
}

func TestRunnerStatusNeverPrintsSecrets(t *testing.T) {
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })

	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	if code, _, stderr := runRunner(t, cliTestToken+"\nsuper-secret-value\n",
		"login", "--hub", "https://hub.example", "--access-client-id", "cid", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	code, stdout, stderr := runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, cliTestToken) || strings.Contains(stdout, "super-secret-value") ||
		strings.Contains(stdout, strings.TrimPrefix(cliTestToken, runnerwire.TokenPrefix)[:8]) {
		t.Fatalf("status printed a secret:\n%s", stdout)
	}
	for _, want := range []string{"https://hub.example", "token:   (set)", "client id cid", "9.9.9 (Claude Code)", "codex   not found"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}

	if code, _, _ := runRunner(t, "", "logout", "--config", cfgPath); code != 0 {
		t.Fatal("logout failed")
	}
	cfg, _ := runner.LoadConfig(cfgPath)
	if cfg.Token != "" || cfg.AccessClientSecret != "" || cfg.Hub == "" {
		t.Fatalf("logout left %+v", cfg)
	}
}

func TestRunnerRepoAddAsksFirst(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repoDir := t.TempDir()
	if out, err := exec.Command("git", "-C", repoDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cfgPath := filepath.Join(t.TempDir(), "runner.json")

	if code, _, _ := runRunner(t, "n\n", "repo", "add", repoDir, "--config", cfgPath); code == 0 {
		t.Fatal("repo add without a yes succeeded")
	}
	code, stdout, stderr := runRunner(t, "y\n", "repo", "add", repoDir, "--name", "mine", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("repo add: %s", stderr)
	}
	if !strings.Contains(stderr, "Allow agent runs") || !strings.Contains(stdout, `"mine"`) {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
	if code, _, _ := runRunner(t, "", "repo", "add", repoDir, "--yes", "--config", cfgPath); code == 0 {
		t.Fatal("same repository added twice")
	}
	_, list, _ := runRunner(t, "", "repo", "list", "--config", cfgPath)
	if !strings.Contains(list, "mine") {
		t.Fatalf("list = %q", list)
	}
	if code, _, stderr := runRunner(t, "", "repo", "remove", "mine", "--config", cfgPath); code != 0 {
		t.Fatalf("remove: %s", stderr)
	}
	if cfg, _ := runner.LoadConfig(cfgPath); len(cfg.Repos) != 0 {
		t.Fatalf("repos after remove = %+v", cfg.Repos)
	}
}

func TestRunnerSetAndStatus(t *testing.T) {
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	cfgPath := filepath.Join(t.TempDir(), "runner.json")

	code, _, stderr := runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 {
		t.Fatal(stderr)
	}
	for _, args := range [][]string{
		{"set", "maxConcurrent", "2"},
		{"set", "runTimeoutMinutes", "30", "--config", cfgPath},
		{"set", "permissionMode", "plan"},
		{"set", "allowedTools", "Edit", "Bash(go test *)"},
	} {
		args = append(args, "--config", cfgPath)
		if code, _, stderr := runRunner(t, "", args...); code != 0 {
			t.Fatalf("%v: exit %d %s", args, code, stderr)
		}
	}
	code, stdout, stderr := runRunner(t, "", "set", "autoAccept", "true", "--config", cfgPath)
	if code != 0 || !strings.Contains(stderr, "Anyone who can sign in to the hub as you") {
		t.Fatalf("autoAccept: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "autoAccept         true") {
		t.Fatalf("set output:\n%s", stdout)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 2 || cfg.RunTimeoutMinutes != 30 || cfg.PermissionMode != "plan" ||
		len(cfg.AllowedTools) != 2 || !cfg.AutoAccept {
		t.Fatalf("config = %+v", cfg)
	}
	_, stdout, _ = runRunner(t, "", "status", "--config", cfgPath)
	for _, want := range []string{"maxConcurrent      2", "runTimeoutMinutes  30", "permissionMode     plan",
		"allowedTools       Edit, Bash(go test *)", "AUTO-ACCEPT", "runner-data"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}

	for _, bad := range [][]string{{"set"}, {"set", "maxConcurrent"}, {"set", "bogus", "1"}, {"set", "permissionMode", "yolo"}} {
		if code, _, _ := runRunner(t, "", append(bad, "--config", cfgPath)...); code == 0 {
			t.Errorf("%v succeeded", bad)
		}
	}
}

func TestRunnerStartNeedsLogin(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	code, _, stderr := runRunner(t, "", "--config", cfgPath)
	if code != 1 || !strings.Contains(stderr, "runner login") {
		t.Fatalf("start without login: exit %d, %s", code, stderr)
	}
	if code, _, _ := runRunner(t, "", "bogus"); code != 2 {
		t.Fatalf("unknown subcommand exit %d", code)
	}
}
