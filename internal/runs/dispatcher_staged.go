package runs

// The staged task pipeline (agent roles, spec 3.2): test → verify_red →
// implement → verify_green → guard → ci → review → done, each stage optional
// per the workflow the pipeline froze when it was made. Like every pipeline
// step, each stage is an ordinary run its runner still has to accept; a
// verify run executes the repository's own test command on the runner, never
// on the hub, and no agent or model takes part in it.

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// maxReviewOutput caps how much of a verify run's output a revise prompt
// quotes.
const maxReviewOutput = 4 << 10

// stagedStep is one run a staged pipeline queues.
type stagedStep struct {
	kind, role, phase string
	am                runnerwire.AgentModel
	review            string
	verify            *runnerwire.VerifySpec
	// noPR runs on no pull request (a story's writer and judge).
	noPR bool
}

func shortHead(head string) string {
	if len(head) > 7 {
		return head[:7]
	}
	return head
}

// tail keeps the last n bytes of a verify run's output.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return "…" + s[start:]
}

// workflowOf is the pipeline's frozen workflow with its levers' defaults.
func workflowOf(p PipelineRecord) *engine.Workflow {
	if p.Config == nil || p.Config.Workflow == nil {
		return (&engine.Workflow{}).Normalized()
	}
	return p.Config.Workflow.Normalized()
}

// advanceStaged moves a staged pipeline on from one of its runs ending. The
// caller holds the pipeline's lock; p is running.
func (d *Dispatcher) advanceStaged(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	if p.Status == runnerwire.PipelineWaitingHuman {
		return // a person decides next
	}
	end := func(status, reason string) { d.endPipeline(ctx, p, r, status, reason, notified) }
	if r.Kind == runnerwire.JobReview && r.Status == runnerwire.RunFailed {
		if r.RetryOf == "" && r.FailureKind == runnerwire.FailureTask && d.retryReview(ctx, r) {
			return
		}
		d.stagedReviewEnded(ctx, p, r, notified)
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
	case runnerwire.JobTest:
		d.testerEnded(ctx, p, r, notified)
	case runnerwire.JobVerify:
		d.verifyEnded(ctx, p, r, notified)
	case runnerwire.JobImplement, runnerwire.JobRevise:
		d.executorEnded(ctx, p, r, notified)
	case runnerwire.JobReview:
		d.stagedReviewEnded(ctx, p, r, notified)
	case runnerwire.JobJudge:
		d.judgeEnded(ctx, p, r, notified)
	}
}

// saveStaged writes p's staged state; false when the pipeline stopped running
// meanwhile.
func (d *Dispatcher) saveStaged(ctx context.Context, p *PipelineRecord, phase string) bool {
	p.Phase = phase
	ok, err := d.store.SetStaged(ctx, p.ID, phase, p.TestFiles, p.Guard, p.Tries)
	if err != nil {
		slog.Warn("runs: save staged pipeline", slog.String("pipeline", p.ID), slog.Any("error", err))
		return false
	}
	return ok
}

