package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run's agent can fill a submodule's directory with a repository of its
// own (a clone, a .git it wrote) whose config names a filter driver. git
// looking for changes inside that submodule runs `git status` there, which
// runs the filter: as the owner, outside any sandbox the agent had. The
// runner's git never looks inside a submodule, whatever .gitmodules says, and
// still commits a submodule the agent moved to another commit.
func TestRunnerGitLooksIntoNoSubmodule(t *testing.T) {
	ctx := context.Background()
	sub := newTestRepo(t)
	if err := os.WriteFile(filepath.Join(sub, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, sub, "add", "b.go")
	gitT(t, sub, "commit", "-q", "-m", "b")
	repo := newTestRepo(t)
	gitT(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+gitT(t, sub, "rev-parse", "HEAD")+",nested")
	// ignore = none in .gitmodules (the agent's to write) undoes any
	// diff.ignoreSubmodules config.
	if err := os.WriteFile(filepath.Join(repo, ".gitmodules"),
		[]byte("[submodule \"nested\"]\n\tpath = nested\n\turl = ./nested\n\tignore = none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".gitmodules")
	gitT(t, repo, "commit", "-q", "-m", "submodule")

	wt, err := CreateWorktree(ctx, repo, t.TempDir(), "run-sub", "n", "main")
	if err != nil {
		t.Fatal(err)
	}
	// What the agent does: populate the submodule and plant a filter.
	nested := filepath.Join(wt.Path, "nested")
	gitT(t, wt.Path, "clone", "-q", sub, "nested")
	marker := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(filepath.Join(nested, ".gitattributes"), []byte("*.go filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, nested, "config", "filter.evil.clean", "echo x > '"+filepath.ToSlash(marker)+"'; cat")
	// dirty makes b.go stat-dirty in the submodule's index, so git has to
	// read (and clean) it to know whether it changed.
	stamp := time.Now().Add(-48 * time.Hour)
	dirty := func() {
		t.Helper()
		stamp = stamp.Add(time.Hour)
		if err := os.Chtimes(filepath.Join(nested, "b.go"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(marker)
	}
	ran := func(what string) {
		t.Helper()
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s ran the filter the agent put in a submodule's config", what)
		}
	}

	// Control: plain git runs it, so the test proves something.
	dirty()
	if out, err := exec.Command("git", "-C", wt.Path, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: plain git status did not run the filter, so the test proves nothing: %v", err)
	}

	dirty()
	if out, err := changedFiles(ctx, wt.Path); err != nil {
		t.Fatalf("changedFiles: %v %s", err, out)
	}
	ran("changedFiles")

	dirty()
	if _, err := repoDiff(ctx, wt.Path, ""); err != nil {
		t.Fatalf("repoDiff: %v", err)
	}
	ran("repoDiff")

	dirty()
	if err := os.WriteFile(filepath.Join(wt.Path, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := commitRun(ctx, wt, "agent work"); res.Err != nil || res.Commit == "" {
		t.Fatalf("commit: %+v", res)
	}
	ran("commitRun")

	// The agent moved the submodule: that is still committed.
	if err := os.WriteFile(filepath.Join(nested, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, nested, "add", "c.txt")
	gitT(t, nested, "commit", "-q", "-m", "c")
	moved := gitT(t, nested, "rev-parse", "HEAD")
	dirty()
	res := commitRun(ctx, wt, "move the submodule")
	if res.Err != nil || res.Commit == "" {
		t.Fatalf("commit: %+v", res)
	}
	ran("commitRun")
	if tree := gitT(t, wt.Path, "ls-tree", "HEAD", "nested"); !strings.Contains(tree, moved) {
		t.Fatalf("the moved submodule was not committed: %q, want %s", tree, moved)
	}
}
