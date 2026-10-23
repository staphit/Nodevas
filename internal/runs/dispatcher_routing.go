package runs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"nodevas/internal/runnerwire"
)

// Fallback and pipelines (W5). When a run ends, the dispatcher may queue the
// next one: the same task on the next model of its fallback chain, or the
// next step of its pipeline. Each is an ordinary run, offered through the
// normal queue, that its runner still has to accept.

// pipelineLocks serialises the steps of one pipeline, so a run ending and a
// cancel (or a PR report) cannot both move it.
var pipelineLocks sync.Map // pipeline ID → *sync.Mutex

func lockPipeline(id string) func() {
	value, _ := pipelineLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// note records a hub-side log line on a run and announces it.
func (d *Dispatcher) note(ctx context.Context, r Record, text string) {
	if err := d.store.Note(ctx, r.ID, text); err != nil {
		slog.Warn("runs: note", slog.String("run", r.ID), slog.Any("error", err))
		return
	}
	d.notifyEvents(r)
}

// afterEnd runs once per run reaching an end state, whichever way it got
// there. notified says whether the run's own end was already announced as a
// notification.
func (d *Dispatcher) afterEnd(ctx context.Context, r Record, notified bool) {
	if !runnerwire.RunFinal(r.Status) {
		return
	}
	if r.PipelineID != "" {
		unlock := lockPipeline(r.PipelineID)
		defer unlock()
	}
	if r.Status == runnerwire.RunFailed && runnerwire.FallbackWorthy(r.FailureKind) && len(r.Fallbacks) > 0 {
		if d.fallback(ctx, r) {
			return
		}
	}
	if r.PipelineID != "" {
		d.advance(ctx, r, notified)
	}
}

// fallback queues the run again on the next usable entry of its chain. It
// reports whether it did; if not, the reason is noted on the run.
func (d *Dispatcher) fallback(ctx context.Context, r Record) bool {
	if r.PipelineID != "" {
		p, err := d.store.Pipeline(ctx, r.PipelineID)
		if err != nil || p.Status != runnerwire.PipelineRunning {
			return false
		}
	}
	runner, err := d.reg.Get(ctx, r.RunnerID)
	if err != nil {
		d.note(ctx, r, "not falling back: the runner is gone")
		return false
	}
	budget, err := d.store.BudgetStatus(ctx, r.OwnerID)
	if err != nil {
		slog.Warn("runs: fallback budget", slog.String("run", r.ID), slog.Any("error", err))
		return false
	}
	if budget.Exceeded != "" {
		d.note(ctx, r, "not falling back: "+BudgetMessage(budget))
		return false
	}
	chain := r.Fallbacks
	for len(chain) > 0 {
		head := chain[0]
		chain = chain[1:]
		model, err := ResolveAgentModel(runner.Info, head)
		if err != nil {
			d.note(ctx, r, fmt.Sprintf("skipping fallback %s: %v", label(head), err))
			continue
		}
		next, err := d.store.Create(ctx, New{
			Project: r.Project, NodeID: r.NodeID, NodeTitle: r.NodeTitle,
			RunnerID: r.RunnerID, RunnerName: runner.Name, OwnerID: r.OwnerID,
			Agent: head.Agent, Model: model, RepoID: r.RepoID, RepoName: r.RepoName, Base: r.Base,
			Prompt: r.Prompt, JobPrompt: r.JobPrompt, CreatedBy: r.CreatedBy, CreatedByName: r.CreatedByName,
			Kind: r.Kind, JobBranch: r.JobBranch, JobPRURL: r.JobPRURL, Review: r.Review,
			RetryOf: r.ID, PipelineID: r.PipelineID, Fallbacks: chain,
		})
		if err != nil {
			d.note(ctx, r, "not falling back: "+err.Error())
			return false
		}
		d.note(ctx, r, fmt.Sprintf("falling back to %s/%s (%s) as run %s", head.Agent, model, r.FailureKind, next.ID))
		d.Submit(ctx, next)
		return true
	}
	d.note(ctx, r, "fallback chain exhausted")
	return false
}

func label(am runnerwire.AgentModel) string {
	if am.Model == "" {
		return am.Agent
	}
	return am.Agent + "/" + am.Model
}

// PRReported moves a pipeline whose implement run ended before its pull
// request was reported. Called with the run as it stands after the report.
func (d *Dispatcher) prReported(ctx context.Context, r Record) {
	if r.PipelineID == "" || r.Kind != runnerwire.JobImplement || r.Status != runnerwire.RunSucceeded {
		return
	}
	unlock := lockPipeline(r.PipelineID)
	defer unlock()
	d.advance(ctx, r, false)
}

// advance moves a pipeline on from one of its runs ending. The caller holds
// the pipeline's lock.
func (d *Dispatcher) advance(ctx context.Context, r Record, notified bool) {
	p, err := d.store.Pipeline(ctx, r.PipelineID)
	if err != nil {
		slog.Warn("runs: read pipeline", slog.String("pipeline", r.PipelineID), slog.Any("error", err))
		return
	}
	if p.Status != runnerwire.PipelineRunning {
		return
	}
	end := func(status, reason string) { d.endPipeline(ctx, p, r, status, reason, notified) }
	switch r.Status {
	case runnerwire.RunCancelled:
		end(runnerwire.PipelineCancelled, "run "+r.ID+" was cancelled")
		return
	case runnerwire.RunDeclined:
		end(runnerwire.PipelineFailed, "run "+r.ID+" was declined: "+r.Error)
		return
	case runnerwire.RunFailed:
		end(runnerwire.PipelineFailed, "run "+r.ID+" failed: "+r.Error)
		return
	case runnerwire.RunSucceeded:
	default:
		return
	}
	switch r.Kind {
	case runnerwire.JobImplement:
		switch {
		case r.PRURL != "":
			claimed, err := d.store.ClaimPR(ctx, p.ID, r.Branch, r.PRURL)
			if err != nil || !claimed {
				return // already moved on from this PR
			}
			p.Branch, p.PRURL = Truncate(r.Branch, maxShortField), Truncate(r.PRURL, MaxPRURLBytes)
			d.step(ctx, p, r, runnerwire.JobReview, p.Pipeline.Review, "", notified)
		case r.PRError != "":
			end(runnerwire.PipelineFailed, "the implement run opened no pull request: "+r.PRError)
		case r.Commit == "":
			end(runnerwire.PipelineFailed, "the implement run changed nothing, so there is nothing to review")
		default:
			// Committed, PR not reported yet: the pr message moves it on.
		}
	case runnerwire.JobRevise:
		d.step(ctx, p, r, runnerwire.JobReview, p.Pipeline.Review, "", notified)
	case runnerwire.JobReview:
		switch r.Verdict {
		case runnerwire.VerdictApprove:
			end(runnerwire.PipelineApproved, "approved by "+label(runnerwire.AgentModel{Agent: r.Agent, Model: r.Model}))
		case runnerwire.VerdictChanges:
			if p.Round >= p.Pipeline.MaxRounds {
				end(runnerwire.PipelineChangesLeft, fmt.Sprintf("changes still requested after %d round(s)", p.Round))
				return
			}
			moved, err := d.store.NextRound(ctx, p.ID, p.Round)
			if err != nil || !moved {
				return
			}
			p.Round++
			d.step(ctx, p, r, runnerwire.JobRevise, p.Pipeline.Implement, r.Summary, notified)
		default:
			end(runnerwire.PipelineFailed, "the review gave no verdict")
		}
	}
}

// step queues the pipeline's next run, on the same runner and repository as
// the run before it. If it cannot, the pipeline fails.
func (d *Dispatcher) step(ctx context.Context, p PipelineRecord, from Record, kind string, am runnerwire.AgentModel, review string, notified bool) {
	fail := func(reason string) {
		d.endPipeline(ctx, p, from, runnerwire.PipelineFailed, reason, notified)
	}
	runner, err := d.reg.Get(ctx, from.RunnerID)
	if err != nil {
		fail("the runner is gone")
		return
	}
	model, err := ResolveAgentModel(runner.Info, am)
	if err != nil {
		fail(fmt.Sprintf("cannot start the %s step: %v", stepName(kind), err))
		return
	}
	budget, err := d.store.BudgetStatus(ctx, from.OwnerID)
	if err != nil {
		fail("cannot read the budget: " + err.Error())
		return
	}
	if budget.Exceeded != "" {
		fail(BudgetMessage(budget))
		return
	}
	jobPrompt := p.JobPrompt
	var fallbacks []runnerwire.AgentModel
	if kind == runnerwire.JobRevise {
		jobPrompt = RevisePrompt(p.JobPrompt, review)
		fallbacks = p.Fallbacks
	}
	next, err := d.store.Create(ctx, New{
		Project: from.Project, NodeID: from.NodeID, NodeTitle: from.NodeTitle,
		RunnerID: from.RunnerID, RunnerName: runner.Name, OwnerID: from.OwnerID,
		Agent: am.Agent, Model: model, RepoID: from.RepoID, RepoName: from.RepoName, Base: from.Base,
		Prompt: p.Prompt, JobPrompt: jobPrompt, CreatedBy: from.CreatedBy, CreatedByName: from.CreatedByName,
		Kind: kind, JobBranch: p.Branch, JobPRURL: p.PRURL, Review: review,
		PipelineID: p.ID, Fallbacks: fallbacks,
	})
	if err != nil {
		fail("cannot start the next step: " + err.Error())
		return
	}
	d.note(ctx, from, fmt.Sprintf("pipeline round %d: %s step on %s/%s as run %s", p.Round, stepName(kind), am.Agent, model, next.ID))
	d.Submit(ctx, next)
}

func stepName(kind string) string {
	if kind == runnerwire.JobImplement {
		return runnerwire.StepImplement
	}
	return kind
}

// RevisePrompt is a revise run's task: the original task and, after it, the
// review to address. The review is never the part that is cut.
func RevisePrompt(base, review string) string {
	tail := Truncate("\n\nReview to address:\n"+strings.TrimSpace(review), runnerwire.MaxPromptBytes/2)
	return Truncate(base, runnerwire.MaxPromptBytes-len(tail)) + tail
}

// endPipeline ends a running pipeline, notes why on the run that ended it and
// announces it.
func (d *Dispatcher) endPipeline(ctx context.Context, p PipelineRecord, r Record, status, reason string, notified bool) {
	ended, err := d.store.EndPipeline(ctx, p.ID, status, reason)
	if err != nil {
		slog.Warn("runs: end pipeline", slog.String("pipeline", p.ID), slog.Any("error", err))
		return
	}
	if !ended {
		return
	}
	d.note(ctx, r, "pipeline "+status+": "+reason)
	d.notifyChanged(r)
	if !notified && status != runnerwire.PipelineCancelled {
		d.notifyRun(r)
	}
}

// CancelPipeline ends a running pipeline as cancelled and cancels its runs
// that have not ended. A pipeline that already ended is left as it is; its
// runs are still cancelled.
func (d *Dispatcher) CancelPipeline(ctx context.Context, id, reason string) error {
	unlock := lockPipeline(id)
	ended, err := d.store.EndPipeline(ctx, id, runnerwire.PipelineCancelled, reason)
	unlock()
	if err != nil {
		return err
	}
	runs, err := d.store.PipelineRuns(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if runnerwire.RunFinal(r.Status) {
			continue
		}
		if _, err := d.Cancel(ctx, r.ID, reason); err != nil {
			slog.Warn("runs: cancel pipeline run", slog.String("run", r.ID), slog.Any("error", err))
		}
	}
	if ended && len(runs) > 0 {
		last := runs[len(runs)-1]
		d.note(ctx, last, "pipeline cancelled: "+reason)
		d.notifyChanged(last)
	}
	return nil
}
