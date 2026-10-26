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
	// A usage limit with no fallback left: wait for it to reset (handoff.go).
	if d.waitForReset(ctx, r) {
		return
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
		// The next model continues from the partial work, if any was kept.
		branch, handoffNote := handoffOf(r)
		next, err := d.store.Create(ctx, New{
			Project: r.Project, NodeID: r.NodeID, NodeTitle: r.NodeTitle,
			RunnerID: r.RunnerID, RunnerName: runner.Name, OwnerID: r.OwnerID,
			Agent: head.Agent, Model: model, RepoID: r.RepoID, RepoName: r.RepoName, Base: r.Base,
			Prompt: r.Prompt, JobPrompt: r.JobPrompt, CreatedBy: r.CreatedBy, CreatedByName: r.CreatedByName,
			Kind: r.Kind, JobBranch: r.JobBranch, JobPRURL: r.JobPRURL, Review: r.Review,
			RetryOf: r.ID, PipelineID: r.PipelineID, Fallbacks: chain,
			SteppedUpAt:      r.SteppedUpAt,
			JobHandoffBranch: branch, JobHandoffNote: handoffNote, LimitWaits: r.LimitWaits,
		})
		if err != nil {
			d.note(ctx, r, "not falling back: "+err.Error())
			return false
		}
		if r.Kind == runnerwire.JobReview {
			// The reviewer's seat moves with it: the retry's verdict is its vote.
			if err := d.store.MoveVote(ctx, r.ID, next.ID); err != nil {
				slog.Warn("runs: move vote", slog.String("run", r.ID), slog.Any("error", err))
			}
		}
		text := fmt.Sprintf("falling back to %s/%s (%s) as run %s", head.Agent, model, r.FailureKind, next.ID)
		if branch != "" {
			text += ", handing off the partial work on " + branch
		}
		d.note(ctx, r, text)
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
	if p.Status == runnerwire.PipelineWaitingHuman {
		return // a person decides next; no run of this round is pending
	}
	// A review run that failed is a reviewer abstaining, not the pipeline
	// failing: the others' votes may still carry the round.
	if r.Kind == runnerwire.JobReview && r.Status == runnerwire.RunFailed {
		// Only a failure of the review itself (no verdict, a session cut
		// short) is worth a second try on the same agent; a quota or a
		// missing model would fail the same way.
		if r.RetryOf == "" && r.FailureKind == runnerwire.FailureTask && d.retryReview(ctx, r) {
			return
		}
		d.reviewEnded(ctx, p, r, notified)
		return
	}
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
			d.startReviews(ctx, p, r, notified)
		case r.PRError != "":
			end(runnerwire.PipelineFailed, "the implement run opened no pull request: "+r.PRError)
		case r.Commit == "":
			end(runnerwire.PipelineFailed, "the implement run changed nothing, so there is nothing to review")
		default:
			// Committed, PR not reported yet: the pr message moves it on.
		}
	case runnerwire.JobRevise:
		d.startReviews(ctx, p, r, notified)
	case runnerwire.JobReview:
		d.reviewEnded(ctx, p, r, notified)
	}
}

// startReviews starts a round's reviews on the pipeline's PR: one review run
// per reviewer, in parallel, each with a pending vote. With no reviewers the
// round goes straight to a person, or, without one, the pipeline is approved
// as it stands (implement only). The caller holds the pipeline's lock.
func (d *Dispatcher) startReviews(ctx context.Context, p PipelineRecord, from Record, notified bool) {
	reviewers := runnerwire.ReviewersOf(p.Pipeline)
	if len(reviewers) == 0 {
		if p.Pipeline.Human {
			d.awaitHuman(ctx, p, from, notified)
			return
		}
		d.endPipeline(ctx, p, from, runnerwire.PipelineApproved, "no review asked for; the pull request is open", notified)
		return
	}
	started := make([]Record, 0, len(reviewers))
	for i, am := range reviewers {
		next, ok := d.prepareStep(ctx, p, from, runnerwire.JobReview, am, "", notified)
		if !ok {
			// The pipeline failed; the reviews already queued have nothing
			// left to decide.
			for _, run := range started {
				if _, err := d.Cancel(ctx, run.ID, "the pipeline failed"); err != nil {
					slog.Warn("runs: cancel review", slog.String("run", run.ID), slog.Any("error", err))
				}
			}
			return
		}
		if err := d.store.StartVote(ctx, p.ID, p.Round, i, runnerwire.AgentModel{Agent: am.Agent, Model: next.Model}, next.ID); err != nil {
			slog.Warn("runs: start vote", slog.String("pipeline", p.ID), slog.Any("error", err))
		}
		started = append(started, next)
	}
	for _, next := range started {
		d.note(ctx, from, fmt.Sprintf("pipeline round %d: review by %s/%s as run %s", p.Round, next.Agent, next.Model, next.ID))
		d.Submit(ctx, next)
	}
	d.pipelineChanged(ctx, p.ID, from)
}

