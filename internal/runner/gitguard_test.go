package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeHook puts a hook that leaves a marker file into dir.
func writeHook(t *testing.T, dir, name, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ran > '" + filepath.ToSlash(marker) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRunSkipsInTreeHooks(t *testing.T) {
	repo := newTestRepo(t)
	gitT(t, repo, "config", "core.hooksPath", ".husky")
	ctx := context.Background()

	// Control: the hook is real, plain git runs it.
	control := filepath.Join(t.TempDir(), "control")
	writeHook(t, filepath.Join(repo, ".husky"), "pre-commit", control)
	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "c.txt")
	gitT(t, repo, "commit", "-q", "-m", "control")
	if _, err := os.Stat(control); err != nil {
		t.Fatalf("control hook did not run, so the test proves nothing: %v", err)
	}

	wt, err := CreateWorktree(ctx, repo, t.TempDir(), "run-hook", "n", "main")
	if err != nil {
		t.Fatal(err)
	}
	// What an agent would write: a hook in the tree the repository's config
	// already points at.
	marker := filepath.Join(t.TempDir(), "marker")
	writeHook(t, filepath.Join(wt.Path, ".husky"), "pre-commit", marker)
	writeHook(t, filepath.Join(wt.Path, ".husky"), "post-commit", marker)
	if err := os.WriteFile(filepath.Join(wt.Path, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := commitRun(ctx, wt, "agent work")
	if res.Err != nil || res.Commit == "" {
		t.Fatalf("commit: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook from the worktree ran during the runner's commit")
	}
}

func TestCommitRunRefusesSwappedGitDir(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	wt, err := CreateWorktree(ctx, repo, t.TempDir(), "run-swap", "n", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkGitDir(ctx, wt); err != nil {
		t.Fatalf("untouched worktree: %v", err)
	}

	// The agent makes a repository of its own, with a hook, and points the
	// worktree's .git at it.
	evil := filepath.Join(wt.Path, "evil")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, evil, "init", "-q")
	marker := filepath.Join(t.TempDir(), "marker")
	writeHook(t, filepath.Join(evil, ".git", "hooks"), "pre-commit", marker)
	gitT(t, evil, "config", "core.fsmonitor", "echo pwned > '"+filepath.ToSlash(marker)+"'")
	pointer := "gitdir: " + filepath.ToSlash(filepath.Join(evil, ".git")) + "\n"
	if err := os.WriteFile(filepath.Join(wt.Path, ".git"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkGitDir(ctx, wt); !errors.Is(err, errGitDirChanged) {
		t.Fatalf("checkGitDir = %v", err)
	}
	res := commitRun(ctx, wt, "agent work")
	if !errors.Is(res.Err, errGitDirChanged) || res.Commit != "" {
		t.Fatalf("commit after the swap: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the swapped git directory's hook ran")
	}

	// A .git turned into a directory is refused as well.
	if err := os.Remove(filepath.Join(wt.Path, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt.Path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkGitDir(ctx, wt); !errors.Is(err, errGitDirChanged) {
		t.Fatalf(".git as a directory: %v", err)
	}
	// So is a worktree whose pointer was never recorded.
	if err := checkGitDir(ctx, Worktree{Path: wt.Path}); !errors.Is(err, errGitDirChanged) {
		t.Fatalf("unrecorded: %v", err)
	}
}
