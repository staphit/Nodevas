package runs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

func groupContinueHello(maxConcurrent int, repoNames ...string) runnerwire.Info {
	info := groupHello(maxConcurrent, repoNames...)
	info.Capabilities = append(info.Capabilities,
		runnerwire.CapContinue,
		runnerwire.CapRunWorkspace,
		runnerwire.CapWorkspaceActions,
	)
	return info
}

func acceptAndFinishGroupRun(t *testing.T, f *stepUpFixture, r Record, status string) Record {
	t.Helper()
	ctx := context.Background()
	branch := "agent/" + r.BranchSlug + "-" + r.ID[:8]
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: branch}); err != nil {
		t.Fatalf("Accept(%s): %v", r.ID, err)
	}
	if err := f.d.Changes(ctx, f.runner.ID, r.ID, sampleChanges()); err != nil {
		t.Fatalf("Changes(%s): %v", r.ID, err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, r.ID, runnerwire.RunResult{Status: status, Summary: status}); err != nil {
		t.Fatalf("Finish(%s): %v", r.ID, err)
	}
	got, err := f.s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", r.ID, err)
	}
	return got
}

func TestGroupFollowUpFailedOnly(t *testing.T) {
	f, log := newGlobalFixture(t, groupContinueHello(3, "api", "web", "worker"))
	ctx := context.Background()

	req := f.groupRequest("Upgrade Go", "repo-1", "repo-2", "repo-3")
	started, err := f.d.StartGlobalGroup(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	r1 := acceptAndFinishGroupRun(t, f, started.Runs[0], runnerwire.RunSucceeded)
	r2 := acceptAndFinishGroupRun(t, f, started.Runs[1], runnerwire.RunFailed)
	r3 := acceptAndFinishGroupRun(t, f, started.Runs[2], runnerwire.RunCancelled)
	_ = r1
	_ = r3

	res, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID: started.Group.ID,
		Body: runnerwire.GroupFollowUpRequest{
			Message: "Fix the failing build\n\nSecret detail: token-xyz",
			Only:    "failed",
		},
		Actor:     identity.Actor{ID: "u-ann", Name: "ann"},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUpGroup: %v", err)
	}
	if len(res.Started) != 1 {
		t.Fatalf("started = %d (%+v), want 1 (only failed run %s)", len(res.Started), res.Started, r2.ID)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want empty", res.Skipped)
	}
	fu := res.Started[0]
	if fu.ContinueOf != r2.ID || fu.WorkRoot != r2.ID || fu.GroupID != started.Group.ID ||
		fu.Mode != runnerwire.ModeHeadless || fu.RepoID != r2.RepoID {
		t.Fatalf("follow-up = %+v, want continuation of %s in group %s", fu, r2.ID, started.Group.ID)
	}
	// Seq-0 user event carries the message; audit entries never contain it.
	events, _, err := f.s.Events(ctx, fu.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Type != runnerwire.EventUser ||
		events[0].Text != "Fix the failing build\n\nSecret detail: token-xyz" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	for _, entry := range log.audited {
		if entry.Action == "run.global.followup" && entry.Target == fu.ID {
			if _, ok := entry.Detail["message"]; ok {
				t.Fatalf("audit unexpectedly recorded message: %+v", entry.Detail)
			}
		}
	}
}

func TestGroupFollowUpSkipsNotLatestAndGone(t *testing.T) {
	f, _ := newGlobalFixture(t, groupContinueHello(4, "api", "web", "worker", "billing"))
	ctx := context.Background()

	started, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Patch CVE", "repo-1", "repo-2", "repo-3", "repo-4"))
	if err != nil {
		t.Fatal(err)
	}
	// repo-1: failed, then already followed up by a succeeded turn (so r1 is NOT latest, r1Next is latest & succeeded).
	r1 := acceptAndFinishGroupRun(t, f, started.Runs[0], runnerwire.RunFailed)
	// repo-2: failed, then workspace discarded (WORK_GONE).
	r2 := acceptAndFinishGroupRun(t, f, started.Runs[1], runnerwire.RunFailed)
	// repo-3: failed, PR merged (PR_ENDED).
	r3 := acceptAndFinishGroupRun(t, f, started.Runs[2], runnerwire.RunFailed)
	// repo-4: failed, open, latest; had a withdrawn follow-up that should not block it.
	r4 := acceptAndFinishGroupRun(t, f, started.Runs[3], runnerwire.RunFailed)

	// Continue r1 -> r1Next (succeeded), so r1 is no longer the latest turn.
	r1Next, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: r1.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Retry repo-1"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp r1: %v", err)
	}
	r1Next = acceptAndFinishGroupRun(t, f, r1Next, runnerwire.RunSucceeded)

	// Mark r2 workspace discarded and r3 PR merged.
	if _, err := f.s.db.ExecContext(ctx, `UPDATE runs SET workspace = ? WHERE id = ?`,
		runnerwire.RunWorkspaceDiscarded, r2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ExecContext(ctx, `UPDATE runs SET pr_state = ? WHERE id = ?`,
		runnerwire.PRMerged, r3.ID); err != nil {
		t.Fatal(err)
	}

	// Create and immediately withdraw a follow-up on r4 (declined before starting).
	r4Withdrawn, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: r4.ID,
		Body:    runnerwire.FollowUpRequest{Message: "Withdrawn turn"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp r4 withdrawn: %v", err)
	}
	if err := f.d.Decline(ctx, f.runner.ID, r4Withdrawn.ID, "console no", runnerwire.DeclineOnRunner); err != nil {
		t.Fatalf("Decline r4Withdrawn: %v", err)
	}

	// 1. only="failed":
	//    - r1 is skipped because it is NOT latest (r1Next succeeded, so repo-1 is not failed);
	//    - r2 is skipped with WORK_GONE;
	//    - r3 is skipped with PR_ENDED;
	//    - r4 is started (withdrawn follow-up freed its slot).
	failedRes, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID:   started.Group.ID,
		Body:      runnerwire.GroupFollowUpRequest{Message: "Fix remaining failures", Only: "failed"},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUpGroup(failed): %v", err)
	}
	if len(failedRes.Started) != 1 || failedRes.Started[0].ContinueOf != r4.ID {
		t.Fatalf("failed started = %+v, want single follow-up of r4 (%s)", failedRes.Started, r4.ID)
	}
	wantSkipped := []runnerwire.GroupFollowUpSkipped{
		{RunID: r2.ID, Code: CodeWorkGone},
		{RunID: r3.ID, Code: CodePREnded},
	}
	if !slices.Equal(failedRes.Skipped, wantSkipped) {
		t.Fatalf("failed skipped = %+v, want %+v", failedRes.Skipped, wantSkipped)
	}

	// Finish the new r4 follow-up so we can test only="all" across the whole group.
	r4Turn2 := acceptAndFinishGroupRun(t, f, failedRes.Started[0], runnerwire.RunSucceeded)

	// 2. only="all":
	//    - r1 and r4 are NOT latest (must NOT be continued);
	//    - r1Next and r4Turn2 ARE latest and open -> started;
	//    - r2 (WORK_GONE) and r3 (PR_ENDED) -> skipped.
	allRes, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID:   started.Group.ID,
		Body:      runnerwire.GroupFollowUpRequest{Message: "Run final lint", Only: "all"},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUpGroup(all): %v", err)
	}
	gotPriors := make([]string, 0, len(allRes.Started))
	for _, r := range allRes.Started {
		gotPriors = append(gotPriors, r.ContinueOf)
	}
	slices.Sort(gotPriors)
	wantPriors := []string{r1Next.ID, r4Turn2.ID}
	slices.Sort(wantPriors)
	if !slices.Equal(gotPriors, wantPriors) {
		t.Fatalf("all started continueOf = %v, want %v (must continue latest turns only)", gotPriors, wantPriors)
	}
	if !slices.Equal(allRes.Skipped, wantSkipped) {
		t.Fatalf("all skipped = %+v, want %+v", allRes.Skipped, wantSkipped)
	}
}

