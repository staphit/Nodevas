package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

func continueHello(caps ...string) runnerwire.Info {
	if caps == nil {
		caps = []string{
			runnerwire.CapHeadless,
			runnerwire.CapInteractive,
			runnerwire.CapGlobalRuns,
			runnerwire.CapGlobalRunsOn,
			runnerwire.CapContinue,
			runnerwire.CapRunWorkspace,
			runnerwire.CapWorkspaceActions,
		}
	}
	return globalHello(caps...)
}

func startAndFinishGlobal(t *testing.T, f *stepUpFixture, task string, edit func(*GlobalStartRequest)) Record {
	t.Helper()
	ctx := context.Background()
	req := f.globalRequest(task)
	req.Body.Base = "main"
	if edit != nil {
		edit(&req)
	}
	r, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	branch := "agent/" + r.BranchSlug + "-" + r.ID[:8]
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: branch}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Changes(ctx, f.runner.ID, r.ID, sampleChanges()); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, r.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFollowUpContinuesTheSameWork(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Fix the login button", func(r *GlobalStartRequest) {
		r.Body.Model = "opus"
		r.Body.Slug = "login-btn"
		r.Body.OpenPR = true
	})

	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID:   root.ID,
		Body:      runnerwire.FollowUpRequest{Message: "Make the button bigger\n\nUse 16px padding."},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp: %v", err)
	}
	if !fu.Global || fu.ContinueOf != root.ID || fu.WorkRoot != root.ID {
		t.Fatalf("follow-up lineage = global=%v continueOf=%q workRoot=%q, want root %s", fu.Global, fu.ContinueOf, fu.WorkRoot, root.ID)
	}
	if fu.RunnerID != root.RunnerID || fu.RepoID != root.RepoID || fu.Base != root.Base ||
		fu.Agent != root.Agent || fu.Model != root.Model || fu.BranchSlug != root.BranchSlug ||
		fu.NoPR != root.NoPR || fu.Origin != runnerwire.OriginPerson {
		t.Fatalf("follow-up fields = %+v, root = %+v", fu, root)
	}
	if fu.Title != "Make the button bigger" {
		t.Fatalf("title = %q, want Make the button bigger", fu.Title)
	}
	if fu.Status != runnerwire.RunOffered {
		t.Fatalf("status = %s, want offered", fu.Status)
	}
	job := f.conn.last(t, fu.ID)
	if job.ContinueOf != root.ID || job.WorkRoot != root.ID || job.Slug != "login-btn" || job.NoPR {
		t.Fatalf("offered job = %+v", job)
	}
	// Seq 0 user event was recorded with the message.
	events, _, err := f.s.Events(ctx, fu.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Seq != 0 || events[0].Type != runnerwire.EventUser ||
		events[0].Text != "Make the button bigger\n\nUse 16px padding." {
		t.Fatalf("events = %+v, %v", events, err)
	}
	// Root now reports ContinuedBy = fu.ID.
	reloadedRoot, err := f.s.Get(ctx, root.ID)
	if err != nil || reloadedRoot.ContinuedBy != fu.ID {
		t.Fatalf("root.ContinuedBy = %q, want %q", reloadedRoot.ContinuedBy, fu.ID)
	}
}

func TestFollowUpChainsRoot(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	t1 := startAndFinishGlobal(t, f, "Turn 1", nil)

	t2, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: t2.ID, Base: "main", Branch: t1.Branch}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, t2.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	t3, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t2.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 3"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if t3.ContinueOf != t2.ID || t3.WorkRoot != t1.ID {
		t.Fatalf("t3 continueOf=%q workRoot=%q, want continueOf=%q workRoot=%q", t3.ContinueOf, t3.WorkRoot, t2.ID, t1.ID)
	}
	thread, err := f.s.Thread(ctx, t2)
	if err != nil || len(thread) != 3 || thread[0].ID != t1.ID || thread[1].ID != t2.ID || thread[2].ID != t3.ID {
		t.Fatalf("thread = %+v, %v", ids(thread), err)
	}
}

