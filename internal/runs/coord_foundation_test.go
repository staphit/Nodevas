package runs

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

func newGlobalRun(t *testing.T, s *Store, edit func(*New)) Record {
	t.Helper()
	in := New{
		Global: true, BranchSlug: "fix-it", NodeTitle: "Fix it",
		RunnerID: "r1", RunnerName: "desk", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", RepoName: "repo",
		JobPrompt: "Fix it", CreatedBy: "u-ann", CreatedByName: "ann",
		Origin: runnerwire.OriginPerson,
	}
	if edit != nil {
		edit(&in)
	}
	r, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStoreContinueColumnsGlobalOnly(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	// Non-global run with ContinueOf, WorkRoot or GroupID is refused.
	for name, edit := range map[string]func(*New){
		"continueOf": func(in *New) { in.ContinueOf = "prior1" },
		"workRoot":   func(in *New) { in.WorkRoot = "root1" },
		"groupId":    func(in *New) { in.GroupID = "g1" },
	} {
		in := New{
			Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
			Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		}
		edit(&in)
		if _, err := s.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("node run with %s: err = %v, want ErrInvalid", name, err)
		}
	}
	// Global run stores ContinueOf, WorkRoot and GroupID and exposes them in Job().
	root := newGlobalRun(t, s, nil)
	cont := newGlobalRun(t, s, func(in *New) {
		in.ContinueOf = root.ID
		in.WorkRoot = root.ID
		in.GroupID = "g1"
	})
	if cont.ContinueOf != root.ID || cont.WorkRoot != root.ID || cont.GroupID != "g1" {
		t.Fatalf("cont = %+v", cont.Run)
	}
	job := cont.Job()
	if job.ContinueOf != root.ID || job.WorkRoot != root.ID {
		t.Fatalf("job = %+v", job)
	}
	// Root now reports ContinuedBy = cont.ID.
	reloaded, err := s.Get(ctx, root.ID)
	if err != nil || reloaded.ContinuedBy != cont.ID {
		t.Fatalf("root.ContinuedBy = %q, want %q (err=%v)", reloaded.ContinuedBy, cont.ID, err)
	}
}

