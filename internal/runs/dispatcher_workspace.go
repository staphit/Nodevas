package runs

import (
	"context"
	"log/slog"
	"time"

	"nodevas/internal/runnerwire"
)

// workspaceVerb is an action's word in audit names: run.workspace.<verb>.*.
func workspaceVerb(action string) string {
	switch action {
	case runnerwire.WorkspaceMergeLocal:
		return "merge"
	case runnerwire.WorkspaceOpenPR:
		return "open_pr"
	default:
		return "discard"
	}
}

// WorkspaceRunnerOf is what the gates need to know of r's runner now, and
// whether other work still needs r's branch or PR.
func (d *Dispatcher) WorkspaceRunnerOf(ctx context.Context, r Record) WorkspaceRunner {
	var out WorkspaceRunner
	if runnerwire.RunFinal(r.Status) && r.Workspace == runnerwire.RunWorkspaceLive {
		inUse, err := d.store.WorkspaceInUse(ctx, r)
		if err != nil {
			slog.Warn("runs: is the run's branch in use", slog.String("run", r.ID), slog.Any("error", err))
		}
		// Unknown counts as in use: a discard can wait.
		out.InUse = inUse || err != nil
	}
	if d.reg == nil {
		return out
	}
	out.Online = d.reg.Online(r.RunnerID)
	if runner, err := d.reg.Get(ctx, r.RunnerID); err == nil && runner.Info != nil {
		out.Caps = runner.Info.Capabilities
	}
	return out
}

// WorkspaceAction records the owner's request to merge a run's branch into
// its base on the runner (merge_local, at headSHA, the run commit they saw)
// or to discard its worktree and branch, and hands it to the run's runner.
// The caller has checked by is the runner's owner and a person.
//
// A discard of a run that never had a worktree is done here at once; one of
// a run already discarded changes nothing.
func (d *Dispatcher) WorkspaceAction(ctx context.Context, runID string, by Approver, action, headSHA string) (Record, error) {
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	runner := d.WorkspaceRunnerOf(ctx, r)
	if err := CheckWorkspaceAction(r, runner, action, headSHA); err != nil {
		return r, err
	}
	gate := WorkspaceGate(r, runner)
	verb := workspaceVerb(action)
	detail := func(a runnerwire.RunWorkspaceAction) map[string]any {
		return map[string]any{"actionId": a.ID, "action": a.Action, "headSha": a.HeadSHA, "base": a.Base,
			"branch": r.Branch, "runner": r.RunnerID}
	}
	if action == runnerwire.WorkspaceDiscard && gate.DiscardDone {
		return r, nil
	}
	if action == runnerwire.WorkspaceDiscard && gate.DiscardHubOnly {
		done, a, err := d.store.MarkDiscarded(ctx, r.ID, by.ID, by.Name)
		if err != nil {
			return r, err
		}
		if a.ID != "" {
			d.record(AuditEntry{Project: r.Project, ActorID: by.ID, ActorName: by.Name,
				Action: "run.workspace.discard.request", Target: r.ID, Detail: detail(a)})
			d.record(AuditEntry{Project: r.Project, ActorID: by.ID, ActorName: by.Name,
				Action: "run.workspace.discard.done", Target: r.ID, Detail: detail(a)})
		}
		d.notifyChanged(done)
		return done, nil
	}
	// The run's commit either way: a merge merges it, a discard deletes
	// the branch only up to it (the runner refuses commits added on top).
	headSHA = r.Commit
	a, err := d.store.CreateWorkspaceAction(ctx, r.ID, action, headSHA, r.Base, by.ID, by.Name)
	if err != nil {
		return r, err
	}
	d.record(AuditEntry{Project: r.Project, ActorID: by.ID, ActorName: by.Name,
		Action: "run.workspace." + verb + ".request", Target: r.ID, Detail: detail(a)})
	d.sendWorkspaceAction(r.RunnerID, WorkspaceJob(r, a))
	latest, err := d.store.Get(ctx, r.ID)
	if err != nil {
		return r, err
	}
	d.notifyChanged(latest)
	return latest, nil
}

// waiter is the channel closed when action id is answered or expires.
func (d *Dispatcher) waiter(id string) chan struct{} {
	d.workspaceMu.Lock()
	defer d.workspaceMu.Unlock()
	if d.workspaceWaiters == nil {
		d.workspaceWaiters = map[string]chan struct{}{}
	}
	ch, ok := d.workspaceWaiters[id]
	if !ok {
		ch = make(chan struct{})
		d.workspaceWaiters[id] = ch
	}
	return ch
}