func TestGroupFollowUpValidationAndOutdated(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web")) // lacks CapContinue
	ctx := context.Background()

	started, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Bump deps", "repo-1", "repo-2"))
	if err != nil {
		t.Fatal(err)
	}

	// Agent refused.
	if _, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID: started.Group.ID,
		Body:    runnerwire.GroupFollowUpRequest{Message: "hi", Only: "all"},
		Actor:   identity.Actor{ID: "u-ann", Agent: identity.AgentWorker},
	}); err == nil {
		t.Fatal("expected agent refusal")
	} else {
		var se *StartError
		if !errors.As(err, &se) || se.Reason != "PERMISSION_DENIED" {
			t.Fatalf("agent err = %#v, want PERMISSION_DENIED", err)
		}
	}

	// Invalid only field -> StartInvalid field "only".
	if _, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID: started.Group.ID,
		Body:    runnerwire.GroupFollowUpRequest{Message: "hi", Only: "succeeded"},
		ActorID: "u-ann", ActorName: "ann",
	}); err == nil {
		t.Fatal("expected invalid only refusal")
	} else {
		var se *StartError
		if !errors.As(err, &se) || se.Code != StartInvalid || se.Field != "only" {
			t.Fatalf("invalid only err = %#v, want StartInvalid field only", err)
		}
	}

	// Empty message -> StartInvalid field "message".
	if _, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID: started.Group.ID,
		Body:    runnerwire.GroupFollowUpRequest{Message: "   ", Only: "all"},
		ActorID: "u-ann", ActorName: "ann",
	}); err == nil {
		t.Fatal("expected empty message refusal")
	} else {
		var se *StartError
		if !errors.As(err, &se) || se.Code != StartInvalid || se.Field != "message" {
			t.Fatalf("empty message err = %#v, want StartInvalid field message", err)
		}
	}

	// Runner without CapContinue -> StartOutdated RUNNER_OUTDATED.
	if _, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID: started.Group.ID,
		Body:    runnerwire.GroupFollowUpRequest{Message: "Continue", Only: "all"},
		ActorID: "u-ann", ActorName: "ann",
	}); err == nil {
		t.Fatal("expected RUNNER_OUTDATED refusal")
	} else {
		var se *StartError
		if !errors.As(err, &se) || se.Code != StartOutdated || se.Reason != CodeRunnerOutdated {
			t.Fatalf("outdated err = %#v, want StartOutdated RUNNER_OUTDATED", err)
		}
	}
}

