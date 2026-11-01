package agenthook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