func TestFollowUpQueuedWhilePriorRunsInBackground(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	prior, err := f.d.StartGlobal(ctx, f.globalRequest("Background task"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: prior.ID, Base: "main", Branch: "agent/bg-" + prior.ID[:8]}); err != nil {
		t.Fatal(err)
	}

	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: prior.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Also update the tests", Mode: runnerwire.ModeInteractive},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp while background running: %v", err)
	}
	// Queued reply is held in queued status and forced to background mode.
	if fu.Status != runnerwire.RunQueued || fu.Mode != runnerwire.ModeHeadless {
		t.Fatalf("queued reply status=%q mode=%q, want queued headless", fu.Status, fu.Mode)
	}
	if f.conn.count(fu.ID) != 0 {
		t.Fatal("queued reply was offered before prior finished")
	}
	// Once prior finishes, Pump offers the queued reply.
	if err := f.d.Finish(ctx, f.runner.ID, prior.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.Get(ctx, fu.ID)
	if err != nil || after.Status != runnerwire.RunOffered {
		t.Fatalf("after prior finish: status=%q err=%v, want offered", after.Status, err)
	}
}

func TestFollowUpRefusesAnotherPerson(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Ann's work", nil)

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Bob hijacks"},
		ActorID: "u-bob", ActorName: "bob",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartNotFound {
		t.Fatalf("err = %#v, want StartNotFound", err)
	}
}

func TestFollowUpRefusesAgent(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Ann's work", nil)

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Agent loop"},
		Actor:   identity.Actor{ID: "u-ann", Name: "ann", Agent: identity.AgentWorker},
		IsAgent: true,
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != "PERMISSION_DENIED" {
		t.Fatalf("err = %#v, want PERMISSION_DENIED", err)
	}
}

func TestFollowUpRefusesLiveInteractive(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	req := f.globalRequest("Interactive session")
	req.Body.Mode = runnerwire.ModeInteractive
	prior, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: prior.ID, Base: "main", Branch: "agent/int-" + prior.ID[:8]}); err != nil {
		t.Fatal(err)
	}

	_, err = f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: prior.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Should type in screen"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeRunLive {
		t.Fatalf("err = %#v, want reason %s", err, CodeRunLive)
	}
}

func TestFollowUpRefusesNotLatest(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	t1 := startAndFinishGlobal(t, f, "Turn 1", nil)
	t2, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Stale turn 1 follow-up"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeContinued || refused.LatestID() != t2.ID {
		t.Fatalf("err = %#v (latest=%q), want %s with latestId %s", err, refused.LatestID(), CodeContinued, t2.ID)
	}
}

func TestFollowUpRefusesDiscarded(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Work to discard", nil)
	if _, err := f.s.db.ExecContext(ctx, `UPDATE runs SET workspace = ? WHERE id = ?`, runnerwire.RunWorkspaceDiscarded, root.ID); err != nil {
		t.Fatal(err)
	}

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Continue discarded"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeWorkGone {
		t.Fatalf("err = %#v, want reason %s", err, CodeWorkGone)
	}
}

func TestFollowUpRefusesMergedLocal(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Work to merge locally", nil)
	if _, err := f.s.db.ExecContext(ctx, `UPDATE runs SET workspace = ? WHERE id = ?`, runnerwire.RunWorkspaceMergedLocal, root.ID); err != nil {
		t.Fatal(err)
	}

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Continue merged local"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeWorkGone {
		t.Fatalf("err = %#v, want reason %s", err, CodeWorkGone)
	}
}

func TestFollowUpRefusesPRMerged(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "PR work", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, root.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/10", Number: 10, State: runnerwire.PRMerged,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "After PR merged"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodePREnded {
		t.Fatalf("err = %#v, want reason %s", err, CodePREnded)
	}
}

func TestFollowUpRefusesPRClosed(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "PR work", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, root.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/11", Number: 11, State: runnerwire.PRClosed,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "After PR closed"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodePREnded {
		t.Fatalf("err = %#v, want reason %s", err, CodePREnded)
	}
}

func TestFollowUpRefusesMergePending(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "PR work", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, root.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/12", Number: 12, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: testHead,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Merge(ctx, root.ID, Approver{ID: "u-ann", Name: "ann"}, testHead); err != nil {
		t.Fatal(err)
	}

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "While merge pending"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeMergePending {
		t.Fatalf("err = %#v, want reason %s", err, CodeMergePending)
	}
}

func TestFollowUpRefusesOverBudget(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 1}); err != nil {
		t.Fatal(err)
	}
	root := startAndFinishGlobal(t, f, "Only run allowed today", nil)

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Second run over budget"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartBudget {
		t.Fatalf("err = %#v, want StartBudget", err)
	}
}

