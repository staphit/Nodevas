package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchName(t *testing.T) {
	cases := map[[2]string]string{
		{"n-42", "RUN_0123456789"}:          "agent/n-42-run-0123",
		{"Fix: the Login (again)!", "abc"}:  "agent/fix-the-login-again-abc",
		{"", ""}:                            "agent/node-run",
		{"节点", "r1"}:                        "agent/node-r1",
		{strings.Repeat("long-", 30), "x1"}: "",
	}
	for in, want := range cases {
		got := BranchName(in[0], in[1])
		if len(got) > maxBranchLen {
			t.Errorf("BranchName(%q) = %q is %d long", in, got, len(got))
		}
		for _, r := range strings.TrimPrefix(got, "agent/") {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("BranchName(%q) = %q has %q", in, got, r)
			}
		}
		if want != "" && got != want {
			t.Errorf("BranchName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := BranchName(strings.Repeat("long-", 30), "x1"); !strings.HasSuffix(got, "-x1") || strings.Contains(got, "--") {
		t.Errorf("long node: %q", got)
	}
}

func TestCheckBase(t *testing.T) {
	for _, ok := range []string{"main", "release/1.2", "feat/runner-w1", "v1.0"} {
		if err := checkBase(ok); err != nil {
			t.Errorf("checkBase(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"-x", "--upload-pack=evil", "a:b", "a..b", "a b", "x/", "x.lock", "", "a//b", "\x00"} {
		if checkBase(bad) == nil {
			t.Errorf("checkBase(%q) accepted", bad)
		}
	}
}

// gitT runs git for a test.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newTestRepo is a repository with one commit on main and no remote.
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "README")
	gitT(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestCreateWorktreeOffline(t *testing.T) {
	repo := newTestRepo(t)
	// The main checkout is on another branch; with no origin, the default is
	// the branch the main checkout is on.
	gitT(t, repo, "checkout", "-q", "-b", "trunk")
	data := t.TempDir()
	wt, err := CreateWorktree(context.Background(), repo, data, "run-1", "n-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Base != "trunk" || wt.BaseRef != "trunk" || wt.Branch != "agent/n-1-run-1" ||
		wt.Path != filepath.Join(data, "worktrees", "run-1") {
		t.Fatalf("worktree = %+v", wt)
	}
	if got := gitT(t, wt.Path, "branch", "--show-current"); got != wt.Branch {
		t.Fatalf("worktree is on %q", got)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "README")); err != nil {
		t.Fatal(err)
	}

	// An explicit base works too, and the same run cannot be made twice.
	if _, err := CreateWorktree(context.Background(), repo, data, "run-2", "n-1", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWorktree(context.Background(), repo, data, "run-1", "n-1", "main"); err == nil ||
		!strings.Contains(err.Error(), "git worktree") {
		t.Fatalf("second worktree for run-1: %v", err)
	}
	if _, err := CreateWorktree(context.Background(), repo, data, "run-3", "n-1", "nope"); err == nil ||
		!strings.Contains(err.Error(), "neither locally nor on origin") {
		t.Fatalf("missing base: %v", err)
	}
	for _, bad := range []string{"../escape", "a/b", ""} {
		if _, err := CreateWorktree(context.Background(), repo, data, bad, "n", "main"); err == nil {
			t.Errorf("run id %q accepted", bad)
		}
	}
	if _, err := CreateWorktree(context.Background(), repo, data, "run-4", "n", "--orphan"); err == nil {
		t.Error("an option as a base was accepted")
	}
}

func TestCreateWorktreeFromOrigin(t *testing.T) {
	upstream := newTestRepo(t)
	gitT(t, upstream, "checkout", "-q", "-b", "develop")
	gitT(t, upstream, "commit", "-q", "--allow-empty", "-m", "on develop")
	gitT(t, upstream, "checkout", "-q", "main")

	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, filepath.Dir(clone), "clone", "-q", upstream, clone)
	// The clone's main checkout wanders off; origin/HEAD still says main.
	gitT(t, clone, "checkout", "-q", "-b", "local-work")
	// Upstream moves on after the clone: the run must start from the fetched tip.
	gitT(t, upstream, "commit", "-q", "--allow-empty", "-m", "newer")
	want := gitT(t, upstream, "rev-parse", "main")

	data := t.TempDir()
	wt, err := CreateWorktree(context.Background(), clone, data, "r1", "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Base != "main" || wt.BaseRef != "origin/main" {
		t.Fatalf("worktree = %+v", wt)
	}
	if got := gitT(t, wt.Path, "rev-parse", "HEAD"); got != want {
		t.Fatalf("worktree HEAD %s, want fetched %s", got, want)
	}

	// A base that only exists on origin.
	wt, err = CreateWorktree(context.Background(), clone, data, "r2", "node", "develop")
	if err != nil {
		t.Fatal(err)
	}
	if wt.BaseRef != "origin/develop" {
		t.Fatalf("develop worktree = %+v", wt)
	}
}