// stagedHuman has the pipeline wait for a person, saying why.
func (d *Dispatcher) stagedHuman(ctx context.Context, p PipelineRecord, from Record, reason string) {
	ok, err := d.store.AwaitHumanReason(ctx, p.ID, p.Round, reason)
	if err != nil {
		slog.Warn("runs: await human", slog.String("pipeline", p.ID), slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	d.note(ctx, from, "pipeline waiting for a person: "+reason)
	d.pipelineChanged(ctx, p.ID, from)
	d.notifyChanged(from)
	d.notifyRun(from)
}

// stagedRun queues one step of a staged pipeline on the runner and
// repository of from. If it cannot, the pipeline fails and ok is false.
func (d *Dispatcher) stagedRun(ctx context.Context, p PipelineRecord, from Record, step stagedStep, notified bool) (Record, bool) {
	fail := func(reason string) {
		d.endPipeline(ctx, p, from, runnerwire.PipelineFailed, reason, notified)
	}
	runner, err := d.reg.Get(ctx, from.RunnerID)
	if err != nil {
		fail("the runner is gone")
		return Record{}, false
	}
	model := ""
	if step.kind != runnerwire.JobVerify {
		model, err = ResolveAgentModel(runner.Info, step.am)
		if err != nil {
			fail(fmt.Sprintf("cannot start the %s step: %v", step.kind, err))
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
		if step.role != "" {
			status, err := d.store.RoleBudgetStatus(ctx, from.OwnerID, step.role)
			if err != nil {
				fail("cannot read the role budget: " + err.Error())
				return Record{}, false
			}
			if status.Exceeded != "" {
				fail(RoleBudgetMessage(step.role, status))
				return Record{}, false
			}
		}
	}
	jobPrompt := p.JobPrompt
	var fallbacks []runnerwire.AgentModel
	switch step.kind {
	case runnerwire.JobVerify:
		jobPrompt, step.am = "", runnerwire.AgentModel{}
	case runnerwire.JobRevise, runnerwire.JobImplement, runnerwire.JobTest:
		if step.review != "" {
			jobPrompt = RevisePrompt(p.JobPrompt, step.review)
		}
		fallbacks = p.Fallbacks
	}
	jobRoles := JobRoles{VerifySpec: step.verify}
	if step.role != "" && p.Config != nil {
		jobRoles.Tools = p.Config.Tools[step.role]
		jobRoles.Acceptance = p.Config.Acceptance
	}
	switch {
	case step.role == engine.RoleExecutor && step.kind == runnerwire.JobRevise:
		jobRoles.TestFiles = p.TestFiles
	case step.kind == runnerwire.JobReview && p.Guard != nil && p.Guard.Violation():
		// The reviewer is told which protected files the Executor changed.
		protected := append(slices.Clone(p.Guard.Touched), p.Guard.Sensitive...)
		jobRoles.TestFiles = protected
	}
	branch, prURL := p.Branch, p.PRURL
	if step.noPR {
		branch, prURL = "", ""
	}
	next, err := d.store.Create(ctx, New{
		Project: from.Project, NodeID: from.NodeID, NodeTitle: from.NodeTitle,
		RunnerID: from.RunnerID, RunnerName: runner.Name, OwnerID: from.OwnerID,
		Agent: step.am.Agent, Model: model, RepoID: from.RepoID, RepoName: from.RepoName, Base: from.Base,
		Prompt: p.Prompt, JobPrompt: jobPrompt, CreatedBy: from.CreatedBy, CreatedByName: from.CreatedByName,
		Kind: step.kind, JobBranch: branch, JobPRURL: prURL, Review: step.review,
		PipelineID: p.ID, Fallbacks: fallbacks, SteppedUpAt: from.SteppedUpAt,
		Mode: runnerwire.ModeHeadless, Role: step.role, JobRoles: jobRoles,
	})
	if err != nil {
		fail("cannot start the next step: " + err.Error())
		return Record{}, false
	}
	return next, true
}

// stagedStepNow saves the phase, queues the step and announces it.
func (d *Dispatcher) stagedStepNow(ctx context.Context, p PipelineRecord, from Record, step stagedStep, notified bool) {
	if !d.saveStaged(ctx, &p, step.phase) {
		return
	}
	next, ok := d.stagedRun(ctx, p, from, step, notified)
	if !ok {
		return
	}
	who := step.kind
	if step.am.Agent != "" {
		who += " by " + label(runnerwire.AgentModel{Agent: step.am.Agent, Model: next.Model})
	}
	d.note(ctx, from, fmt.Sprintf("pipeline %s: %s as run %s", step.phase, who, next.ID))
	d.Submit(ctx, next)
	d.pipelineChanged(ctx, p.ID, from)
}

func testerOf(p PipelineRecord) runnerwire.AgentModel {
	if p.Pipeline.Tester.Agent != "" {
		return p.Pipeline.Tester
	}
	return p.Pipeline.Implement
}

// executorStep is the Executor's next try on the pipeline's PR.
func executorStep(p PipelineRecord, review string) stagedStep {
	if p.Config != nil && p.Config.Story {
		return stagedStep{kind: runnerwire.JobImplement, role: engine.RoleExecutor, phase: runnerwire.PhaseImplement,
			am: p.Pipeline.Implement, review: review, noPR: true}
	}
	return stagedStep{kind: runnerwire.JobRevise, role: engine.RoleExecutor, phase: runnerwire.PhaseImplement,
		am: p.Pipeline.Implement, review: review}
}

// testerEnded checks a Tester round: it must have opened (or pushed to) the
// pipeline's PR and changed test files only; then the tests are run, and
// must fail.
func (d *Dispatcher) testerEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	if r.PRURL == "" {
		switch {
		case r.PRError != "":
			d.endPipeline(ctx, p, r, runnerwire.PipelineFailed, "the Tester's run opened no pull request: "+r.PRError, notified)
		case r.Commit == "":
			d.testerRetry(ctx, p, r, "you wrote no tests: write tests for the acceptance criteria that fail until they are implemented", notified)
		}
		return // committed, PR not reported yet: the pr message moves it on
	}
	if p.PRURL != r.PRURL {
		// The first round's PR, or a later round's own (each Tester round
		// starts its branch from the base again).
		moved, err := d.store.SetPipelinePR(ctx, p.ID, r.Branch, r.PRURL)
		if err != nil || !moved {
			return
		}
		if p.PRURL != "" {
			d.note(ctx, r, "the Tester's new round replaces pull request "+p.PRURL+" with "+r.PRURL)
		}
		p.Branch, p.PRURL = Truncate(r.Branch, maxShortField), Truncate(r.PRURL, MaxPRURLBytes)
	}
	changes, err := d.store.Changes(ctx, r.ID)
	if err != nil {
		d.endPipeline(ctx, p, r, runnerwire.PipelineFailed, "cannot read the Tester's changes: "+err.Error(), notified)
		return
	}
	if changes == nil || len(changes.Files) == 0 {
		d.testerRetry(ctx, p, r, "you wrote no tests: write tests for the acceptance criteria that fail until they are implemented", notified)
		return
	}
	if bad := NonTestFiles(workflowOf(p).TestGlobs, changes.Files); len(bad) > 0 {
		d.testerRetry(ctx, p, r, fmt.Sprintf(
			"tester changed non-test files %s: write tests only (files matching the test globs) and leave the implementation to the Executor",
			strings.Join(bad, ", ")), notified)
		return
	}
	// This round's branch starts from the base: its files are the tests.
	p.TestFiles = changedPaths(changes.Files)
	head := r.PRHeadSHA
	p.Tries.RedHead = head
	d.stagedStepNow(ctx, p, r, stagedStep{kind: runnerwire.JobVerify, phase: runnerwire.PhaseVerifyRed,
		verify: &runnerwire.VerifySpec{Phase: runnerwire.VerifyRed, Head: head}}, notified)
}

// testerRetry gives the Tester another round, or a person the pipeline when
// the rounds ran out.
func (d *Dispatcher) testerRetry(ctx context.Context, p PipelineRecord, r Record, review string, notified bool) {
	p.Tries.Tester++
	if p.Tries.Tester >= workflowOf(p).MaxTesterRounds {
		if !d.saveStaged(ctx, &p, runnerwire.PhaseTest) {
			return
		}
		d.stagedHuman(ctx, p, r, fmt.Sprintf("the Tester's round %d failed: %s", p.Tries.Tester, review))
		return
	}
	// A new round starts its own branch and draft PR from the base: the
	// runner never reopens a Tester's.
	d.stagedStepNow(ctx, p, r, stagedStep{kind: runnerwire.JobTest, role: engine.RoleTester, phase: runnerwire.PhaseTest,
		am: testerOf(p), review: review, noPR: true}, notified)
}

// verifyEnded applies a red or green verify run's result. The hub decides
// from the exit code: red passes when the tests fail.
func (d *Dispatcher) verifyEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	phase, head := runnerwire.VerifyRed, ""
	if r.VerifySpec != nil {
		phase, head = r.VerifySpec.Phase, r.VerifySpec.Head
	}
	res := r.Verify
	if res == nil {
		d.stagedHuman(ctx, p, r, "the verify run reported no test result")
		return
	}
	if res.Head != "" {
		head = res.Head
	}
	kind := runnerwire.PhaseVerifyRed
	if phase == runnerwire.VerifyGreen {
		kind = runnerwire.PhaseVerifyGreen
	}
	if res.NoCommand {
		d.recordEvidence(r, kind, "no test command on the runner")
		d.stagedHuman(ctx, p, r, "the runner has no test command for this repository; configure a test command (nodevas runner repo set <repo> --test-command \"...\") and continue")
		return
	}
	d.recordEvidence(r, kind, fmt.Sprintf("exit %d in %.1fs at %s", res.ExitCode, float64(res.DurationMs)/1000, shortHead(head)))
	output := tail(res.Output, maxReviewOutput)
	switch phase {
	case runnerwire.VerifyRed:
		if res.ExitCode == 0 {
			d.testerRetry(ctx, p, r, "your tests already pass at "+shortHead(head)+
				": they test nothing new. Write tests that fail until the acceptance criteria are implemented.", notified)
			return
		}
		review := fmt.Sprintf("The Tester's tests fail as they should (red at %s). Make them pass without changing them.", shortHead(head))
		if output != "" {
			review += "\n\nRed run output:\n" + output
		}
		d.stagedStepNow(ctx, p, r, executorStep(p, review), notified)
	default:
		if res.ExitCode != 0 {
			p.Tries.Green++
			if p.Tries.Green >= workflowOf(p).MaxGreenTries {
				d.endPipeline(ctx, p, r, runnerwire.PipelineChangesLeft,
					fmt.Sprintf("the tests still fail after %d tries", p.Tries.Green), notified)
				return
			}
			review := fmt.Sprintf("The tests still fail at %s (exit %d). Make them pass without changing them.", shortHead(head), res.ExitCode)
			if output != "" {
				review += "\n\nTest output:\n" + output
			}
			d.stagedStepNow(ctx, p, r, executorStep(p, review), notified)
			return
		}
		p.Tries.GreenHead = head
		d.guardStep(ctx, p, r, notified)
	}
}

// executorEnded follows an Executor's run: the tests are run again (green),
// or for a story the checklist is judged.
func (d *Dispatcher) executorEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	if p.Config.Story {
		d.stagedStepNow(ctx, p, r, stagedStep{kind: runnerwire.JobJudge, role: engine.RoleReviewer, phase: runnerwire.PhaseJudge,
			am: judgeOf(&p.Pipeline), noPR: true}, notified)
		return
	}
	if r.PRURL == "" && r.PRHeadSHA == "" {
		switch {
		case r.PRError != "":
			d.endPipeline(ctx, p, r, runnerwire.PipelineFailed, "the Executor's run has no pull request: "+r.PRError, notified)
			return
		case r.Commit != "":
			return // committed, PR not reported yet: the pr message moves it on
		}
	}
	if p.PRURL == "" && r.PRURL != "" {
		claimed, err := d.store.ClaimPR(ctx, p.ID, r.Branch, r.PRURL)
		if err != nil || !claimed {
			return
		}
		p.Branch, p.PRURL = Truncate(r.Branch, maxShortField), Truncate(r.PRURL, MaxPRURLBytes)
	}
	head := r.PRHeadSHA
	if head == "" {
		head = p.Tries.RedHead
	}
	if workflowOf(p).Stages.Tester {
		d.stagedStepNow(ctx, p, r, stagedStep{kind: runnerwire.JobVerify, phase: runnerwire.PhaseVerifyGreen,
			verify: &runnerwire.VerifySpec{Phase: runnerwire.VerifyGreen, Head: head}}, notified)
		return
	}
	p.Tries.GreenHead = head
	d.guardStep(ctx, p, r, notified)
}

