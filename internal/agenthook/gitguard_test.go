package agenthook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The hook's git runs as the person, at the end of an agent's turn, in a
// repository the agent can write: nothing the repository's config or
// attributes name (fsmonitor, hooks, filter and textconv drivers) runs, as
// with the runner's own git.
func TestSnapshotRunsNothingFromRepoConfig(t *testing.T) {
	repo := gitRepo(t)
	markers := t.TempDir()
	marker := func(name string) string { return filepath.ToSlash(filepath.Join(markers, name)) }
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + filepath.ToSlash(markers) + "/none"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(t, filepath.Join(repo, ".gitattributes"), "*.go diff=evil filter=evil\n")
	gitIn("add", ".gitattributes")
	gitIn("commit", "-q", "-m", "attributes")
	gitIn("config", "core.fsmonitor", "echo x > '"+marker("fsmonitor")+"'; true")
	gitIn("config", "diff.evil.textconv", "echo x > '"+marker("textconv")+"'; cat")
	gitIn("config", "filter.evil.clean", "echo x > '"+marker("clean")+"'; cat")
	gitIn("config", "filter.evil.smudge", "cat")
	hooks := filepath.Join(repo, ".git", "hooks")
	for _, name := range []string{"post-index-change", "reference-transaction"} {
		write(t, filepath.Join(hooks, name), "#!/bin/sh\necho x > '"+marker(name)+"'\n")
		if err := os.Chmod(filepath.Join(hooks, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(repo, "a.go"), "package a\n\nvar x = 2\n")
	write(t, filepath.Join(repo, "untracked.txt"), "new\n")

	snap, ok := Snapshot(context.Background(), repo)
	if !ok || !strings.Contains(snap.Patch, "+var x = 2") {
		t.Fatalf("snapshot = %+v, %v", snap, ok)
	}
	ran, _ := os.ReadDir(markers)
	var names []string
	for _, e := range ran {
		names = append(names, e.Name())
	}
	if len(names) > 0 {
		t.Fatalf("the hook's git ran what the repository configured: %v", names)
	}
}

// A submodule's own repository is the agent's to write too: a filter its
// config names would run when git looks inside it for changes, whatever
// .gitmodules or the repository's config says about ignoring it. The hook's
// git never looks inside a submodule.
func TestSnapshotLooksIntoNoSubmodule(t *testing.T) {
	repo := gitRepo(t)
	sub := gitRepo(t)
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test",
			"-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn(repo, "update-index", "--add", "--cacheinfo", "160000,"+gitIn(sub, "rev-parse", "HEAD")+",nested")
	write(t, filepath.Join(repo, ".gitmodules"), "[submodule \"nested\"]\n\tpath = nested\n\turl = ./nested\n\tignore = none\n")
	gitIn(repo, "add", ".gitmodules")
	gitIn(repo, "commit", "-q", "-m", "submodule")
	gitIn(repo, "config", "submodule.nested.ignore", "none")
	gitIn(repo, "config", "diff.submodule", "diff")
	gitIn(repo, "clone", "-q", sub, "nested")
	nested := filepath.Join(repo, "nested")
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "marker"))
	write(t, filepath.Join(nested, ".gitattributes"), "*.go filter=evil\n")
	gitIn(nested, "config", "filter.evil.clean", "echo x > '"+marker+"'; cat")
	stamp := time.Now().Add(-48 * time.Hour)
	dirty := func() {
		t.Helper()
		stamp = stamp.Add(time.Hour)
		if err := os.Chtimes(filepath.Join(nested, "a.go"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(marker)
	}

	// Control: plain git runs it, so the test proves something.
	dirty()
	if out, err := exec.Command("git", "-C", repo, "diff", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git diff: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: plain git diff did not run the filter, so the test proves nothing: %v", err)
	}

	dirty()
	write(t, filepath.Join(repo, "a.go"), "package a\n\nvar x = 2\n")
	snap, ok := Snapshot(context.Background(), repo)
	if !ok || !strings.Contains(snap.Patch, "+var x = 2") {
		t.Fatalf("snapshot = %+v, %v", snap, ok)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the hook's git ran the filter a submodule's config names")
	}
}