// retryReview runs a review that failed once more, on the same agent and
// model, before it counts as an abstention: a reviewer that ended without a
// verdict (its session cut short, a last line in the wrong form) usually
// gives one on a second try, and one abstention can sink a round. It
// reports whether it queued the retry. The caller holds the pipeline's lock.
func (d *Dispatcher) retryReview(ctx context.Context, r Record) bool {
	runner, err := d.reg.Get(ctx, r.RunnerID)
	if err != nil {
		return false
	}
	budget, err := d.store.BudgetStatus(ctx, r.OwnerID)
	if err != nil || budget.Exceeded != "" {
		return false
	}
	next, err := d.store.Create(ctx, New{
		Project: r.Project, NodeID: r.NodeID, NodeTitle: r.NodeTitle,
		RunnerID: r.RunnerID, RunnerName: runner.Name, OwnerID: r.OwnerID,
		Agent: r.Agent, Model: r.Model, RepoID: r.RepoID, RepoName: r.RepoName, Base: r.Base,
		Prompt: r.Prompt, JobPrompt: r.JobPrompt, CreatedBy: r.CreatedBy, CreatedByName: r.CreatedByName,
		Kind: r.Kind, JobBranch: r.JobBranch, JobPRURL: r.JobPRURL, Review: r.Review,
		RetryOf: r.ID, PipelineID: r.PipelineID,
		SteppedUpAt: r.SteppedUpAt,
	})
	if err != nil {
		d.note(ctx, r, "not retrying the review: "+err.Error())
		return false
	}
	if err := d.store.MoveVote(ctx, r.ID, next.ID); err != nil {
		slog.Warn("runs: move vote", slog.String("run", r.ID), slog.Any("error", err))
	}
	d.note(ctx, r, "the review ended without a verdict; trying it once more as run "+next.ID)
	d.Submit(ctx, next)
	return true
}

// reviewEnded records a review run's vote and, once the round's last vote is
// in, decides the round: approved (or on to a person) when Quorum reviewers
// approve, else revise with the reviews that asked for changes, or stop when
// the rounds ran out. The caller holds the pipeline's lock.
func (d *Dispatcher) reviewEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	end := func(status, reason string) { d.endPipeline(ctx, p, r, status, reason, notified) }
	verdict := runnerwire.VerdictAbstain
	if r.Status == runnerwire.RunSucceeded && CleanVerdict(r.Verdict) != "" {
		verdict = r.Verdict
	}
	settled, err := d.store.SettleVote(ctx, r.ID, verdict)
	if err != nil {
		slog.Warn("runs: settle vote", slog.String("run", r.ID), slog.Any("error", err))
		return
	}
	if !settled {
		// A review this pipeline never recorded a vote for (one started
		// before W7): its verdict alone decides, as it did then.
		d.legacyReviewEnded(ctx, p, r, notified)
		return
	}
	votes, err := d.store.RoundVotes(ctx, p.ID, p.Round)
	if err != nil {
		slog.Warn("runs: read votes", slog.String("pipeline", p.ID), slog.Any("error", err))
		return
	}
	var approvals, pending int
	var approvers []string
	var changes []Vote
	for _, v := range votes {
		if v.Reviewer == HumanReviewerKey {
			continue
		}
		switch v.Verdict {
		case "":
			pending++
		case runnerwire.VerdictApprove:
			approvals++
			approvers = append(approvers, label(runnerwire.AgentModel{Agent: v.Agent, Model: v.Model}))
		case runnerwire.VerdictChanges:
			changes = append(changes, v)
		}
	}
	d.pipelineChanged(ctx, p.ID, r)
	if pending > 0 {
		return
	}
	reviewers := len(runnerwire.ReviewersOf(p.Pipeline))
	needed := runnerwire.QuorumOf(p.Pipeline)
	tally := fmt.Sprintf("%d of %d approved, %d needed", approvals, reviewers, needed)
	if approvals >= needed {
		if p.Pipeline.Human {
			d.awaitHuman(ctx, p, r, notified)
			return
		}
		end(runnerwire.PipelineApproved, "approved by "+strings.Join(approvers, ", ")+" ("+tally+")")
		return
	}
	if len(changes) == 0 {
		end(runnerwire.PipelineFailed, "too few reviews approved and none asked for changes ("+tally+")")
		return
	}
	if p.Round >= p.Pipeline.MaxRounds {
		end(runnerwire.PipelineChangesLeft, fmt.Sprintf("changes still requested after %d round(s) (%s)", p.Round, tally))
		return
	}
	moved, err := d.store.NextRound(ctx, p.ID, p.Round)
	if err != nil || !moved {
		return
	}
	p.Round++
	d.step(ctx, p, r, runnerwire.JobRevise, p.Pipeline.Implement, d.combinedReview(ctx, changes), notified)
}