func TestFollowUpRefusesOutdatedRunner(t *testing.T) {
	// Runner has CapGlobalRuns and CapGlobalRunsOn, but NOT CapContinue.
	f, _ := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Initial run", nil)

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Follow up on old runner"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartOutdated {
		t.Fatalf("err = %#v, want StartOutdated", err)
	}
}

func TestFollowUpRefusesTooLong(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Initial run", nil)

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: strings.Repeat("x", runnerwire.MaxFollowUpBytes+1)},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "message" {
		t.Fatalf("err = %#v, want StartInvalid field message", err)
	}
}

func TestFollowUpRefusesNodeRun(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	nodeRun := f.submit(t, "")

	_, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: nodeRun.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Try to follow up a node run"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %#v, want *StartError", err)
	}
}

func TestFollowUpAllowedOnOpenPR(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Open PR work", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, root.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/15", Number: 15, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: testHead,
	}); err != nil {
		t.Fatal(err)
	}

	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Update the open PR"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp on open PR refused: %v", err)
	}
	if fu.ContinueOf != root.ID || fu.WorkRoot != root.ID || fu.NoPR {
		t.Fatalf("follow-up on open PR = %+v", fu)
	}
}

func TestFollowUpQueuedBehindSkipsOnlyRunLive(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	req := f.globalRequest("Live interactive work")
	req.Body.Mode = runnerwire.ModeInteractive
	prior, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: prior.ID, Base: "main", Branch: "agent/int-" + prior.ID[:8]}); err != nil {
		t.Fatal(err)
	}

	// QueuedBehind skips RUN_LIVE and queues the follow-up behind the interactive prior.
	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID:      prior.ID,
		Body:         runnerwire.FollowUpRequest{Message: "Queued behind interactive"},
		ActorID:      "u-ann",
		ActorName:    "ann",
		QueuedBehind: true,
	})
	if err != nil {
		t.Fatalf("QueuedBehind follow-up failed: %v", err)
	}
	if fu.Status != runnerwire.RunQueued || fu.ContinueOf != prior.ID {
		t.Fatalf("QueuedBehind follow-up = %+v", fu)
	}

	// QueuedBehind still enforces other checks (e.g. NotLatest on prior now that fu exists).
	_, err = f.d.FollowUp(ctx, FollowUpRequest{
		PriorID:      prior.ID,
		Body:         runnerwire.FollowUpRequest{Message: "Second queued behind"},
		ActorID:      "u-ann",
		ActorName:    "ann",
		QueuedBehind: true,
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeContinued {
		t.Fatalf("second QueuedBehind err = %#v, want CONTINUED", err)
	}
}

func TestDeclinedFollowUpFreesTheSlotInDispatcher(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	prior := startAndFinishGlobal(t, f, "Turn 1", nil)

	// Start a follow-up that the runner declines with branch_moved before it ever starts.
	declined, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: prior.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2 declined"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Decline(ctx, f.runner.ID, declined.ID, "branch moved", runnerwire.DeclineBranchMoved); err != nil {
		t.Fatal(err)
	}

	// Reload prior: ContinuedBy is empty and WorkspaceGate does not say continued.
	reloadedPrior, err := f.s.Get(ctx, prior.ID)
	if err != nil || reloadedPrior.ContinuedBy != "" {
		t.Fatalf("prior after declined follow-up: ContinuedBy=%q err=%v", reloadedPrior.ContinuedBy, err)
	}
	gates := WorkspaceGate(reloadedPrior, f.d.WorkspaceRunnerOf(ctx, reloadedPrior))
	if gates.MergeBlocked == runnerwire.WorkspaceGateContinued || gates.DiscardBlocked == runnerwire.WorkspaceGateContinued {
		t.Fatalf("WorkspaceGate after declined follow-up = %+v", gates)
	}
	if err := CheckWorkspaceAction(reloadedPrior, f.d.WorkspaceRunnerOf(ctx, reloadedPrior), runnerwire.WorkspaceMergeLocal, reloadedPrior.Commit); err != nil {
		t.Fatalf("merge check on prior after declined follow-up: %v", err)
	}
	if err := CheckWorkspaceAction(reloadedPrior, f.d.WorkspaceRunnerOf(ctx, reloadedPrior), runnerwire.WorkspaceDiscard, ""); err != nil {
		t.Fatalf("discard check on prior after declined follow-up: %v", err)
	}

	// Naming the declined run itself as prior gets 409 CONTINUED with latestId = prior.ID.
	_, err = f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: declined.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Follow up naming declined run"},
		ActorID: "u-ann", ActorName: "ann",
	})
	var refused *StartError
	if !errors.As(err, &refused) || refused.Reason != CodeContinued || refused.LatestID() != prior.ID {
		t.Fatalf("follow-up on declined run = %#v (latest=%q), want CONTINUED %s", err, refused.LatestID(), prior.ID)
	}

	// A new follow-up of prior is accepted.
	retry, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: prior.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2 retry"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("new follow-up of prior after decline: %v", err)
	}
	if retry.ContinueOf != prior.ID {
		t.Fatalf("retry.ContinueOf = %q, want %q", retry.ContinueOf, prior.ID)
	}
}

