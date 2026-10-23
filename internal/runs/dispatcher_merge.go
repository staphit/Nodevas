package runs

import (
	"context"
	"log/slog"

	"nodevas/internal/runnerwire"
)

// Approver is who approves a merge.
type Approver struct{ ID, Name string }

// Merge records an approval to merge a run's PR at headSHA and hands it to
// the run's runner: at once if it is online, otherwise when it next connects
// (within MergeTTL). The caller has checked the approver may see the run.
func (d *Dispatcher) Merge(ctx context.Context, runID string, by Approver, headSHA string) (Record, error) {
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	if err := CheckMergeable(r, headSHA); err != nil {
		return r, err
	}
	m, err := d.store.CreateMerge(ctx, r.ID, r.PRHeadSHA, by.ID, by.Name)
	if err != nil {
		return r, err
	}
	d.record(AuditEntry{
		Project: r.Project, ActorID: by.ID, ActorName: by.Name, Action: "run.merge.approve", Target: r.ID,
		Detail: map[string]any{"mergeId": m.ID, "headSha": m.HeadSHA, "pr": r.PRURL, "runner": r.RunnerID},
	})
	d.sendMerge(r.RunnerID, runnerwire.MergeJob{ID: m.ID, RunID: r.ID, HeadSHA: m.HeadSHA})
	latest, err := d.store.Get(ctx, r.ID)
	if err != nil {
		return r, err
	}
	d.notifyChanged(latest)
	return latest, nil
}

// sendMerge sends a merge job if the runner is online. A job that does not
// go out stays pending for the runner's next connection.
func (d *Dispatcher) sendMerge(runnerID string, job runnerwire.MergeJob) {
	if d.reg == nil || !d.reg.Online(runnerID) {
		return
	}
	if err := d.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeMerge, Merge: &job}); err != nil {
		slog.Warn("runs: send merge", slog.String("run", job.RunID), slog.Any("error", err))
	}
}

// sendPendingMerges retires stale approvals, then sends a runner that just
// connected the approvals still waiting for it. One the runner already got
// before it dropped is sent again: the runner answers by merge ID.
func (d *Dispatcher) sendPendingMerges(ctx context.Context, runnerID string) {
	d.SweepMerges(ctx)
	jobs, err := d.store.PendingMerges(ctx, runnerID)
	if err != nil {
		slog.Warn("runs: read pending merges", slog.String("runner", runnerID), slog.Any("error", err))
		return
	}
	for _, job := range jobs {
		d.sendMerge(runnerID, job)
	}
}

// SweepMerges expires approvals their runner did not answer within MergeTTL
// and announces the runs they belonged to.
func (d *Dispatcher) SweepMerges(ctx context.Context) {
	runIDs, err := d.store.ExpireMerges(ctx)
	if err != nil {
		slog.Warn("runs: expire merges", slog.Any("error", err))
	}
	for _, id := range runIDs {
		if r, err := d.store.Get(ctx, id); err == nil {
			d.notifyChanged(r)
		}
	}
}

// MergeResult applies a runner's answer to a merge job. A result for a merge
// that is not one of this runner's runs', or that was already answered, is
// refused.
func (d *Dispatcher) MergeResult(ctx context.Context, runnerID string, outcome runnerwire.MergeOutcome) error {
	r, m, err := d.store.FinishMerge(ctx, runnerID, outcome)
	if err != nil {
		return err
	}
	d.record(AuditEntry{
		Project: r.Project, ActorID: r.OwnerID, ActorName: r.RunnerName, Action: "run.merge." + m.Status, Target: r.ID,
		Detail: map[string]any{"mergeId": m.ID, "headSha": m.HeadSHA, "reason": m.Reason, "runner": runnerID},
	})
	d.notifyChanged(r)
	d.notifyRun(r)
	return nil
}