// legacyReviewEnded is W5's rule for a review with no vote: its verdict
// decides the pipeline.
func (d *Dispatcher) legacyReviewEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	end := func(status, reason string) { d.endPipeline(ctx, p, r, status, reason, notified) }
	if r.Status != runnerwire.RunSucceeded {
		end(runnerwire.PipelineFailed, "run "+r.ID+" failed: "+r.Error)
		return
	}
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

// combinedReview is what a revise run addresses: every review that asked for
// changes, each under its reviewer's name.
func (d *Dispatcher) combinedReview(ctx context.Context, changes []Vote) string {
	text := func(v Vote) string {
		if v.RunID != "" {
			if run, err := d.store.Get(ctx, v.RunID); err == nil {
				return run.Summary
			}
		}
		return v.Comment
	}
	// One review is the review, as in W5: no heading to address.
	if len(changes) == 1 && changes[0].Reviewer != HumanReviewerKey {
		return text(changes[0])
	}
	var b strings.Builder
	for i, v := range changes {
		text := text(v)
		if i > 0 {
			b.WriteString("\n\n")
		}
		who := label(runnerwire.AgentModel{Agent: v.Agent, Model: v.Model})
		if v.Reviewer == HumanReviewerKey {
			who = v.Model + " (a person)"
		}
		fmt.Fprintf(&b, "Review %d of %d, by %s:\n%s", i+1, len(changes), who, strings.TrimSpace(text))
	}
	return b.String()
}

