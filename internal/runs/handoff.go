package runs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nodevas/internal/runnerwire"
)

// Handoff (runnerwire/handoff.go). A headless implement run that stops at
// its agent's usage limit leaves its partial work on a local branch of its
// runner, and a note. Whatever runs the task next continues from there:
//
//   - the next model of the fallback chain, at once (dispatcher_routing.go);
//   - with no fallback left, the same agent and model once the limit resets:
//     a queued run with retry_at set, which Pump leaves alone until the
//     retry loop (or a person's "retry now") releases it. It is stored, so a
//     restart keeps the wait; it is an ordinary run, so cancelling it ends
//     the wait for good, and the budget and the runner's acceptance rules
//     apply to it as to any offer.
//
// The hub only names the branch. The runner made it and checks any branch a
// job names is one of its own before it uses it.

// canHandOff reports whether a run's work is handed on when it stops at a
// limit: headless implement runs only. A review or plan changes no files,
// and a revise already works on its pull request's branch.
func canHandOff(r Record) bool {
	return (r.Kind == runnerwire.JobImplement || r.Kind == runnerwire.JobTest) && r.Mode == runnerwire.ModeHeadless
}

// canWaitForReset reports whether a run stopped at a usage limit is queued
// again for when the limit resets: headless implement, test and revise runs
// (a revise starts again from its pull request's head).
func canWaitForReset(r Record) bool {
	switch r.Kind {
	case runnerwire.JobImplement, runnerwire.JobTest, runnerwire.JobRevise:
		return r.Mode == runnerwire.ModeHeadless
	}
	return false
}

// handoffOf is the handoff a follow-up of r continues from: the one r left,
// else the one r was itself given (a run that stopped before changing
// anything leaves the earlier work where it was).
func handoffOf(r Record) (branch, note string) {
	if !canHandOff(r) {
		return "", ""
	}
	if r.HandoffBranch != "" {
		return r.HandoffBranch, r.HandoffNote
	}
	return r.JobHandoffBranch, r.JobHandoffNote
}

func (d *Dispatcher) clockNow() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// resetTime is when r's limit should have lifted: what the runner read from
// the agent, else what the error text says in zone-free wording, else the
// default backoff; plus LimitResetSlack.
func resetTime(r Record, now time.Time) time.Time {
	if at, err := time.Parse(time.RFC3339, r.ResetAt); err == nil && at.After(now) && at.Sub(now) <= runnerwire.MaxLimitWait {
		return at.Add(runnerwire.LimitResetSlack)
	}
	if at, ok := runnerwire.ParseResetTime(r.Error, now, nil); ok {
		return at.Add(runnerwire.LimitResetSlack)
	}
	return now.Add(runnerwire.DefaultLimitBackoff(r.FailureKind))
}

// waitForReset queues r's task again on the same agent and model, to be
// offered once the usage limit resets, continuing from r's handoff. It
// reports whether it did; if not, the reason is noted on the run.
func (d *Dispatcher) waitForReset(ctx context.Context, r Record) bool {
	if r.Status != runnerwire.RunFailed || !runnerwire.HandoffWorthy(r.FailureKind) || !canWaitForReset(r) {
		return false
	}
	if r.PipelineID != "" {
		p, err := d.store.Pipeline(ctx, r.PipelineID)
		if err != nil || p.Status != runnerwire.PipelineRunning {
			return false
		}
	}
	if r.LimitWaits >= runnerwire.MaxLimitWaits {
		d.note(ctx, r, fmt.Sprintf("not waiting for the usage limit again: this task already waited %d times", r.LimitWaits))
		return false
	}
	runner, err := d.reg.Get(ctx, r.RunnerID)
	if err != nil {
		d.note(ctx, r, "not waiting for the usage limit: the runner is gone")
		return false
	}
	budget, err := d.store.BudgetStatus(ctx, r.OwnerID)
	if err != nil {
		slog.Warn("runs: limit wait budget", slog.String("run", r.ID), slog.Any("error", err))
		return false
	}
	if budget.Exceeded != "" {
		d.note(ctx, r, "not waiting for the usage limit: "+BudgetMessage(budget))
		return false
	}
	at := resetTime(r, d.clockNow())
	branch, handoffNote := handoffOf(r)
	next, err := d.store.Create(ctx, New{
		Project: r.Project, NodeID: r.NodeID, NodeTitle: r.NodeTitle,
		RunnerID: r.RunnerID, RunnerName: runner.Name, OwnerID: r.OwnerID,
		Agent: r.Agent, Model: r.Model, RepoID: r.RepoID, RepoName: r.RepoName, Base: r.Base,
		Prompt: r.Prompt, JobPrompt: r.JobPrompt, CreatedBy: r.CreatedBy, CreatedByName: r.CreatedByName,
		Kind: r.Kind, RetryOf: r.ID, PipelineID: r.PipelineID,
		JobBranch: r.JobBranch, JobPRURL: r.JobPRURL, Review: r.Review,
		SteppedUpAt:      r.SteppedUpAt,
		JobHandoffBranch: branch, JobHandoffNote: handoffNote,
		RetryAt: formatStamp(at), LimitWaits: r.LimitWaits + 1,
		Role: r.Role, JobRoles: r.JobRoles,
	})
	if err != nil {
		d.note(ctx, r, "not waiting for the usage limit: "+err.Error())
		return false
	}
	text := fmt.Sprintf("waiting for the usage limit to reset: run %s retries %s at %s",
		next.ID, label(runnerwire.AgentModel{Agent: r.Agent, Model: r.Model}), at.UTC().Format("2006-01-02 15:04 UTC"))
	if branch != "" {
		text += ", continuing from " + branch
	}
	d.note(ctx, r, text)
	d.Submit(ctx, next)
	return true
}

