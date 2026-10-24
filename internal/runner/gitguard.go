package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/runnerwire"
)

// errGitDirChanged is why a run fails when its agent pointed the worktree at
// another git directory: git run there would read config (hooks, fsmonitor,
// filters) the agent wrote, and run it as the owner.
var errGitDirChanged = errors.New("the worktree's git directory was changed")

var (
	noHooksOnce sync.Once
	noHooksDir  string
)

// noHooksPath is an empty directory the runner owns, for core.hooksPath: no
// hook — the repository's own, or one a run wrote — runs from the runner's git.
func noHooksPath() string {
	noHooksOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nodevas-nohooks-")
		if err != nil {
			// A path inside the null device holds no files either.
			dir = filepath.Join(os.DevNull, "nodevas-nohooks")
		}
		noHooksDir = filepath.ToSlash(dir)
	})
	return noHooksDir
}

// hardenedArgs puts the runner's overrides in front of a git command line:
// command-line -c wins over any repository or worktree config.
func hardenedArgs(dir string, args []string) []string {
	return append([]string{"-C", dir,
		"-c", "core.hooksPath=" + noHooksPath(),
		"-c", "core.fsmonitor=false",
	}, args...)
}

// recordGitDir notes, right after the worktree is made and before any agent
// runs in it, where its .git file points.
func recordGitDir(ctx context.Context, wt *Worktree) error {
	b, err := os.ReadFile(filepath.Join(wt.Path, ".git"))
	if err != nil {
		return err
	}
	dirs, err := gitDirs(ctx, wt.Path)
	if err != nil {
		return err
	}
	wt.GitFile, wt.GitDirs = string(b), dirs
	return nil
}

// gitDirs is the worktree's git directory and common directory, absolute.
func gitDirs(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// checkGitDir is called before any git command after the agent ran: the
// worktree's .git is still the file the runner made, pointing where it did.
func checkGitDir(ctx context.Context, wt Worktree) error {
	if wt.GitFile == "" {
		return errGitDirChanged
	}
	path := filepath.Join(wt.Path, ".git")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errGitDirChanged
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, []byte(wt.GitFile)) {
		return errGitDirChanged
	}
	dirs, err := gitDirs(ctx, wt.Path)
	if err != nil || dirs != wt.GitDirs {
		return errGitDirChanged
	}
	return nil
}

// guardGitDir fails the run when its agent changed where the worktree's git
// directory is: nothing more of it is committed, pushed or posted. ok is
// false when the run was finished here.
func (j *jobs) guardGitDir(r *run, wt Worktree) bool {
	if err := checkGitDir(j.ctx, wt); err != nil {
		j.c.logf("run %s: %v; nothing committed or pushed; worktree kept at %s", r.job.RunID, err, wt.Path)
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			FailureKind: runnerwire.FailureTask, Error: err.Error() + "; nothing of the run was committed or pushed"})
		return false
	}
	return true
}