// answered wakes whoever waits on action id.
func (d *Dispatcher) answered(id string) {
	d.workspaceMu.Lock()
	ch, ok := d.workspaceWaiters[id]
	delete(d.workspaceWaiters, id)
	d.workspaceMu.Unlock()
	if ok {
		close(ch)
	}
}

// AwaitWorkspaceAction returns when action id is answered (or expired), or
// once wait has passed, or ctx ends: whichever is first.
func (d *Dispatcher) AwaitWorkspaceAction(ctx context.Context, id string, wait time.Duration) {
	ch := d.waiter(id)
	defer func() {
		d.workspaceMu.Lock()
		if d.workspaceWaiters[id] == ch {
			delete(d.workspaceWaiters, id)
		}
		d.workspaceMu.Unlock()
	}()
	// An answer that came before the wait began is in the store already.
	if a, err := d.store.WorkspaceAction(ctx, id); err != nil || a.Status != runnerwire.WorkspacePending {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// sendWorkspaceAction sends a job if the runner is online. A job that does
// not go out stays pending for the runner's next connection.
func (d *Dispatcher) sendWorkspaceAction(runnerID string, job runnerwire.WorkspaceActionJob) {
	if d.reg == nil || !d.reg.Online(runnerID) {
		return
	}
	if err := d.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceAction, RunID: job.RunID,
		WorkspaceAction: &job}); err != nil {
		slog.Warn("runs: send workspace action", slog.String("run", job.RunID), slog.Any("error", err))
	}
}

// sendPendingWorkspaceActions retires stale requests, then sends a runner
// that just connected the ones still waiting for it. One it already got
// before it dropped is sent again: the runner answers by action ID.
func (d *Dispatcher) sendPendingWorkspaceActions(ctx context.Context, runnerID string) {
	d.SweepWorkspaceActions(ctx)
	jobs, err := d.store.PendingWorkspaceActions(ctx, runnerID)
	if err != nil {
		slog.Warn("runs: read pending workspace actions", slog.String("runner", runnerID), slog.Any("error", err))
		return
	}
	for _, job := range jobs {
		d.sendWorkspaceAction(runnerID, job)
	}
}

// SweepWorkspaceActions expires requests their runner did not answer within
// WorkspaceActionTTL, records that, and announces the runs.
func (d *Dispatcher) SweepWorkspaceActions(ctx context.Context) {
	expired, err := d.store.ExpireWorkspaceActions(ctx)
	if err != nil {
		slog.Warn("runs: expire workspace actions", slog.Any("error", err))
	}
	for _, a := range expired {
		r, err := d.store.Get(ctx, a.RunID)
		if err != nil {
			continue
		}
		d.record(AuditEntry{Project: r.Project, ActorName: "hub", Action: "run.workspace." + workspaceVerb(a.Action) + ".expired",
			Target: r.ID, Detail: map[string]any{"actionId": a.ID, "action": a.Action, "runner": r.RunnerID, "reason": a.Reason}})
		d.notifyChanged(r)
		d.answered(a.ID)
	}
}

// WorkspaceResult applies a runner's answer to a workspace action. A result
// for an action that is not one of this runner's runs', or that was already
// answered, is refused.
func (d *Dispatcher) WorkspaceResult(ctx context.Context, runnerID string, outcome runnerwire.WorkspaceOutcome) error {
	r, a, err := d.store.FinishWorkspaceAction(ctx, runnerID, outcome)
	if err != nil {
		return err
	}
	// .failed is the audit panel's "refused attempt".
	result := "done"
	if a.Status != runnerwire.WorkspaceDone {
		result = "failed"
	}
	d.record(AuditEntry{
		Project: r.Project, ActorID: r.OwnerID, ActorName: r.RunnerName,
		Action: "run.workspace." + workspaceVerb(a.Action) + "." + result, Target: r.ID,
		Detail: map[string]any{"actionId": a.ID, "action": a.Action, "headSha": a.HeadSHA, "base": a.Base,
			"branch": r.Branch, "runner": runnerID, "code": a.Code, "reason": a.Reason,
			"mergeCommit": a.MergeCommit, "prClosed": outcome.PRClosed,
			"worktreeRemoved": outcome.WorktreeRemoved, "branchDeleted": outcome.BranchDeleted},
	})
	d.notifyChanged(r)
	d.notifyRun(r)
	d.answered(a.ID)
	return nil
}