func TestFollowUpAfterUsageLimitWaitsForReset(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	prior, err := f.d.StartGlobal(ctx, f.globalRequest("Hit usage limit"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: prior.ID, Base: "main", Branch: "agent/limit-" + prior.ID[:8]}); err != nil {
		t.Fatal(err)
	}
	resetAt := f.clock.Add(45 * time.Minute).UTC().Format(time.RFC3339)
	if err := f.d.Finish(ctx, f.runner.ID, prior.ID, runnerwire.RunResult{
		Status:      runnerwire.RunFailed,
		FailureKind: runnerwire.FailureQuota,
		Error:       "quota reached",
		ResetAt:     resetAt,
	}); err != nil {
		t.Fatal(err)
	}

	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: prior.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Continue after quota resets"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp after usage limit: %v", err)
	}
	if fu.RetryAt != resetAt || fu.Status != runnerwire.RunQueued {
		t.Fatalf("follow-up retryAt=%q status=%q, want retryAt=%q status=queued", fu.RetryAt, fu.Status, resetAt)
	}
	if f.conn.count(fu.ID) != 0 {
		t.Fatal("follow-up waiting for reset was offered early")
	}
}

func TestFollowUpNeverAuditsText(t *testing.T) {
	f, log := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "First task", nil)
	log.audited = nil

	secretMsg := "Change the API token to super-secret-phrase-999"
	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID,
		Body:    runnerwire.FollowUpRequest{Message: secretMsg},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(log.audited) != 1 {
		t.Fatalf("audited = %+v, want 1 entry", log.audited)
	}
	entry := log.audited[0]
	raw := fmt.Sprint(entry)
	if entry.Action != "run.global.followup" || entry.Target != fu.ID ||
		entry.Detail["prior"] != root.ID || entry.Detail["root"] != root.ID ||
		entry.Detail["messageBytes"] != len(secretMsg) ||
		strings.Contains(raw, "super-secret-phrase-999") || strings.Contains(raw, "Change the API") {
		t.Fatalf("audit entry leaked text or wrong shape: %+v", entry)
	}
}

func TestWorkspaceGateContinued(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	t1 := startAndFinishGlobal(t, f, "Turn 1", nil)
	t2, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: t2.ID, Base: "main", Branch: t1.Branch}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Changes(ctx, f.runner.ID, t2.ID, sampleChanges()); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, t2.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	reloaded1, err := f.s.Get(ctx, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	g1 := WorkspaceGate(reloaded1, fullRunner)
	if g1.MergeBlocked != runnerwire.WorkspaceGateContinued || g1.DiscardBlocked != runnerwire.WorkspaceGateContinued || g1.MergeMode != "" {
		t.Fatalf("older turn gate = %+v, want continued", g1)
	}
	if err := CheckWorkspaceAction(reloaded1, fullRunner, runnerwire.WorkspaceMergeLocal, reloaded1.Commit); BlockedCode(err) != runnerwire.WorkspaceGateContinued {
		t.Fatalf("merge on older turn = %v, want continued", err)
	}
	if err := CheckWorkspaceAction(reloaded1, fullRunner, runnerwire.WorkspaceDiscard, ""); BlockedCode(err) != runnerwire.WorkspaceGateContinued {
		t.Fatalf("discard on older turn = %v, want continued", err)
	}

	// Latest turn is mergeable and discardable.
	reloaded2, err := f.s.Get(ctx, t2.ID)
	if err != nil {
		t.Fatal(err)
	}
	g2 := WorkspaceGate(reloaded2, fullRunner)
	if g2.MergeMode != runnerwire.WorkspaceMergeModeLocal || g2.MergeBlocked != "" || g2.DiscardBlocked != "" {
		t.Fatalf("latest turn gate = %+v", g2)
	}
}