func TestOneLiveContinuationPerRun(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	root := newGlobalRun(t, s, nil)

	first := newGlobalRun(t, s, func(in *New) {
		in.ContinueOf = root.ID
		in.WorkRoot = root.ID
	})
	// Second live continuation fails the unique index.
	if _, err := s.Create(ctx, New{
		Global: true, BranchSlug: "fix-it", NodeTitle: "Second",
		RunnerID: "r1", OwnerID: "u-ann", Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		ContinueOf: root.ID, WorkRoot: root.ID,
	}); !db.IsUniqueViolation(err, "runs_one_continuation") {
		t.Fatalf("second continuation err = %v, want runs_one_continuation", err)
	}
	// Withdrawing (cancelling before starting) frees the slot.
	if _, err := s.Transition(ctx, first.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Successor(ctx, root.ID); err != nil || ok {
		t.Fatalf("Successor after withdraw = %v, %v; want false", ok, err)
	}
	second := newGlobalRun(t, s, func(in *New) {
		in.ContinueOf = root.ID
		in.WorkRoot = root.ID
	})
	succ, ok, err := s.Successor(ctx, root.ID)
	if err != nil || !ok || succ.ID != second.ID {
		t.Fatalf("Successor = %+v %v %v, want %s", succ.ID, ok, err, second.ID)
	}
}

func TestDeclinedFollowUpFreesTheSlot(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	root := newGlobalRun(t, s, nil)

	first := newGlobalRun(t, s, func(in *New) {
		in.ContinueOf = root.ID
		in.WorkRoot = root.ID
	})
	if _, err := s.Transition(ctx, first.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, first.ID, runnerwire.RunDeclined, Change{
		DeclineCode: runnerwire.DeclineBranchMoved, Error: "branch moved",
	}); err != nil {
		t.Fatal(err)
	}
	// Withdrawn because started_at == '': Successor ignores it, ContinuedBy is empty, and a new follow-up succeeds.
	if _, ok, err := s.Successor(ctx, root.ID); err != nil || ok {
		t.Fatalf("Successor after decline = %v, %v; want false", ok, err)
	}
	reloaded, err := s.Get(ctx, root.ID)
	if err != nil || reloaded.ContinuedBy != "" {
		t.Fatalf("ContinuedBy after decline = %q, %v", reloaded.ContinuedBy, err)
	}
	second := newGlobalRun(t, s, func(in *New) {
		in.ContinueOf = root.ID
		in.WorkRoot = root.ID
	})
	if succ, ok, err := s.Successor(ctx, root.ID); err != nil || !ok || succ.ID != second.ID {
		t.Fatalf("Successor after retry = %s %v %v", succ.ID, ok, err)
	}
}

func TestUserFirstSeqZero(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	nodeRun := createRun(t, s, "n1")
	if err := s.UserFirst(ctx, nodeRun.ID, "hello"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UserFirst on node run = %v, want ErrInvalid", err)
	}
	g := newGlobalRun(t, s, nil)
	if err := s.UserFirst(ctx, g.ID, "make the button bigger"); err != nil {
		t.Fatal(err)
	}
	// Second call is ignored (ON CONFLICT DO NOTHING).
	if err := s.UserFirst(ctx, g.ID, "ignored"); err != nil {
		t.Fatal(err)
	}
	events, more, err := s.Events(ctx, g.ID, 0, 10)
	if err != nil || more || len(events) != 1 {
		t.Fatalf("events = %+v more=%v err=%v", events, more, err)
	}
	if events[0].Seq != 0 || events[0].Type != runnerwire.EventUser || events[0].Text != "make the button bigger" {
		t.Fatalf("event[0] = %+v", events[0])
	}
}

func TestFinishStoresResetAtEndReasonAndPushRefused(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// Failed run stores reset_at.
	r1 := createRun(t, s, "n1")
	_, _ = s.Transition(ctx, r1.ID, runnerwire.RunOffered, Change{})
	_, _ = s.Transition(ctx, r1.ID, runnerwire.RunRunning, Change{Runner: "r1"})
	failed, err := s.Finish(ctx, r1.ID, "r1", runnerwire.RunResult{
		Status:      runnerwire.RunFailed,
		FailureKind: runnerwire.FailureQuota,
		ResetAt:     "2026-11-18T14:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded1, err := s.Get(ctx, failed.ID)
	if err != nil || reloaded1.ResetAt != "2026-11-18T14:00:00Z" || reloaded1.PushRefused {
		t.Fatalf("failed reloaded = %+v, %v", reloaded1.Run, err)
	}

	// Succeeded run stores end_reason and push_refused.
	r2 := createRun(t, s, "n2")
	_, _ = s.Transition(ctx, r2.ID, runnerwire.RunOffered, Change{})
	_, _ = s.Transition(ctx, r2.ID, runnerwire.RunRunning, Change{Runner: "r1"})
	done, err := s.Finish(ctx, r2.ID, "r1", runnerwire.RunResult{
		Status:      runnerwire.RunSucceeded,
		EndReason:   runnerwire.EndReasonQuietWaiting,
		PushRefused: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded2, err := s.Get(ctx, done.ID)
	if err != nil || reloaded2.EndReason != runnerwire.EndReasonQuietWaiting || !reloaded2.PushRefused {
		t.Fatalf("done reloaded = %+v, %v", reloaded2.Run, err)
	}
}

func TestOpenPRWorkspaceActionOnlyForContestWinner(t *testing.T) {
	coordRunner := WorkspaceRunner{
		Online: true,
		Caps:   []string{runnerwire.CapRunWorkspace, runnerwire.CapWorkspaceActions, runnerwire.CapCoord},
	}
	winner := Record{Run: runnerwire.Run{
		ID: "r1", Status: runnerwire.RunSucceeded, Branch: "agent/n1-r1", Commit: wsCommit,
		ContestID: "c1", ContestLabel: "A",
	}}
	if err := CheckWorkspaceAction(winner, coordRunner, runnerwire.WorkspaceOpenPR, ""); err != nil {
		t.Fatalf("valid contest entry refused: %v", err)
	}
	// Plain non-contest run refused.
	plain := winner
	plain.ContestID, plain.ContestLabel = "", ""
	if err := CheckWorkspaceAction(plain, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateKind {
		t.Fatalf("plain run = %v, want kind gate", err)
	}
	// Reviewer label J refused.
	reviewer := winner
	reviewer.ContestLabel = "J"
	if err := CheckWorkspaceAction(reviewer, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateKind {
		t.Fatalf("reviewer = %v, want kind gate", err)
	}
	// Failed contest entry refused.
	failed := winner
	failed.Status = runnerwire.RunFailed
	if err := CheckWorkspaceAction(failed, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateKind {
		t.Fatalf("failed entry = %v, want kind gate", err)
	}
	// No commit refused.
	noCommit := winner
	noCommit.Commit = ""
	if err := CheckWorkspaceAction(noCommit, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateNoCommit {
		t.Fatalf("no commit = %v, want no_commit", err)
	}
	// Existing PR refused.
	hasPR := winner
	hasPR.PRURL = "https://github.com/org/repo/pull/1"
	if err := CheckWorkspaceAction(hasPR, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceErrHasPR {
		t.Fatalf("existing PR = %v, want has_pr", err)
	}
	// Non-live workspace refused.
	merged := winner
	merged.Workspace = runnerwire.RunWorkspaceMergedLocal
	if err := CheckWorkspaceAction(merged, coordRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateMergedLocal {
		t.Fatalf("merged workspace = %v, want merged_local", err)
	}
	// Runner without CapCoord refused.
	if err := CheckWorkspaceAction(winner, fullRunner, runnerwire.WorkspaceOpenPR, ""); BlockedCode(err) != runnerwire.WorkspaceGateOutdated {
		t.Fatalf("no CapCoord = %v, want outdated", err)
	}
	if workspaceVerb(runnerwire.WorkspaceOpenPR) != "open_pr" {
		t.Fatalf("workspaceVerb(open_pr) = %q", workspaceVerb(runnerwire.WorkspaceOpenPR))
	}
}

func TestOpenPROutcomeStoresPRURL(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		NoPR: true, ContestID: "c1", ContestLabel: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{})
	_, _ = s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1", Base: "main", Branch: "agent/n1-r1"})
	_, _, _ = s.SetChanges(ctx, r.ID, "r1", sampleChanges())
	r, err = s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceOpenPR, r.Commit, "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/org/repo/pull/99"
	updated, doneAction, err := s.FinishWorkspaceAction(ctx, "r1", runnerwire.WorkspaceOutcome{
		ID: a.ID, RunID: r.ID, Action: runnerwire.WorkspaceOpenPR, PRURL: prURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doneAction.Status != runnerwire.WorkspaceDone {
		t.Fatalf("action status = %q", doneAction.Status)
	}
	if updated.PRURL != prURL || updated.PRState != runnerwire.PROpen || updated.Workspace != runnerwire.RunWorkspaceLive {
		t.Fatalf("updated run = prURL=%q prState=%q workspace=%q", updated.PRURL, updated.PRState, updated.Workspace)
	}
}

func TestPumpHoldsAFollowUpUntilPriorFinal(t *testing.T) {
	ctx := context.Background()
	f := newStepUpFixture(t)
	if err := f.d.reg.SetHello(ctx, f.runner.ID, runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn}, MaxConcurrent: 4,
	}); err != nil {
		t.Fatal(err)
	}
	prior := newGlobalRun(t, f.s, func(in *New) { in.RunnerID = f.runner.ID })
	prior = f.d.Submit(ctx, prior)
	if prior.Status != runnerwire.RunOffered {
		t.Fatalf("prior status = %q", prior.Status)
	}
	// Follow-up created while prior is not final stays queued.
	fu := newGlobalRun(t, f.s, func(in *New) {
		in.RunnerID = f.runner.ID
		in.ContinueOf = prior.ID
		in.WorkRoot = prior.ID
	})
	fu = f.d.Submit(ctx, fu)
	if fu.Status != runnerwire.RunQueued {
		t.Fatalf("follow-up offered while prior %s: got %s", prior.Status, fu.Status)
	}
	// Once prior finishes, Pump offers the follow-up.
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: prior.ID, Base: "main", Branch: "agent/fix-it"}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, prior.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.Get(ctx, fu.ID)
	if err != nil || after.Status != runnerwire.RunOffered {
		t.Fatalf("follow-up after prior finished = %s, %v", after.Status, err)
	}
}

func TestPumpSkipsAHeldGroup(t *testing.T) {
	ctx := context.Background()
	f := newStepUpFixture(t)
	if err := f.d.reg.SetHello(ctx, f.runner.ID, runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn}, MaxConcurrent: 4,
	}); err != nil {
		t.Fatal(err)
	}
	future := formatStamp(f.clock.Add(30 * time.Minute))
	if _, err := f.s.db.ExecContext(ctx, `INSERT INTO run_groups
		(id, owner_id, runner_id, created_by, title, agent, model, no_pr, repo_count, held_until, created_at)
		VALUES ('g1', 'u-ann', ?, 'u-ann', 'Upgrade', 'claude', 'opus', FALSE, 2, ?, ?)`,
		f.runner.ID, future, formatStamp(f.clock)); err != nil {
		t.Fatal(err)
	}
	member := newGlobalRun(t, f.s, func(in *New) {
		in.RunnerID = f.runner.ID
		in.GroupID = "g1"
	})
	member = f.d.Submit(ctx, member)
	if member.Status != runnerwire.RunQueued {
		t.Fatalf("held group member was offered: %s", member.Status)
	}
	// Clearing the hold lets Pump offer it.
	if err := f.s.SetGroupHold(ctx, "g1", ""); err != nil {
		t.Fatal(err)
	}
	f.d.Pump(ctx, f.runner.ID)
	after, err := f.s.Get(ctx, member.ID)
	if err != nil || after.Status != runnerwire.RunOffered {
		t.Fatalf("unheld group member = %s, %v", after.Status, err)
	}
}

func TestAcceptCallsViewOnlyHook(t *testing.T) {
	ctx := context.Background()
	f := newStepUpFixture(t)
	var gotRun string
	var gotViewOnly bool
	f.d.coord.onAcceptViewOnly = func(_ context.Context, r Record, accept runnerwire.Accept) {
		gotRun = r.ID
		gotViewOnly = accept.ViewOnly
	}
	r := f.submit(t, "")
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{
		RunID: r.ID, Base: "main", Branch: "agent/n1", ViewOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	if gotRun != r.ID || !gotViewOnly {
		t.Fatalf("acceptViewOnly called with (%q, %v), want (%q, true)", gotRun, gotViewOnly, r.ID)
	}
}

func TestStoreContestNoPROnlyInContest(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	// Node run with NoPR and no contest is refused.
	if _, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann", NoPR: true,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("node NoPR without contest = %v, want ErrInvalid", err)
	}
	// Node contest entry with NoPR succeeds and Job() carries NoPR + Contest.
	r, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		NoPR: true, ContestID: "c1", ContestLabel: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := r.Job()
	if !job.NoPR || job.Contest == nil || job.Contest.ID != "c1" || job.Contest.Label != "A" {
		t.Fatalf("job = %+v", job)
	}
}

func TestStoreCompareOnlyAsReviewer(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	// Compare without contest refused.
	if _, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		Kind: runnerwire.JobCompare,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("compare without contest = %v", err)
	}
	// Compare with label A refused.
	if _, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		Kind: runnerwire.JobCompare, ContestID: "c1", ContestLabel: "A",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("compare with label A = %v", err)
	}
	// Compare with label J succeeds (both node and global).
	if _, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		Kind: runnerwire.JobCompare, ContestID: "c1", ContestLabel: "J", NoPR: true,
	}); err != nil {
		t.Fatalf("node compare reviewer: %v", err)
	}
	if _, err := s.Create(ctx, New{
		Global: true, BranchSlug: "cmp", NodeTitle: "Compare", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		Kind: runnerwire.JobCompare, ContestID: "c2", ContestLabel: "J", NoPR: true,
	}); err != nil {
		t.Fatalf("global compare reviewer: %v", err)
	}
}

func TestStoreContestRefusesFallbacks(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
		NoPR: true, ContestID: "c1", ContestLabel: "A",
		Fallbacks: []runnerwire.AgentModel{{Agent: "codex", Model: "gpt-5"}},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("contest with fallbacks = %v, want ErrInvalid", err)
	}
}

func TestCreateWithReadsBackInsideTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := newStore(t)

	var createdRun Record
	var createdPipe PipelineRecord
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, "budget:u-ann", "repo:abc", "node:./n1", "repo:abc"); err != nil {
			return err
		}
		p, err := s.createPipelineWith(ctx, tx, NewPipeline{
			OwnerID: "u-ann", CreatedBy: "u-ann", Project: ".", NodeID: "n1",
			Pipeline: runnerwire.Pipeline{Implement: am("claude", "opus"), Review: am("codex", "gpt-5"), MaxRounds: 1},
		})
		if err != nil {
			return err
		}
		createdPipe = p
		r, err := s.createWith(ctx, tx, New{
			Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: "r1", OwnerID: "u-ann",
			Agent: "claude", Model: "opus", RepoID: "repo", CreatedBy: "u-ann",
			PipelineID: p.ID, AfterRun: "r0", BusyChoice: runnerwire.OnBusyQueue,
			RepoKey: "0123456789abcdef0123456789abcdef",
		})
		if err != nil {
			return err
		}
		createdRun = r
		// Verify reader pool cannot see uncommitted row yet while createWith already returned it.
		if _, err := s.Get(ctx, r.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("reader saw uncommitted row: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdPipe.ID == "" || createdRun.ID == "" || createdRun.PipelineID != createdPipe.ID ||
		createdRun.AfterRun != "r0" || createdRun.BusyChoice != runnerwire.OnBusyQueue ||
		createdRun.RepoKey != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("created = pipe %+v run %+v", createdPipe.ID, createdRun)
	}
}

func TestPumpAsksWaitsBehind(t *testing.T) {
	ctx := context.Background()
	f := newStepUpFixture(t)
	var held atomic.Bool
	held.Store(true)
	f.d.coord.onWaitsBehind = func(_ context.Context, r Record) bool {
		return held.Load()
	}
	r := f.submit(t, "")
	if r.Status != runnerwire.RunQueued {
		t.Fatalf("waitsBehind=true still offered run: %s", r.Status)
	}
	held.Store(false)
	f.d.Pump(ctx, f.runner.ID)
	after, err := f.s.Get(ctx, r.ID)
	if err != nil || after.Status != runnerwire.RunOffered {
		t.Fatalf("after waitsBehind=false: %s, %v", after.Status, err)
	}
}

func TestEndedHookRunsAfterSuccessorsAndForGlobal(t *testing.T) {
	ctx := context.Background()
	f := newHandoffFixture(t)
	if err := f.d.reg.SetHello(ctx, f.runner.ID, runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn}, MaxConcurrent: 4,
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var sawSuccessorWhenEnded bool
	var endedIDs []string
	f.d.coord.onEnded = func(ctx context.Context, r Record) {
		endedIDs = append(endedIDs, r.ID)
		if !r.Global {
			_, ok := f.follower(t, r.ID)
			sawSuccessorWhenEnded = ok
		}
	}

	// Node run with fallback: successor must already exist when ended runs.
	first := f.start(t, func(in *New) {
		in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	})
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureModel})
	if !sawSuccessorWhenEnded {
		t.Fatal("ended ran before fallback successor was created")
	}

	// Global run also triggers ended despite afterEnd's global early return.
	g := newGlobalRun(t, f.s, func(in *New) { in.RunnerID = f.runner.ID })
	g = f.d.Submit(ctx, g)
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: g.ID, Base: "main", Branch: "agent/fix-it"}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, g.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	foundGlobal := false
	for _, id := range endedIDs {
		if id == g.ID {
			foundGlobal = true
		}
	}
	if !foundGlobal {
		t.Fatalf("ended did not run for global run %s; got %v", g.ID, endedIDs)
	}
}

