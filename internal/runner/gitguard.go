package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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

// noHooksPath is the core.hooksPath that runs no hook, as internal/gitrepo's:
// a path inside the git executable, which is a file, so no directory can
// ever exist there and nobody who could not already replace git can put a
// hook there. Never a directory: one in the temp dir is writable by a Codex
// run's default sandbox, and a hook planted there would run as the owner.
func noHooksPath() string {
	noHooksOnce.Do(func() {
		base, err := exec.LookPath("git")
		if err != nil {
			// A path inside the null device holds no files either.
			base = os.DevNull
		}
		noHooksDir = filepath.ToSlash(base) + "/no-hooks"
	})
	return noHooksDir
}

// hardenedArgs puts the runner's overrides in front of a git command line:
// command-line -c wins over any repository or worktree config.
// diff.ignoreSubmodules=dirty keeps porcelain from running `git status`
// inside a submodule, whose repository (and the filters its config names) an
// agent can write; .gitmodules can undo it, so the runner's own commands that
// read the worktree also pass --ignore-submodules (or stage as stageAll does).
func hardenedArgs(dir string, args []string) []string {
	return append([]string{"-C", dir,
		"-c", "core.hooksPath=" + noHooksPath(),
		"-c", "core.fsmonitor=false",
		"-c", "diff.ignoreSubmodules=dirty",
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

// gitConfigEnv is hardenedArgs' overrides as environment, for programs that
// run git themselves (gh): command-scope config, which wins over the
// repository's own.
func gitConfigEnv() []string {
	return gitConfigEnvWith(nil)
}

// gitConfigEnvWith is gitConfigEnv plus more key/value overrides.
func gitConfigEnvWith(extra [][2]string) []string {
	pairs := append([][2]string{
		{"core.hooksPath", noHooksPath()},
		{"core.fsmonitor", "false"},
		{"diff.ignoreSubmodules", "dirty"},
	}, extra...)
	out := []string{fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(pairs))}
	for i, p := range pairs {
		out = append(out, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, p[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, p[1]))
	}
	return out
}

// maxSubmoduleOverrides bounds the per-submodule overrides gh's git gets.
const maxSubmoduleOverrides = 4096

// ghGitEnv is the git config environment for gh run in dir. gh runs git
// itself (git status in pr create), and `ignore = none` in .gitmodules, which
// a run's agent can write, beats diff.ignoreSubmodules: that git would look
// inside a submodule the agent populated and run a filter its config names.
// submodule.<name>.ignore from the environment beats .gitmodules, so every
// name .gitmodules gives a path, in the worktree's file, the index's or
// HEAD's, gets ignore = dirty. A .gitmodules that cannot be read fails the
// call: gh's git would read it too.
func ghGitEnv(ctx context.Context, dir string) ([]string, error) {
	if dir == "" {
		return gitConfigEnv(), nil
	}
	names, err := submoduleNames(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("reading .gitmodules before running gh: %w", err)
	}
	if len(names) > maxSubmoduleOverrides {
		return nil, fmt.Errorf(".gitmodules names %d submodules, more than the %d gh can be run with safely", len(names), maxSubmoduleOverrides)
	}
	extra := make([][2]string, 0, len(names))
	for _, name := range names {
		extra = append(extra, [2]string{"submodule." + name + ".ignore", "dirty"})
	}
	return gitConfigEnvWith(extra), nil
}

// submoduleNames is every submodule name with a path in dir's .gitmodules:
// the worktree's file, the index's and HEAD's, as git may read any of them.
// A dir in no repository has none: gh's git finds no repository there either.
func submoduleNames(ctx context.Context, dir string) ([]string, error) {
	top, code, stderr, err := gitExit(ctx, dir, "rev-parse", "--show-toplevel")
	switch {
	case err != nil:
		return nil, err
	case code != 0 && strings.Contains(stderr, "not a git repository"):
		return nil, nil
	case code != 0:
		return nil, fmt.Errorf("git rev-parse --show-toplevel: %s", firstLine(stderr))
	}
	top = strings.TrimSpace(top)
	var sources [][]string
	file := filepath.Join(top, ".gitmodules")
	if info, err := os.Lstat(file); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("the worktree's .gitmodules is not a regular file")
		}
		sources = append(sources, []string{"--file", file})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, rev := range []string{":.gitmodules", "HEAD:.gitmodules"} {
		found, err := gitHas(ctx, dir, rev)
		if err != nil {
			return nil, err
		}
		if found {
			sources = append(sources, []string{"--blob", rev})
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, src := range sources {
		out, err := git(ctx, dir, append(append([]string{"config", "--no-includes"}, src...), "--list", "--name-only", "-z")...)
		if err != nil {
			return nil, err
		}
		for _, key := range strings.Split(out, "\x00") {
			key = strings.TrimSuffix(key, "\n")
			lower := strings.ToLower(key)
			if len(key) <= len("submodule..path") || !strings.HasPrefix(lower, "submodule.") || !strings.HasSuffix(lower, ".path") {
				continue
			}
			name := key[len("submodule.") : len(key)-len(".path")]
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// gitHas says whether rev names an object in dir's repository; an unborn
// HEAD names none.
func gitHas(ctx context.Context, dir, rev string) (bool, error) {
	_, code, stderr, err := gitExit(ctx, dir, "rev-parse", "--verify", "--quiet", rev)
	switch {
	case err != nil:
		return false, err
	case code == 0:
		return true, nil
	case code == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git rev-parse %s: %s", rev, firstLine(stderr))
	}
}

// gitExit runs hardened git in dir, in the C locale, and returns its stdout,
// exit code and stderr; err is only for a git that could not run.
func gitExit(ctx context.Context, dir string, args ...string) (string, int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", hardenedArgs(dir, args)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "LC_ALL=C", "LANGUAGE=C")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return stdout.String(), exit.ExitCode(), stderr.String(), nil
	}
	if err != nil {
		return "", -1, stderr.String(), fmt.Errorf("git %s: %w", gitVerb(args), err)
	}
	return stdout.String(), 0, stderr.String(), nil
}