func TestWorkspaceInUseByWorkRoot(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	t1 := startAndFinishGlobal(t, f, "Turn 1", nil)

	if inUse, err := f.s.WorkspaceInUse(ctx, t1); err != nil || inUse {
		t.Fatalf("before follow-up: inUse=%v err=%v", inUse, err)
	}
	// Create a queued follow-up (not yet accepted, so branch is still empty, only work_root links them).
	t2, err := f.s.Create(ctx, New{
		Global: true, BranchSlug: t1.BranchSlug, NodeTitle: "Turn 2",
		RunnerID: f.runner.ID, OwnerID: "u-ann", Agent: "claude", Model: "default",
		RepoID: "repo", JobPrompt: "# Turn 2", CreatedBy: "u-ann",
		ContinueOf: t1.ID, WorkRoot: t1.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inUse, err := f.s.WorkspaceInUse(ctx, t1); err != nil || !inUse {
		t.Fatalf("with queued follow-up in work_root: inUse=%v err=%v, want true", inUse, err)
	}
	// Once t2 finishes or is cancelled, WorkspaceInUse is false again.
	if _, err := f.s.Transition(ctx, t2.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	if inUse, err := f.s.WorkspaceInUse(ctx, t1); err != nil || inUse {
		t.Fatalf("after follow-up cancelled: inUse=%v err=%v, want false", inUse, err)
	}
}

func TestMergeRefusedWhileTurnRunning(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	t1 := startAndFinishGlobal(t, f, "Turn 1", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, t1.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/21", Number: 21, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: testHead,
	}); err != nil {
		t.Fatal(err)
	}

	// Start turn 2 (offered / running).
	t2, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2 in progress"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: t2.ID, Base: "main", Branch: t1.Branch}); err != nil {
		t.Fatal(err)
	}

	// Merge approval while turn 2 is running is refused with TURN_RUNNING.
	_, err = f.d.Merge(ctx, t1.ID, Approver{ID: "u-ann", Name: "ann"}, testHead)
	if !errors.Is(err, ErrPrecondition) || BlockedCode(err) != CodeTurnRunning {
		t.Fatalf("Merge while turn running = %#v (code=%q), want TURN_RUNNING", err, BlockedCode(err))
	}
}

func TestMergeThroughOlderTurnFailsHeadCheck(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	head1 := "1111111111111111111111111111111111111111"
	head2 := "2222222222222222222222222222222222222222"

	t1 := startAndFinishGlobal(t, f, "Turn 1", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
	if err := f.d.PR(ctx, f.runner.ID, t1.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/22", Number: 22, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: head1,
	}); err != nil {
		t.Fatal(err)
	}

	t2, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: t1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Turn 2"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: t2.ID, Base: "main", Branch: t1.Branch}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.PR(ctx, f.runner.ID, t2.ID, runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/22", Number: 22, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: head2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, t2.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	// Approving merge through the older turn t1 fails the head check, even with head1.
	_, err = f.d.Merge(ctx, t1.ID, Approver{ID: "u-ann", Name: "ann"}, head1)
	if !errors.Is(err, ErrPrecondition) || err.Error() != MsgHeadMoved {
		t.Fatalf("Merge through older turn = %v, want %q", err, MsgHeadMoved)
	}
	// Approving merge through the latest turn t2 with head2 succeeds.
	if _, err := f.d.Merge(ctx, t2.ID, Approver{ID: "u-ann", Name: "ann"}, head2); err != nil {
		t.Fatalf("Merge through latest turn: %v", err)
	}
}

func TestEndOnlyByTypists(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	// Node run dispatched by u-bea on u-ann's runner.
	r, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "Interactive",
		RunnerID: f.runner.ID, RunnerName: f.runner.Name, OwnerID: "u-ann",
		Agent: "claude", Model: "default", RepoID: "repo", JobPrompt: "# Interactive",
		CreatedBy: "u-bea", CreatedByName: "bea", Mode: runnerwire.ModeInteractive,
	})
	if err != nil {
		t.Fatal(err)
	}
	r = f.d.Submit(ctx, r)
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/n1"}); err != nil {
		t.Fatal(err)
	}

	// Admin who is neither dispatcher nor runner owner is refused.
	admin := identity.Actor{ID: "u-admin", Name: "admin", Role: identity.RoleAdmin}
	if _, err := f.d.End(ctx, r.ID, admin); !errors.Is(err, ErrEndForbidden) {
		t.Fatalf("admin outside mayType End = %v, want ErrEndForbidden", err)
	}
	// Agent is refused.
	agent := identity.Actor{ID: "u-bea", Name: "bea", Agent: identity.AgentWorker}
	if _, err := f.d.End(ctx, r.ID, agent); !errors.Is(err, ErrEndForbidden) {
		t.Fatalf("agent End = %v, want ErrEndForbidden", err)
	}
	// Dispatcher (u-bea) and runner owner (u-ann) are allowed.
	if _, err := f.d.End(ctx, r.ID, identity.Actor{ID: "u-bea", Name: "bea"}); err != nil {
		t.Fatalf("dispatcher End: %v", err)
	}
	if _, err := f.d.End(ctx, r.ID, identity.Actor{ID: "u-ann", Name: "ann"}); err != nil {
		t.Fatalf("owner End: %v", err)
	}
}

