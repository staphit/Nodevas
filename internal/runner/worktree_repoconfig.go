package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// A read-only run (a review or a plan) checks out content that is not the
// owner's, and agent CLIs run programs from config they find in the
// directory they run in. See adapter/repoconfig.go for what each CLI reads
// and which have a switch for it; for the rest, the runner removes that
// config from the worktree before the agent starts and again after it exits,
// before anything else runs there. The run's files are never committed, and
// its worktree is thrown away after.

// runAgent is startAgent, with the worktree's own agent config removed first
// and again after when the run is read only. job is r.job, or for a review
// the job with the PR's diff added to its prompt.
func (j *jobs) runAgent(ctx context.Context, r *run, wt Worktree, job runnerwire.Job) (runnerwire.RunResult, string) {
	if !adapter.ReadOnly(job) {
		return j.startAgent(ctx, r, wt, job)
	}
	removed, err := clearRepoConfig(wt.Path, adapter.RepoConfigPaths())
	if err != nil {
		return runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: runnerwire.FailureTask,
			Error: adapter.Truncate("could not remove the worktree's own agent config: "+err.Error(),
				runnerwire.MaxSummaryBytes)}, ""
	}
	if len(removed) > 0 {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: "read only: removed the worktree's own agent config (not trusted here): " + strings.Join(removed, ", ")})
	}
	result, text := j.startAgent(ctx, r, wt, job)

	// Again, before anything else runs in the worktree: the agent may have
	// written some (a CLI's own files are expected, see RunWrittenRepoConfig).
	removed, err = clearRepoConfig(wt.Path, adapter.RepoConfigPaths())
	if err != nil {
		j.c.logf("run %s: removing agent config after the run: %v", job.RunID, err)
		if result.Status != runnerwire.RunCancelled {
			return runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: result.ExitCode,
				FailureKind: runnerwire.FailureTask, Summary: result.Summary, Usage: result.Usage,
				Error: adapter.Truncate("could not remove the agent config left in the worktree: "+err.Error(),
					runnerwire.MaxSummaryBytes)}, ""
		}
		return result, text
	}
	expected := adapter.RunWrittenRepoConfig(job.Agent)
	var unexpected []string
	for _, path := range removed {
		if !slices.Contains(expected, path) {
			unexpected = append(unexpected, path)
		}
	}
	if len(unexpected) > 0 {
		j.c.logf("run %s: the agent left agent config in its read-only worktree; removed: %s", job.RunID,
			strings.Join(unexpected, ", "))
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: "the agent wrote agent config into its read-only worktree; removed: " + strings.Join(unexpected, ", ")})
	}
	return result, text
}

// clearRepoConfig removes each of paths (slash-separated, relative to dir)
// that exists, without following a symlink anywhere: a link on the way (a
// .claude that points at the owner's own) is removed itself, not what it
// points at. removed lists what went, in slash form.
func clearRepoConfig(dir string, paths []string) (removed []string, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, path := range paths {
		target, err := repoConfigTarget(root, path)
		if err != nil {
			return removed, err
		}
		if target == "" || slices.Contains(removed, target) {
			continue
		}
		if err := root.RemoveAll(filepath.FromSlash(target)); err != nil {
			return removed, fmt.Errorf("removing %s: %w", target, err)
		}
		removed = append(removed, target)
	}
	return removed, nil
}

// repoConfigTarget is what to remove for path: path itself, or the first
// directory on its way that is not a plain directory (a symlink, a file, a
// Windows reparse point), or "" when nothing is there.
func repoConfigTarget(root *os.Root, path string) (string, error) {
	parts := strings.Split(path, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(filepath.FromSlash(prefix))
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("checking %s: %w", prefix, err)
		}
		last := i == len(parts)-1
		if !last && (!info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0) {
			return prefix, nil
		}
		if last {
			return prefix, nil
		}
	}
	return "", nil
}

// changedFiles is `git status --porcelain` of a read-only run's worktree,
// leaving out the agent config the runner removed there.
func changedFiles(ctx context.Context, dir string) (string, error) {
	args := []string{"status", "--porcelain", "--", "."}
	for _, path := range adapter.RepoConfigPaths() {
		args = append(args, ":(exclude,literal)"+path)
	}
	return git(ctx, dir, args...)
}
