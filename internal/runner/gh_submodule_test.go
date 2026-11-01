package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitStatusRunner stands in for gh: gh pr create runs `git status
// --porcelain` in its directory to count uncommitted changes, with the
// environment the runner gave it. This runs exactly that.
type gitStatusRunner struct{ env []string }

func (g *gitStatusRunner) RunIn(ctx context.Context, dir string, env []string, _ string, _ string, _ ...string) (string, error) {
	g.env = env
	return (SystemCommands{}).RunIn(ctx, dir, env, "", "git", "status", "--porcelain")
}

// The git that gh runs in a run's worktree reads the worktree's .gitmodules,
// which the agent wrote: `ignore = none` there undoes diff.ignoreSubmodules,
// so gh's git status would look inside a submodule the agent populated and
// run the filter its config names, as the owner. gh's git gets a per-name
// override, which wins over .gitmodules, for every submodule .gitmodules
// names, including one whose name holds dots.
func TestGHGitLooksIntoNoSubmodule(t *testing.T) {
	for _, name := range []string{"nested", "a.b"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			sub := newTestRepo(t)
			if err := os.WriteFile(filepath.Join(sub, "b.go"), []byte("package b\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitT(t, sub, "add", "b.go")
			gitT(t, sub, "commit", "-q", "-m", "b")
			repo := newTestRepo(t)
			gitT(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+gitT(t, sub, "rev-parse", "HEAD")+",nested")
			gitT(t, repo, "commit", "-q", "-m", "submodule")

			wt, err := CreateWorktree(ctx, repo, t.TempDir(), "run-ghsub", "n", "main")
			if err != nil {
				t.Fatal(err)
			}
			// What the agent does: name the submodule in .gitmodules with
			// ignore = none, populate it and plant a filter.
			if err := os.WriteFile(filepath.Join(wt.Path, ".gitmodules"),
				[]byte("[submodule \""+name+"\"]\n\tpath = nested\n\turl = ./nested\n\tignore = none\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(wt.Path, "nested")
			gitT(t, wt.Path, "clone", "-q", sub, "nested")
			marker := filepath.Join(t.TempDir(), "marker")
			if err := os.WriteFile(filepath.Join(nested, ".gitattributes"), []byte("*.go filter=evil\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitT(t, nested, "config", "filter.evil.clean", "echo x > '"+filepath.ToSlash(marker)+"'; cat")
			stamp := time.Now().Add(-48 * time.Hour)
			dirty := func() {
				t.Helper()
				stamp = stamp.Add(time.Hour)
				if err := os.Chtimes(filepath.Join(nested, "b.go"), stamp, stamp); err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(marker)
			}

			// Control: the runner's git overrides alone do not stop it, so
			// the test proves something.
			dirty()
			if _, err := (SystemCommands{}).RunIn(ctx, wt.Path, gitConfigEnv(), "", "git", "status", "--porcelain"); err != nil {
				t.Fatalf("git status: %v", err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("control: git status under gitConfigEnv did not run the filter, so the test proves nothing: %v", err)
			}

			dirty()
			fake := &gitStatusRunner{}
			g := &ghCLI{run: fake, bin: "gh"}
			if _, err := g.call(ctx, wt.Path, "", "pr", "create"); err != nil {
				t.Fatalf("gh's git status failed: %v", err)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the git gh runs ran the filter the agent put in a submodule's config")
			}
			// The rest of the runner's hardening still reaches gh.
			for _, want := range []string{"core.hooksPath", "core.fsmonitor", "diff.ignoreSubmodules"} {
				if !strings.Contains(strings.Join(fake.env, "\n"), "="+want+"\n") {
					t.Errorf("gh's environment lacks %s: %q", want, fake.env)
				}
			}
		})
	}
}