func TestEndOnViewOnlyRunOwnerOnly(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	// Dispatched by u-bea on u-ann's runner, accepted as viewOnly.
	r, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "ViewOnly Interactive",
		RunnerID: f.runner.ID, RunnerName: f.runner.Name, OwnerID: "u-ann",
		Agent: "claude", Model: "default", RepoID: "repo", JobPrompt: "# Interactive",
		CreatedBy: "u-bea", CreatedByName: "bea", Mode: runnerwire.ModeInteractive,
	})
	if err != nil {
		t.Fatal(err)
	}
	r = f.d.Submit(ctx, r)
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{
		RunID: r.ID, Base: "main", Branch: "agent/n1", ViewOnly: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Dispatcher (u-bea) cannot End a view-only run on u-ann's runner.
	if _, err := f.d.End(ctx, r.ID, identity.Actor{ID: "u-bea", Name: "bea"}); !errors.Is(err, ErrEndForbidden) {
		t.Fatalf("dispatcher End on view-only run = %v, want ErrEndForbidden", err)
	}
	// Runner owner (u-ann) can End it.
	if _, err := f.d.End(ctx, r.ID, identity.Actor{ID: "u-ann", Name: "ann"}); err != nil {
		t.Fatalf("owner End on view-only run: %v", err)
	}
}

func TestEndOldRunner412(t *testing.T) {
	// Runner lacks CapContinue.
	f, _ := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	req := f.globalRequest("Interactive on old runner")
	req.Body.Mode = runnerwire.ModeInteractive
	r, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/old"}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.d.End(ctx, r.ID, identity.Actor{ID: "u-ann", Name: "ann"}); !errors.Is(err, ErrEndOutdated) {
		t.Fatalf("End on old runner = %v, want ErrEndOutdated", err)
	}
}

func TestAcceptViewOnlyMakesViewersReadOnly(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	req := f.globalRequest("Interactive view-only")
	req.Body.Mode = runnerwire.ModeInteractive
	r, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	// Attach a typing viewer while the run is still offered.
	viewer, err := f.d.TermAttach(ctx, r, true)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Detach()
	if !viewer.CanType() {
		t.Fatal("viewer should initially be able to type")
	}
	// Drain initial status frame ("offered").
	if c := control(t, nextFrame(t, viewer)); c.Type != "status" || c.Status != runnerwire.RunOffered {
		t.Fatalf("initial frame = %+v", c)
	}

	// Runner accepts with ViewOnly: true.
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{
		RunID: r.ID, Base: "main", Branch: "agent/vo", ViewOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	if viewer.CanType() {
		t.Fatal("viewer.CanType() = true after ViewOnly accept, want false")
	}
	reloaded, err := f.s.Get(ctx, r.ID)
	if err != nil || !reloaded.ViewOnly {
		t.Fatalf("reloaded.ViewOnly = %v, err = %v", reloaded.ViewOnly, err)
	}

	// Viewer receives readonly frame (and running status frame).
	sawReadOnly := false
	for range 2 {
		c := control(t, nextFrame(t, viewer))
		if c.Type == "readonly" {
			sawReadOnly = true
		}
	}
	if !sawReadOnly {
		t.Fatal("viewer never received readonly control frame")
	}
	// Input and resize are now refused.
	if err := f.d.TermInput(viewer, []byte("ls\r")); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("TermInput after viewOnly = %v, want ErrPrecondition", err)
	}
	if err := f.d.TermResize(viewer, 80, 24); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("TermResize after viewOnly = %v, want ErrPrecondition", err)
	}
}
