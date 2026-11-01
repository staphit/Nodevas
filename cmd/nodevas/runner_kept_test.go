package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
)

// Worktrees kept for withheld agent config show in `runner status`, and
// `runner prune-kept` removes them on request (--dry-run only lists).
func TestRunnerStatusListsKeptWorktreesAndPruneKeptRemovesThem(t *testing.T) {
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	if code, _, stderr := runRunner(t, cliTestToken+"\n", "login", "--hub", "https://hub.example", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	dataDir := t.TempDir()
	if code, _, stderr := runRunner(t, "", "set", "dataDir", dataDir, "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@example.test",
		"commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	ctx := context.Background()
	wt, err := runner.CreateWorktree(ctx, repo, dataDir, "run-kept", "n-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt.Path, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, ".codex", "config.toml"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runner.RecordKept(ctx, dataDir, repo, wt, []string{".codex/config.toml"}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, stderr)
	}
	for _, want := range []string{"kept worktrees: 1", wt.Path, "nodevas runner prune-kept"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = runRunner(t, "", "prune-kept", "--dry-run", "--config", cfgPath)
	if code != 0 || !strings.Contains(stdout, "would remove "+wt.Path) {
		t.Fatalf("prune-kept --dry-run exit %d:\n%s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("a dry run removed the worktree: %v", err)
	}
	code, stdout, stderr = runRunner(t, "", "prune-kept", "--config", cfgPath)
	if code != 0 || !strings.Contains(stdout, "removed "+wt.Path) {
		t.Fatalf("prune-kept exit %d:\n%s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still there: %v", err)
	}
	if _, stdout, _ = runRunner(t, "", "status", "--config", cfgPath); !strings.Contains(stdout, "kept worktrees: 0") {
		t.Errorf("status after pruning:\n%s", stdout)
	}
}