// RetryInterval is how often the dispatcher looks for waits that are over.
const RetryInterval = 15 * time.Second

// RunRetries releases the runs whose usage-limit wait is over, now and every
// interval, until ctx ends. The waits are in the database, so a hub that
// restarts picks them up here.
func (d *Dispatcher) RunRetries(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = RetryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		d.ReleaseDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReleaseDue releases every run whose wait is over and reports how many it
// offered again.
func (d *Dispatcher) ReleaseDue(ctx context.Context) int {
	due, err := d.store.DueRetries(ctx, d.clockNow())
	if err != nil {
		slog.Warn("runs: read due retries", slog.Any("error", err))
		return 0
	}
	n := 0
	for _, r := range due {
		if next, err := d.release(ctx, r, "the usage limit should have reset"); err == nil && next.Status != runnerwire.RunCancelled {
			n++
		}
	}
	return n
}

// RetryNow ends a run's usage-limit wait early, on a person's word. A run
// that is not waiting is ErrPrecondition.
func (d *Dispatcher) RetryNow(ctx context.Context, id, who string) (Record, error) {
	r, err := d.store.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if r.Status != runnerwire.RunQueued || r.RetryAt == "" {
		return r, fmt.Errorf("%w: the run is not waiting for a usage limit to reset", ErrPrecondition)
	}
	return d.release(ctx, r, "retried now by "+who)
}

// release ends r's wait: the run joins its runner's queue like any other and
// is offered when the runner has room. Over the budget, it is cancelled
// instead.
func (d *Dispatcher) release(ctx context.Context, r Record, why string) (Record, error) {
	budget, err := d.store.BudgetStatus(ctx, r.OwnerID)
	if err != nil {
		return r, err
	}
	if budget.Exceeded != "" {
		next, err := d.store.Transition(ctx, r.ID, runnerwire.RunCancelled, Change{
			Error: "not retried after the usage limit: " + BudgetMessage(budget),
		})
		if err != nil {
			return r, err
		}
		d.notifyChanged(next)
		d.afterEnd(ctx, next, false)
		return next, nil
	}
	released, err := d.store.ReleaseRetry(ctx, r.ID, r.RetryAt)
	if err != nil {
		return r, err
	}
	if !released {
		latest, err := d.store.Get(ctx, r.ID)
		if err != nil {
			return r, err
		}
		return latest, fmt.Errorf("%w: the run is no longer waiting", ErrTransition)
	}
	if r.RetryOf != "" {
		if prev, err := d.store.Get(ctx, r.RetryOf); err == nil {
			d.note(ctx, prev, fmt.Sprintf("%s: offering run %s again", why, r.ID))
		}
	}
	latest, err := d.store.Get(ctx, r.ID)
	if err != nil {
		return r, err
	}
	d.notifyChanged(latest)
	d.Pump(ctx, r.RunnerID)
	if after, err := d.store.Get(ctx, r.ID); err == nil {
		latest = after
	}
	return latest, nil
}

// DueRetries is the queued runs whose usage-limit wait is over at now,
// longest waiting first.
func (s *Store) DueRetries(ctx context.Context, now time.Time) ([]Record, error) {
	return s.query(ctx, `SELECT `+runColumns+` FROM runs WHERE status = ? AND retry_at <> '' AND retry_at <= ?
ORDER BY retry_at, id`, runnerwire.RunQueued, formatStamp(now))
}

// ReleaseRetry clears a queued run's wait, if it is still the one at, and
// reports whether it did: of two releases racing (the loop and a person),
// one wins.
func (s *Store) ReleaseRetry(ctx context.Context, id, at string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE runs SET retry_at = '' WHERE id = ? AND status = ? AND retry_at = ? AND retry_at <> ''`,
		id, runnerwire.RunQueued, at)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
