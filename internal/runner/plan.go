package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// executePlan is a plan job (W7) after its accept: the agent reads the
// repository at the job's base and breaks the node into task nodes on the
// board through the nodevas MCP tools. It runs read-only (as a review does),
// and nothing it does to the files is kept: no commit, no push, no pull
// request, and the worktree and its branch go when it ends.
func (j *jobs) executePlan(ctx context.Context, r *run) {
	job := r.job
	dataDir, err := j.c.Config.RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = CreateWorktree(ctx, r.repo.Path, dataDir, job.RunID, job.NodeID, job.Base)
	}
	if err != nil {
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate("could not create the worktree: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return
	}
	// Gone before the finish goes out, branch and all: whoever reads the
	// finish finds nothing of the plan left on this machine.
	cleanup := func() {
		if err := removeWorktree(j.ctx, r.repo.Path, wt, true); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	// No branch to report: nothing of a plan is pushed.
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: wt.Base}
	j.mu.Unlock()
	j.wake()
	j.c.logf("run %s: accepted (plan); worktree %s from %s, thrown away after", job.RunID, wt.Path, wt.BaseRef)
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("plan: worktree %s from %s (read only; thrown away after)", wt.Path, wt.BaseRef)})

	result, _ := j.runAgent(ctx, r, wt, job)
	if err := checkGitDir(j.ctx, wt); err != nil {
		cleanup()
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			FailureKind: runnerwire.FailureTask, Error: err.Error()})
		return
	}
	if dirty, _ := changedFiles(j.ctx, wt.Path); strings.TrimSpace(dirty) != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "the planner changed files; discarded"})
	}
	cleanup()
	j.finish(r, result)
}