// executorChanges is every file the pipeline's Executor runs changed, and
// how many lines.
func (d *Dispatcher) executorChanges(ctx context.Context, p PipelineRecord) ([]runnerwire.FileChange, int, error) {
	runs, err := d.store.PipelineRuns(ctx, p.ID)
	if err != nil {
		return nil, 0, err
	}
	var files []runnerwire.FileChange
	lines := 0
	for _, run := range runs {
		if run.Role != engine.RoleExecutor || run.Status != runnerwire.RunSucceeded {
			continue
		}
		ch, err := d.store.Changes(ctx, run.ID)
		if err != nil {
			return nil, 0, err
		}
		if ch == nil {
			continue
		}
		files = append(files, ch.Files...)
		lines += ch.Additions + ch.Deletions
	}
	return files, lines, nil
}

// guardStep checks the Executor's diff against the Tester's files and test
// infrastructure, then moves to CI, review or approval.
func (d *Dispatcher) guardStep(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	files, lines, err := d.executorChanges(ctx, p)
	if err != nil {
		d.endPipeline(ctx, p, r, runnerwire.PipelineFailed, "cannot read the Executor's changes: "+err.Error(), notified)
		return
	}
	report := Guard(p.TestFiles, files)
	// A test file the Executor added or changed beside the Tester's (a Go
	// TestMain that exits 0, a skip-all setup file) can make the test command
	// pass without the work: it needs the same sign-off as a touched one.
	for _, extra := range executorTestFiles(workflowOf(p).TestGlobs, p.TestFiles, files) {
		if !slices.Contains(report.Sensitive, extra) {
			report.Sensitive = append(report.Sensitive, extra)
		}
	}
	slices.Sort(report.Sensitive)
	if p.Guard != nil && p.Guard.Approved && slices.Equal(p.Guard.Touched, report.Touched) &&
		slices.Equal(p.Guard.Sensitive, report.Sensitive) {
		report.Approved, report.ApprovedBy = true, p.Guard.ApprovedBy
	}
	p.Guard = &report
	summary := "clean"
	if report.Violation() {
		var parts []string
		if len(report.Touched) > 0 {
			parts = append(parts, "tester files changed: "+strings.Join(report.Touched, ", "))
		}
		if len(report.Sensitive) > 0 {
			parts = append(parts, "sensitive: "+strings.Join(report.Sensitive, ", "))
		}
		summary = strings.Join(parts, "; ")
	}
	d.recordEvidence(r, runnerwire.PhaseGuard, summary)
	wf := workflowOf(p)
	if report.Violation() && !report.Approved && !wf.Stages.Reviewer {
		if !d.saveStaged(ctx, &p, runnerwire.PhaseGuard) {
			return
		}
		d.stagedHuman(ctx, p, r, "executor changed tester files or test infrastructure ("+summary+"); a person must approve or send it back")
		return
	}
	if wf.Stages.CI {
		if !d.saveStaged(ctx, &p, runnerwire.PhaseCI) {
			return
		}
		// CI may already have reported on the green head.
		if runs, err := d.store.PipelineRuns(ctx, p.ID); err == nil {
			for i := len(runs) - 1; i >= 0; i-- {
				if runs[i].PRHeadSHA == p.Tries.GreenHead && p.Tries.GreenHead != "" &&
					(runs[i].CIStatus == runnerwire.CISuccess || runs[i].CIStatus == runnerwire.CIFailure) {
					d.ciDecided(ctx, p, runs[i], notified)
					return
				}
			}
		}
		d.pipelineChanged(ctx, p.ID, r)
		return
	}
	d.afterCI(ctx, p, r, lines, notified)
}