// awaitHuman has the pipeline wait for a person's review. The caller holds
// the pipeline's lock.
func (d *Dispatcher) awaitHuman(ctx context.Context, p PipelineRecord, from Record, notified bool) {
	ok, err := d.store.AwaitHuman(ctx, p.ID, p.Round)
	if err != nil {
		slog.Warn("runs: await human", slog.String("pipeline", p.ID), slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	d.note(ctx, from, fmt.Sprintf("pipeline round %d: waiting for a person to review %s", p.Round, p.PRURL))
	d.pipelineChanged(ctx, p.ID, from)
	d.notifyChanged(from)
}

// pipelineChanged tells observers the pipeline's current state, read back
// from the store, with r as the run it concerns.
func (d *Dispatcher) pipelineChanged(ctx context.Context, id string, r Record) {
	p, err := d.store.Pipeline(ctx, id)
	if err != nil {
		slog.Warn("runs: read pipeline", slog.String("pipeline", id), slog.Any("error", err))
		return
	}
	d.observed(Update{Run: r, Pipeline: &p})
}

// Reviewer is the person behind a human review.
type Reviewer struct{ ID, Name string }

// HumanReview records a person's verdict on a pipeline waiting for one:
// approve ends it approved; changes starts a revise round with the comment as
// the review, or ends it with changes left when the rounds ran out. A
// pipeline not waiting for a person is ErrPrecondition.
func (d *Dispatcher) HumanReview(ctx context.Context, id string, who Reviewer, verdict, comment string) (PipelineRecord, error) {
	comment = strings.TrimSpace(comment)
	if verdict == runnerwire.VerdictChanges && comment == "" {
		return PipelineRecord{}, invalidf("say what to change: a review asking for changes needs a comment")
	}
	unlock := lockPipeline(id)
	defer unlock()
	p, err := d.store.Pipeline(ctx, id)
	if err != nil {
		return PipelineRecord{}, err
	}
	if p.Status != runnerwire.PipelineWaitingHuman {
		return PipelineRecord{}, fmt.Errorf("%w: the pipeline is not waiting for a person's review", ErrPrecondition)
	}
	runs, err := d.store.PipelineRuns(ctx, id)
	if err != nil {
		return PipelineRecord{}, err
	}
	if len(runs) == 0 {
		return PipelineRecord{}, fmt.Errorf("%w: the pipeline has no runs", ErrPrecondition)
	}
	last := runs[len(runs)-1]
	if err := d.store.HumanVote(ctx, p.ID, p.Round, who.ID, who.Name, verdict, comment); err != nil {
		return PipelineRecord{}, err
	}
	switch verdict {
	case runnerwire.VerdictApprove:
		d.endPipeline(ctx, p, last, runnerwire.PipelineApproved, "approved by "+who.Name, false)
	case runnerwire.VerdictChanges:
		if p.Round >= p.Pipeline.MaxRounds {
			d.endPipeline(ctx, p, last, runnerwire.PipelineChangesLeft,
				fmt.Sprintf("%s asked for changes after %d round(s)", who.Name, p.Round), false)
			break
		}
		moved, err := d.store.NextRound(ctx, p.ID, p.Round)
		if err != nil {
			return PipelineRecord{}, err
		}
		if moved {
			p.Round++
			review := fmt.Sprintf("Review by %s (a person):\n%s", who.Name, comment)
			d.step(ctx, p, last, runnerwire.JobRevise, p.Pipeline.Implement, review, false)
		}
	}
	return d.store.Pipeline(ctx, id)
}

// step queues the pipeline's next run, on the same runner and repository as
// the run before it. If it cannot, the pipeline fails.
func (d *Dispatcher) step(ctx context.Context, p PipelineRecord, from Record, kind string, am runnerwire.AgentModel, review string, notified bool) {
	next, ok := d.prepareStep(ctx, p, from, kind, am, review, notified)
	if !ok {
		return
	}
	d.note(ctx, from, fmt.Sprintf("pipeline round %d: %s step on %s/%s as run %s", p.Round, stepName(kind), am.Agent, next.Model, next.ID))
	d.Submit(ctx, next)
	d.pipelineChanged(ctx, p.ID, from)
}

// prepareStep records the pipeline's next run without offering it. If it
// cannot, the pipeline fails and ok is false.
func (d *Dispatcher) prepareStep(ctx context.Context, p PipelineRecord, from Record, kind string, am runnerwire.AgentModel, review string, notified bool) (Record, bool) {
	fail := func(reason string) {
		d.endPipeline(ctx, p, from, runnerwire.PipelineFailed, reason, notified)
	}
	runner, err := d.reg.Get(ctx, from.RunnerID)
	if err != nil {
		fail("the runner is gone")
		return Record{}, false
	}
	model, err := ResolveAgentModel(runner.Info, am)
	if err != nil {
		fail(fmt.Sprintf("cannot start the %s step: %v", stepName(kind), err))
		return Record{}, false
	}
	budget, err := d.store.BudgetStatus(ctx, from.OwnerID)
	if err != nil {
		fail("cannot read the budget: " + err.Error())
		return Record{}, false
	}
	if budget.Exceeded != "" {
		fail(BudgetMessage(budget))
		return Record{}, false
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
		SteppedUpAt: from.SteppedUpAt,
	})
	if err != nil {
		fail("cannot start the next step: " + err.Error())
		return Record{}, false
	}
	return next, true
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
	p.Status, p.Reason = status, reason
	d.observed(Update{Run: r, Pipeline: &p})
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