func TestNoUsageLimitWaitOrFallbackForContestEntry(t *testing.T) {
	ctx := context.Background()
	f := newHandoffFixture(t)
	entry := f.start(t, func(in *New) {
		in.NoPR = true
		in.ContestID = "c1"
		in.ContestLabel = "A"
	})
	// Usage-limit failure on contest entry must not create a waitForReset successor.
	f.fail(t, entry.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureRateLimit})
	if _, ok := f.follower(t, entry.ID); ok {
		t.Fatal("contest entry created a usage-limit wait successor")
	}
	// Direct fallback guard returns false for a contest record even if Fallbacks is populated in memory.
	entry.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	if f.d.fallback(ctx, entry) {
		t.Fatal("fallback returned true for a contest entry")
	}
	if canWaitForReset(entry) {
		t.Fatal("canWaitForReset returned true for a contest entry")
	}
}

func TestRunRetriesCallsReleaseWaiting(t *testing.T) {
	f := newStepUpFixture(t)
	var waitingCalls, groupHoldCalls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	f.d.coord.onReleaseWaiting = func(_ context.Context) {
		if waitingCalls.Add(1) >= 1 && groupHoldCalls.Load() >= 1 {
			cancel()
		}
	}
	f.d.coord.onReleaseGroupHolds = func(_ context.Context) {
		if groupHoldCalls.Add(1) >= 1 && waitingCalls.Load() >= 1 {
			cancel()
		}
	}
	done := make(chan struct{})
	go func() {
		f.d.RunRetries(ctx, 5*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("RunRetries did not call releaseWaiting and releaseGroupHolds")
	}
	if waitingCalls.Load() < 1 || groupHoldCalls.Load() < 1 {
		t.Fatalf("calls: waiting=%d groupHolds=%d", waitingCalls.Load(), groupHoldCalls.Load())
	}
}