// ciReported moves a staged pipeline waiting in its CI phase when CI on the
// green head settles. CI on any other head does not count.
func (d *Dispatcher) ciReported(ctx context.Context, r Record) {
	if r.PipelineID == "" || (r.CIStatus != runnerwire.CISuccess && r.CIStatus != runnerwire.CIFailure) {
		return
	}
	unlock := lockPipeline(r.PipelineID)
	defer unlock()
	p, err := d.store.Pipeline(ctx, r.PipelineID)
	if err != nil || p.Config == nil || !p.Config.Staged || p.Status != runnerwire.PipelineRunning || p.Phase != runnerwire.PhaseCI {
		return
	}
	if p.Tries.GreenHead == "" || r.PRHeadSHA != p.Tries.GreenHead {
		return
	}
	d.ciDecided(ctx, p, r, false)
}

// ciDecided applies CI's result on the green head.
func (d *Dispatcher) ciDecided(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	d.recordEvidence(r, runnerwire.PhaseCI, r.CIStatus+" at "+shortHead(r.PRHeadSHA))
	if r.CIStatus == runnerwire.CIFailure {
		p.Tries.CI++
		if p.Tries.CI >= workflowOf(p).MaxGreenTries {
			d.endPipeline(ctx, p, r, runnerwire.PipelineChangesLeft, fmt.Sprintf("CI still fails after %d tries", p.Tries.CI), notified)
			return
		}
		d.stagedStepNow(ctx, p, r, executorStep(p, "CI failed at "+shortHead(r.PRHeadSHA)+
			": read the failing checks on the pull request and fix them without changing the Tester's tests."), notified)
		return
	}
	_, lines, err := d.executorChanges(ctx, p)
	if err != nil {
		d.endPipeline(ctx, p, r, runnerwire.PipelineFailed, "cannot read the Executor's changes: "+err.Error(), notified)
		return
	}
	d.afterCI(ctx, p, r, lines, notified)
}

