package autopilot

import (
	"fmt"
	"strings"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Deriving a task instance from its runs. The runs (and the pipeline they
// belong to) are the record of what happened; a task's state and stages are
// read off them every time, never kept up by hand, so a restart or a missed
// update cannot leave the two disagreeing.

// phase is where a task's work stands, for the orchestrator to act on.
type phase int

const (
	phaseIdle     phase = iota // nothing dispatched in this try
	phaseActive                // a run or pipeline is working
	phaseApproved              // the PR is approved (or needs no review)
	phaseMerged                // the PR is merged
	phaseFailed                // the try failed; reason says why
)

// observation is what derive reads: the task's runs in this try, oldest
// first (a pipeline's runs, or a plain run and its fallbacks), the pipeline
// if there is one, and the newest run with a pull request, merges attached.
type observation struct {
	runs     []runs.Record
	pipeline *runs.PipelineRecord
	prRun    *runs.Record
}

// derived is derive's answer.
type derived struct {
	phase  phase
	reason string
	// waitingReview: reviewers are at work; waitingHuman: the pipeline waits
	// for a person (K2's Pipeline.Human); queued: the latest run waits for a
	// runner.
	waitingReview bool
	waitingHuman  bool
	queued        bool
	// retryAt: the latest run waits, queued, for its agent's usage limit to
	// reset (runs/handoff.go). The task is waiting, not failed.
	retryAt   string
	ci        string
	prURL     string
	votes     []runnerwire.ReviewVote
	stages    []runnerwire.StageRecord
	latestRun string
}

// derive reads a try's runs. stamp is the time to give a stage the runs
// carry no time for.
func derive(obs observation, stamp string) derived {
	var out derived
	if len(obs.runs) == 0 {
		return out
	}
	// Stages the runs carry no time for are stamped with the pass's time. Run
	// stamps are strictly increasing and may run a few microseconds ahead of
	// the clock, so the pass's time is never taken earlier than the newest of
	// them: a stage seen now cannot sort before one that happened before it.
	for _, r := range obs.runs {
		for _, at := range []string{r.CreatedAt, r.StartedAt, r.FinishedAt} {
			if at > stamp {
				stamp = at
			}
		}
	}
	add := func(stage, status, at, runID string, round int, detail string) {
		if at == "" {
			at = stamp
		}
		out.stages = append(out.stages, runnerwire.StageRecord{
			Stage: stage, Status: status, At: at, RunID: runID, Round: round, Detail: detail,
		})
	}
	first := obs.runs[0]
	latest := obs.runs[len(obs.runs)-1]
	out.latestRun = latest.ID

	// queued: from the first offer until a run of this try starts.
	started := ""
	for _, r := range obs.runs {
		if r.StartedAt != "" {
			started = r.StartedAt
			break
		}
	}
	switch {
	case started != "":
		add(runnerwire.StageQueued, runnerwire.StageOK, started, first.ID, 0, "")
	case latest.Status == runnerwire.RunDeclined:
		add(runnerwire.StageQueued, runnerwire.StageFailed, latest.FinishedAt, latest.ID, 0, "declined: "+latest.Error)
	default:
		add(runnerwire.StageQueued, runnerwire.StageRunning, first.CreatedAt, first.ID, 0, "waiting for "+first.RunnerName)
	}

	round := 1
	var implement *runs.Record // the newest implement or revise run
	reviewRuns := map[int][]runs.Record{}
	maxRound := 1
	for i := range obs.runs {
		r := obs.runs[i]
		switch r.Kind {
		case runnerwire.JobReview:
			reviewRuns[round] = append(reviewRuns[round], r)
		case runnerwire.JobRevise:
			if r.RetryOf == "" {
				round++
			}
			implement = &obs.runs[i]
		case runnerwire.JobPlan, runnerwire.JobVerify, runnerwire.JobJudge:
			// A plan run is never a task's; verify and judge runs change
			// nothing (stages below).
		default:
			implement = &obs.runs[i]
		}
		maxRound = max(maxRound, round)
	}

	// implement (round 1) and revise (later rounds), each the newest attempt.
	for i := range obs.runs {
		r := obs.runs[i]
		if r.Kind != runnerwire.JobImplement && r.Kind != runnerwire.JobRevise {
			continue
		}
		if next := retriedBy(obs.runs, r.ID); next != "" {
			continue // a fallback took over; its stage stands for this one
		}
		stage, rnd := runnerwire.StageImplement, 1
		if r.Kind == runnerwire.JobRevise {
			stage, rnd = runnerwire.StageRevise, roundOf(obs.runs, i)
		}
		detail := r.Agent
		if r.Model != "" {
			detail += "/" + r.Model
		}
		switch r.Status {
		case runnerwire.RunQueued, runnerwire.RunOffered:
		case runnerwire.RunRunning:
			add(stage, runnerwire.StageRunning, r.StartedAt, r.ID, rnd, detail)
		case runnerwire.RunSucceeded:
			add(stage, runnerwire.StageOK, r.FinishedAt, r.ID, rnd, detail)
		default:
			add(stage, runnerwire.StageFailed, r.FinishedAt, r.ID, rnd, firstNonEmpty(r.Error, r.Status))
		}
	}

	// Staged pipelines (agent roles): the Tester's rounds, the red and green
	// verify runs and a story's checklist judge.
	for _, r := range obs.runs {
		stage, detail := "", ""
		switch r.Kind {
		case runnerwire.JobTest:
			stage, detail = runnerwire.StageTest, firstNonEmpty(r.Agent, "tester")
		case runnerwire.JobVerify:
			stage = runnerwire.StageVerifyRed
			if r.VerifySpec != nil && r.VerifySpec.Phase == runnerwire.VerifyGreen {
				stage = runnerwire.StageVerifyGreen
			}
			if r.Verify != nil {
				detail = fmt.Sprintf("exit %d", r.Verify.ExitCode)
			}
		case runnerwire.JobJudge:
			stage, detail = runnerwire.StageJudge, r.Verdict
		default:
			continue
		}
		if retriedBy(obs.runs, r.ID) != "" {
			continue
		}
		switch r.Status {
		case runnerwire.RunQueued, runnerwire.RunOffered:
		case runnerwire.RunRunning:
			add(stage, runnerwire.StageRunning, r.StartedAt, r.ID, 0, detail)
		case runnerwire.RunSucceeded:
			add(stage, runnerwire.StageOK, r.FinishedAt, r.ID, 0, detail)
		default:
			add(stage, runnerwire.StageFailed, r.FinishedAt, r.ID, 0, firstNonEmpty(r.Error, r.Status))
		}
	}

	if implement != nil && implement.Status == runnerwire.RunSucceeded {
		r := implement
		if r.Commit != "" {
			add(runnerwire.StageCommit, runnerwire.StageOK, r.FinishedAt, r.ID, 0, shortSHA(r.Commit))
			add(runnerwire.StagePush, runnerwire.StageOK, r.FinishedAt, r.ID, 0, r.Branch)
		} else if r.Kind == runnerwire.JobImplement {
			add(runnerwire.StageCommit, runnerwire.StageFailed, r.FinishedAt, r.ID, 0, "the run changed nothing")
		}
	}
	prRun := obs.prRun
	if prRun == nil {
		for i := len(obs.runs) - 1; i >= 0; i-- {
			if obs.runs[i].PRURL != "" {
				prRun = &obs.runs[i]
				break
			}
		}
	}
	if prRun != nil {
		out.prURL = prRun.PRURL
		out.ci = prRun.CIStatus
		add(runnerwire.StagePR, runnerwire.StageOK, prRun.FinishedAt, prRun.ID, 0, prRun.PRURL)
		switch prRun.CIStatus {
		case runnerwire.CIPending:
			add(runnerwire.StageCI, runnerwire.StageRunning, "", prRun.ID, 0, "checks running")
		case runnerwire.CISuccess:
			add(runnerwire.StageCI, runnerwire.StageOK, "", prRun.ID, 0, "checks passed")
		case runnerwire.CIFailure:
			add(runnerwire.StageCI, runnerwire.StageFailed, "", prRun.ID, 0, "checks failed")
		}
	} else if implement != nil && implement.Status == runnerwire.RunSucceeded && implement.PRError != "" {
		add(runnerwire.StagePR, runnerwire.StageFailed, implement.FinishedAt, implement.ID, 0, implement.PRError)
	}

	// review rounds: one vote per reviewer run.
	for rnd := 1; rnd <= maxRound; rnd++ {
		reviews := reviewRuns[rnd]
		if len(reviews) == 0 {
			continue
		}
		running, approve, changes := false, 0, 0
		at := ""
		for _, r := range reviews {
			if retriedBy(obs.runs, r.ID) != "" {
				continue
			}
			v := runnerwire.ReviewVote{Agent: r.Agent, Model: r.Model, RunID: r.ID, Round: rnd}
			switch r.Status {
			case runnerwire.RunSucceeded:
				v.Verdict = r.Verdict
			case runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning:
				running = true
			}
			switch v.Verdict {
			case runnerwire.VerdictApprove:
				approve++
			case runnerwire.VerdictChanges:
				changes++
			}
			at = firstNonEmpty(r.FinishedAt, r.StartedAt, at)
			out.votes = append(out.votes, v)
		}
		detail := fmt.Sprintf("%d approve, %d changes", approve, changes)
		switch {
		case running:
			add(runnerwire.StageReview, runnerwire.StageRunning, at, "", rnd, detail)
			if rnd == maxRound {
				out.waitingReview = true
			}
		case changes > 0 && approve == 0:
			add(runnerwire.StageReview, runnerwire.StageFailed, at, "", rnd, detail+": changes requested")
		default:
			add(runnerwire.StageReview, runnerwire.StageOK, at, "", rnd, detail)
		}
	}

	switch latest.Status {
	case runnerwire.RunQueued, runnerwire.RunOffered:
		out.queued = true
		if latest.Status == runnerwire.RunQueued {
			out.retryAt = latest.RetryAt
		}
	}

	// The try's outcome.
	if p := obs.pipeline; p != nil {
		switch p.Status {
		case runnerwire.PipelineApproved:
			out.phase = phaseApproved
		case runnerwire.PipelineChangesLeft:
			out.phase, out.reason = phaseFailed, firstNonEmpty(p.Reason, "changes still requested when the review rounds ran out")
		case runnerwire.PipelineFailed:
			out.phase, out.reason = phaseFailed, firstNonEmpty(p.Reason, "the pipeline failed")
		case runnerwire.PipelineCancelled:
			out.phase, out.reason = phaseFailed, firstNonEmpty(p.Reason, "the pipeline was cancelled")
		case pipelineWaitingHuman:
			out.phase, out.waitingHuman = phaseActive, true
			add(runnerwire.StageHuman, runnerwire.StageWaiting, "", "", p.Round, "waiting for a person")
		default:
			out.phase = phaseActive
		}
	} else {
		r := latest
		switch {
		case !runnerwire.RunFinal(r.Status):
			out.phase = phaseActive
		case r.Status != runnerwire.RunSucceeded:
			out.phase, out.reason = phaseFailed, "run "+r.ID+" "+r.Status+": "+firstNonEmpty(r.Error, r.Status)
		case r.PRURL != "":
			out.phase = phaseApproved
		case r.PRError != "":
			out.phase, out.reason = phaseFailed, "no pull request: "+r.PRError
		case r.Commit == "":
			out.phase, out.reason = phaseFailed, "the run changed nothing"
		default:
			out.phase = phaseActive // committed, the PR report is on its way
		}
	}

	// The merge, on the run that carries the PR.
	if prRun != nil {
		if prRun.PRState == runnerwire.PRMerged {
			add(runnerwire.StageMerge, runnerwire.StageOK, "", prRun.ID, 0, "merged")
			out.phase = phaseMerged
		} else if m := prRun.Merge; m != nil {
			switch m.Status {
			case runnerwire.MergePending:
				add(runnerwire.StageMerge, runnerwire.StageRunning, m.CreatedAt, prRun.ID, 0, "approved by "+m.RequestedBy)
			case runnerwire.MergeDone:
				add(runnerwire.StageMerge, runnerwire.StageOK, firstNonEmpty(m.FinishedAt, m.CreatedAt), prRun.ID, 0, "merged")
				out.phase = phaseMerged
			default:
				add(runnerwire.StageMerge, runnerwire.StageFailed, firstNonEmpty(m.FinishedAt, m.CreatedAt), prRun.ID, 0,
					m.Status+": "+m.Reason)
			}
		}
	}
	return out
}

// pipelineWaitingHuman is a pipeline's status while it waits for a person's
// review (Pipeline.Human).
const pipelineWaitingHuman = "waiting_human"

// retriedBy is the run that fell back from id, if any.
func retriedBy(all []runs.Record, id string) string {
	for _, r := range all {
		if r.RetryOf == id {
			return r.ID
		}
	}
	return ""
}

// roundOf is the review round run i belongs to: one more for every revise
// run (not a fallback of one) up to and including it.
func roundOf(all []runs.Record, i int) int {
	round := 1
	for j := 0; j <= i; j++ {
		if all[j].Kind == runnerwire.JobRevise && all[j].RetryOf == "" {
			round++
		}
	}
	return round
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// keepTimes gives each new stage record the time an identical earlier one
// had, so a stage the runs carry no time for keeps the moment it was first
// seen rather than moving on every pass.
func keepTimes(prev, next []runnerwire.StageRecord) []runnerwire.StageRecord {
	// A stage that moved on (queued → success) keeps what it said before
	// when the new record says nothing: the queued stage's detail is why the
	// advisor picked the implementer.
	for i := range next {
		if next[i].Detail != "" {
			continue
		}
		for _, p := range prev {
			if p.Stage == next[i].Stage && p.Round == next[i].Round && p.RunID == next[i].RunID && p.Detail != "" {
				next[i].Detail = p.Detail
			}
		}
	}
	for i := range next {
		for _, p := range prev {
			if p.Stage == next[i].Stage && p.Round == next[i].Round && p.Status == next[i].Status && p.RunID == next[i].RunID {
				next[i].At = p.At
				break
			}
		}
	}
	return next
}