func TestGroupFollowUpMidGroupBudgetSkip(t *testing.T) {
	f, _ := newGlobalFixture(t, groupContinueHello(3, "api", "web", "worker"))
	ctx := context.Background()

	started, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Bump deps", "repo-1", "repo-2", "repo-3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range started.Runs {
		acceptAndFinishGroupRun(t, f, r, runnerwire.RunSucceeded)
	}
	// DailyRuns=4 with 3 already used leaves room for 1 follow-up; the other 2 skip with BUDGET_EXCEEDED.
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 4}); err != nil {
		t.Fatal(err)
	}
	res, err := f.d.FollowUpGroup(ctx, GroupFollowUpRequest{
		GroupID:   started.Group.ID,
		Body:      runnerwire.GroupFollowUpRequest{Message: "Polish", Only: "all"},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUpGroup: %v", err)
	}
	if len(res.Started) != 1 || res.Started[0].ContinueOf != started.Runs[0].ID {
		t.Fatalf("started = %+v, want 1 follow-up of %s", res.Started, started.Runs[0].ID)
	}
	wantSkipped := []runnerwire.GroupFollowUpSkipped{
		{RunID: started.Runs[1].ID, Code: "BUDGET_EXCEEDED"},
		{RunID: started.Runs[2].ID, Code: "BUDGET_EXCEEDED"},
	}
	if !slices.Equal(res.Skipped, wantSkipped) {
		t.Fatalf("skipped = %+v, want %+v", res.Skipped, wantSkipped)
	}
}