// afterCI starts the review, or approves when there is none (or the change
// is small enough to skip it).
func (d *Dispatcher) afterCI(ctx context.Context, p PipelineRecord, r Record, lines int, notified bool) {
	wf := workflowOf(p)
	if !wf.Stages.Reviewer {
		d.stagedApprove(ctx, p, r, "verified red then green; no review asked for", notified)
		return
	}
	risky := p.Guard != nil && (p.Guard.Violation())
	if wf.ReviewerSkipBelowLines > 0 && lines < wf.ReviewerSkipBelowLines && !risky {
		d.recordEvidence(r, runnerwire.PhaseReview, fmt.Sprintf("skipped: %d changed lines, under %d", lines, wf.ReviewerSkipBelowLines))
		d.stagedApprove(ctx, p, r, fmt.Sprintf("verified red then green; review skipped (%d lines)", lines), notified)
		return
	}
	if !d.saveStaged(ctx, &p, runnerwire.PhaseReview) {
		return
	}
	d.startStagedReviews(ctx, p, r, notified)
}

// stagedApprove ends the pipeline approved, or hands it to a person first.
func (d *Dispatcher) stagedApprove(ctx context.Context, p PipelineRecord, r Record, reason string, notified bool) {
	if p.Pipeline.Human {
		if !d.saveStaged(ctx, &p, runnerwire.PhaseHuman) {
			return
		}
		d.awaitHuman(ctx, p, r, notified)
		return
	}
	d.endPipeline(ctx, p, r, runnerwire.PipelineApproved, reason, notified)
}

// guardNotice tells a reviewer which protected files the Executor changed.
func guardNotice(g *runnerwire.GuardReport) string {
	if g == nil || !g.Violation() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Protected files changed by the Executor:\n")
	for _, p := range g.Touched {
		b.WriteString("- " + p + " (written by the Tester)\n")
	}
	for _, p := range g.Sensitive {
		b.WriteString("- " + p + " (test or build infrastructure)\n")
	}
	b.WriteString("Approve these changes only if they are justified, with a final line: TESTS: approve-changes")
	return b.String()
}

// startStagedReviews starts a round's reviews, each run in the reviewer role,
// told about any protected files the Executor changed.
func (d *Dispatcher) startStagedReviews(ctx context.Context, p PipelineRecord, from Record, notified bool) {
	reviewers := runnerwire.ReviewersOf(p.Pipeline)
	if len(reviewers) == 0 {
		reviewers = []runnerwire.AgentModel{p.Pipeline.Implement}
	}
	notice := guardNotice(p.Guard)
	started := make([]Record, 0, len(reviewers))
	for i, am := range reviewers {
		next, ok := d.stagedRun(ctx, p, from, stagedStep{kind: runnerwire.JobReview, role: engine.RoleReviewer,
			phase: runnerwire.PhaseReview, am: am, review: notice}, notified)
		if !ok {
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

// stagedReviewEnded records a reviewer's vote. An approval of an Executor
// that changed protected files counts only with the "TESTS: approve-changes"
// acknowledgement; without it, it asks for changes.
func (d *Dispatcher) stagedReviewEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	end := func(status, reason string) { d.endPipeline(ctx, p, r, status, reason, notified) }
	verdict := runnerwire.VerdictAbstain
	if r.Status == runnerwire.RunSucceeded {
		switch CleanVerdict(r.Verdict) {
		case runnerwire.VerdictApprove, runnerwire.VerdictChanges:
			verdict = r.Verdict
		}
	}
	unacknowledged := false
	if verdict == runnerwire.VerdictApprove && p.Guard != nil && p.Guard.Violation() && !p.Guard.Approved {
		if r.GuardAck {
			p.Guard.Approved = true
			p.Guard.ApprovedBy = label(runnerwire.AgentModel{Agent: r.Agent, Model: r.Model})
			d.recordEvidence(r, runnerwire.PhaseGuard, "protected changes approved by "+p.Guard.ApprovedBy)
			if !d.saveStaged(ctx, &p, p.Phase) {
				return
			}
		} else {
			verdict, unacknowledged = runnerwire.VerdictChanges, true
			d.note(ctx, r, "the approval does not acknowledge the changed tester files (TESTS: approve-changes): counted as changes")
		}
	}
	settled, err := d.store.SettleVote(ctx, r.ID, verdict)
	if err != nil {
		slog.Warn("runs: settle vote", slog.String("run", r.ID), slog.Any("error", err))
		return
	}
	if !settled {
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
	if reviewers == 0 {
		reviewers = 1
	}
	needed := runnerwire.QuorumOf(p.Pipeline)
	if needed == 0 {
		needed = reviewers
	}
	tally := fmt.Sprintf("%d of %d approved, %d needed", approvals, reviewers, needed)
	if approvals >= needed {
		d.recordEvidence(r, runnerwire.PhaseReview, "approved by "+strings.Join(approvers, ", "))
		d.stagedApprove(ctx, p, r, "verified red then green; approved by "+strings.Join(approvers, ", ")+" ("+tally+")", notified)
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
	review := d.combinedReview(ctx, changes)
	if unacknowledged || (p.Guard != nil && p.Guard.Violation() && !p.Guard.Approved) {
		review = strings.TrimSpace(review + "\n\nThe reviewer did not approve your changes to protected files (" +
			strings.Join(append(slices.Clone(p.Guard.Touched), p.Guard.Sensitive...), ", ") +
			"). Revert them, or make the case for them in your summary.")
	}
	d.stagedStepNow(ctx, p, r, executorStep(p, review), notified)
}

// judgeEnded applies a checklist judge's verdict: every item passes, or the
// writer revises with the failing items as the review.
func (d *Dispatcher) judgeEnded(ctx context.Context, p PipelineRecord, r Record, notified bool) {
	var failing []string
	for _, check := range r.Checks {
		if !check.Pass {
			item := check.ID
			if note := strings.TrimSpace(check.Note); note != "" {
				item += ": " + note
			}
			failing = append(failing, item)
		}
	}
	pass := r.Verdict == runnerwire.VerdictPass && len(failing) == 0
	if pass {
		d.recordEvidence(r, runnerwire.PhaseJudge, fmt.Sprintf("all %d criteria pass", len(r.Checks)))
		d.stagedApprove(ctx, p, r, "checklist judged: every criterion passes", notified)
		return
	}
	if len(failing) == 0 {
		failing = append(failing, "the judge gave no passing verdict")
	}
	d.recordEvidence(r, runnerwire.PhaseJudge, "failing: "+strings.Join(failing, "; "))
	p.Tries.Green++
	if p.Tries.Green >= workflowOf(p).MaxGreenTries {
		d.endPipeline(ctx, p, r, runnerwire.PipelineChangesLeft,
			fmt.Sprintf("checklist still failing after %d tries: %s", p.Tries.Green, strings.Join(failing, "; ")), notified)
		return
	}
	d.stagedStepNow(ctx, p, r, executorStep(p, "Checklist items failing:\n- "+strings.Join(failing, "\n- ")), notified)
}

// stagedApproved tells the server a staged pipeline was approved, with the
// evidence that got it there: it marks the node done.
func (d *Dispatcher) stagedApproved(ctx context.Context, p PipelineRecord, r Record, reason string) {
	if d.onApproved == nil {
		return
	}
	var parts []string
	if p.Tries.RedHead != "" {
		parts = append(parts, "red at "+shortHead(p.Tries.RedHead))
	}
	if p.Tries.GreenHead != "" {
		parts = append(parts, "green at "+shortHead(p.Tries.GreenHead))
	} else if p.Config != nil && !p.Config.Story && workflowOf(p).Stages.Tester {
		parts = append(parts, "green")
	}
	if p.Guard != nil {
		if p.Guard.Violation() {
			parts = append(parts, "protected changes approved by "+p.Guard.ApprovedBy)
		} else {
			parts = append(parts, "guard clean")
		}
	}
	parts = append(parts, reason)
	d.onApproved(p.Project, p.NodeID, strings.Join(parts, "; "))
}
