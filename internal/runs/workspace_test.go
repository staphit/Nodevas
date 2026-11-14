package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

const wsCommit = "0123456789abcdef0123456789abcdef01234567"

// finishedRun is a succeeded run with a commit and no PR (autoPR off).
func finishedRun(t *testing.T, s *Store) Record {
	t.Helper()
	ctx := context.Background()
	r := startRun(t, s)
	if _, _, err := s.SetChanges(ctx, r.ID, "r1", sampleChanges()); err != nil {
		t.Fatal(err)
	}
	done, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	return done
}

var fullRunner = WorkspaceRunner{Online: true, Caps: []string{runnerwire.CapRunWorkspace, runnerwire.CapWorkspaceActions}}

// Every row of the gate table: the first that applies wins, for merge and
// discard alike.
func TestWorkspaceGateTable(t *testing.T) {
	base := Record{Run: runnerwire.Run{ID: "r", Status: runnerwire.RunSucceeded, Branch: "agent/n-1-r", Commit: wsCommit}}
	pendingMerge := &runnerwire.MergeRequest{Status: runnerwire.MergePending}
	pendingAction := &runnerwire.RunWorkspaceAction{Status: runnerwire.WorkspacePending}
	cases := []struct {
		name    string
		edit    func(*Record)
		custom  bool
		runner  WorkspaceRunner
		mode    string
		merge   string
		discard string
		done    bool
		hubOnly bool
	}{
		{name: "local merge", mode: runnerwire.WorkspaceMergeModeLocal},
		{name: "running", edit: func(r *Record) { r.Status = runnerwire.RunRunning },
			merge: runnerwire.WorkspaceGateRunning, discard: runnerwire.WorkspaceGateRunning},
		{name: "queued", edit: func(r *Record) { r.Status = runnerwire.RunQueued },
			merge: runnerwire.WorkspaceGateRunning, discard: runnerwire.WorkspaceGateRunning},
		{name: "discarded", edit: func(r *Record) { r.Workspace = runnerwire.RunWorkspaceDiscarded },
			merge: runnerwire.WorkspaceGateDiscarded, discard: runnerwire.WorkspaceGateDiscarded, done: true},
		{name: "merged locally", edit: func(r *Record) { r.Workspace = runnerwire.RunWorkspaceMergedLocal },
			merge: runnerwire.WorkspaceGateMergedLocal, discard: runnerwire.WorkspaceGateMergedLocal},
		{name: "PR merged", edit: func(r *Record) { r.PRURL, r.PRState = "https://x/pull/1", runnerwire.PRMerged },
			merge: runnerwire.WorkspaceGatePRMerged, discard: runnerwire.WorkspaceGatePRMerged},
		{name: "action pending", edit: func(r *Record) { r.WorkspaceAction = pendingAction },
			merge: runnerwire.WorkspaceGatePending, discard: runnerwire.WorkspaceGatePending},
		{name: "PR merge pending", edit: func(r *Record) { r.Merge = pendingMerge },
			merge: runnerwire.WorkspaceGatePending, discard: runnerwire.WorkspaceGatePending},
		{name: "open PR", edit: func(r *Record) { r.PRURL, r.PRState = "https://x/pull/1", runnerwire.PROpen },
			mode: runnerwire.WorkspaceMergeModePR},
		{name: "review run", edit: func(r *Record) { r.Kind = runnerwire.JobReview }, merge: runnerwire.WorkspaceGateKind},
		{name: "test run", edit: func(r *Record) { r.Kind = runnerwire.JobTest }, mode: runnerwire.WorkspaceMergeModeLocal},
		{name: "no commit", edit: func(r *Record) { r.Commit = "" }, merge: runnerwire.WorkspaceGateNoCommit},
		{name: "outdated runner", custom: true, runner: WorkspaceRunner{Online: true},
			merge: runnerwire.WorkspaceGateOutdated, discard: runnerwire.WorkspaceGateOutdated},
		{name: "actions off", custom: true, runner: WorkspaceRunner{Online: true, Caps: []string{runnerwire.CapRunWorkspace}},
			merge: runnerwire.WorkspaceGateActionsOff, discard: runnerwire.WorkspaceGateActionsOff},
		{name: "offline", custom: true, runner: WorkspaceRunner{Caps: fullRunner.Caps},
			merge: runnerwire.WorkspaceGateOffline, discard: runnerwire.WorkspaceGateOffline},
		{name: "never accepted, runner away", edit: func(r *Record) { r.Status, r.Branch, r.Commit = runnerwire.RunDeclined, "", "" },
			custom: true, merge: runnerwire.WorkspaceGateNoCommit, hubOnly: true},
		{name: "discarded, PR left open", edit: func(r *Record) {
			r.Workspace, r.PRURL, r.PRState = runnerwire.RunWorkspaceDiscarded, "https://x/pull/1", runnerwire.PROpen
		}, mode: runnerwire.WorkspaceMergeModePR, discard: runnerwire.WorkspaceGateDiscarded, done: true},
		{name: "branch in use", custom: true, runner: WorkspaceRunner{Online: true, Caps: fullRunner.Caps, InUse: true},
			merge: runnerwire.WorkspaceGateInUse, discard: runnerwire.WorkspaceGateInUse},
		{name: "PR in use", edit: func(r *Record) { r.PRURL, r.PRState = "https://x/pull/1", runnerwire.PROpen },
			custom: true, runner: WorkspaceRunner{Online: true, Caps: fullRunner.Caps, InUse: true},
			mode: runnerwire.WorkspaceMergeModePR, discard: runnerwire.WorkspaceGateInUse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			if c.edit != nil {
				c.edit(&r)
			}
			runner := fullRunner
			if c.custom {
				runner = c.runner
			}
			g := WorkspaceGate(r, runner)
			if g.MergeMode != c.mode || g.MergeBlocked != c.merge || g.DiscardBlocked != c.discard ||
				g.DiscardDone != c.done || g.DiscardHubOnly != c.hubOnly {
				t.Fatalf("gates = %+v", g)
			}
		})
	}

	// The POST's own checks: a merge must name the commit, an open PR merges
	// there, a discard of a discarded run is no refusal.
	if err := CheckWorkspaceAction(base, fullRunner, runnerwire.WorkspaceMergeLocal, wsCommit); err != nil {
		t.Fatalf("merge at the commit: %v", err)
	}
	if err := CheckWorkspaceAction(base, fullRunner, runnerwire.WorkspaceMergeLocal, "ffff"); BlockedCode(err) != runnerwire.WorkspaceGateHeadMoved || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale head: %v", err)
	}
	withPR := base
	withPR.PRURL, withPR.PRState = "https://x/pull/1", runnerwire.PROpen
	if err := CheckWorkspaceAction(withPR, fullRunner, runnerwire.WorkspaceMergeLocal, wsCommit); BlockedCode(err) != runnerwire.WorkspaceErrHasPR {
		t.Fatalf("open PR: %v", err)
	}
	if err := CheckWorkspaceAction(withPR, fullRunner, runnerwire.WorkspaceDiscard, ""); err != nil {
		t.Fatalf("discard with an open PR: %v", err)
	}
	gone := base
	gone.Workspace = runnerwire.RunWorkspaceDiscarded
	if err := CheckWorkspaceAction(gone, WorkspaceRunner{}, runnerwire.WorkspaceDiscard, ""); err != nil {
		t.Fatalf("discard again: %v", err)
	}
	if err := CheckWorkspaceAction(base, fullRunner, "push", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown action: %v", err)
	}
}

func TestWorkspaceActionStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := finishedRun(t, s)
	a, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceMergeLocal, wsCommit, "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != runnerwire.WorkspacePending || a.RequestedBy != "ann" || a.HeadSHA != wsCommit || a.Base != "main" {
		t.Fatalf("created = %+v", a)
	}
	if _, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceDiscard, "", "main", "u-ann", "ann"); !errors.Is(err, ErrPrecondition) ||
		err.Error() != MsgWorkspacePending {
		t.Fatalf("second pending: %v", err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.WorkspaceAction == nil || got.WorkspaceAction.ID != a.ID {
		t.Fatalf("attached = %+v %v", got.WorkspaceAction, err)
	}
	jobs, err := s.PendingWorkspaceActions(ctx, "r1")
	if err != nil || len(jobs) != 1 || jobs[0].ID != a.ID || jobs[0].AccountID != "u-ann" || jobs[0].RepoID != "repo" ||
		jobs[0].NodeID != "n1" || jobs[0].Branch != "agent/n1" || jobs[0].Base != "main" || jobs[0].HeadSHA != wsCommit ||
		jobs[0].Action != runnerwire.WorkspaceMergeLocal {
		t.Fatalf("pending = %+v %v", jobs, err)
	}
	if other, _ := s.PendingWorkspaceActions(ctx, "r2"); len(other) != 0 {
		t.Fatalf("another runner's = %+v", other)
	}
	// The PR merge is refused while the workspace is busy.
	if err := CheckMergeable(got, testHead); err == nil || err.Error() != MsgWorkspaceBusy {
		t.Fatalf("PR merge while busy: %v", err)
	}

	// Only the run's runner answers, and only for its own action.
	for name, tc := range map[string]struct {
		runner  string
		outcome runnerwire.WorkspaceOutcome
	}{
		"other runner": {"r2", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: r.ID}},
		"wrong run":    {"r1", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: "nope"}},
		"no action":    {"r1", runnerwire.WorkspaceOutcome{ID: "nope", RunID: r.ID}},
	} {
		if _, _, err := s.FinishWorkspaceAction(ctx, tc.runner, tc.outcome); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	done, finished, err := s.FinishWorkspaceAction(ctx, "r1", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: r.ID,
		Action: runnerwire.WorkspaceMergeLocal, MergeCommit: testHead, Note: "merged; could not\x00 remove the worktree"})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != runnerwire.WorkspaceDone || finished.MergeCommit != testHead || finished.FinishedAt == "" ||
		finished.Note != "merged; could not remove the worktree" {
		t.Fatalf("finished = %+v", finished)
	}
	if done.Workspace != runnerwire.RunWorkspaceMergedLocal || done.WorkspaceCommit != testHead || done.Status != runnerwire.RunSucceeded {
		t.Fatalf("run = %+v", done.Run)
	}
	if _, _, err := s.FinishWorkspaceAction(ctx, "r1", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: r.ID, Code: "git"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("a second answer: %v", err)
	}
}

func TestWorkspaceActionRefusedAndExpired(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := finishedRun(t, s)
	a, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceDiscard, "", "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	refused, failed, err := s.FinishWorkspaceAction(ctx, "r1", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: r.ID,
		Code: runnerwire.WorkspaceErrDirtyWorktree, Error: "wip.txt"})
	if err != nil || failed.Status != runnerwire.WorkspaceFailed || failed.Code != runnerwire.WorkspaceErrDirtyWorktree ||
		failed.Reason != "wip.txt" || refused.Workspace != "" {
		t.Fatalf("refused = %+v %+v %v", failed, refused.Run, err)
	}

	// A request nobody answered expires on read; a late answer is still
	// recorded, since it says what happened on the runner's disk.
	b, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceDiscard, "", "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(WorkspaceActionTTL + time.Minute)
	s.SetClock(func() time.Time { return later })
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.WorkspaceAction == nil || got.WorkspaceAction.ID != b.ID || got.WorkspaceAction.Status != runnerwire.WorkspaceExpired ||
		got.WorkspaceAction.Code != runnerwire.WorkspaceExpired {
		t.Fatalf("expired = %+v %v", got.WorkspaceAction, err)
	}
	if jobs, _ := s.PendingWorkspaceActions(ctx, "r1"); len(jobs) != 0 {
		t.Fatalf("an expired action is still sent: %+v", jobs)
	}
	late, _, err := s.FinishWorkspaceAction(ctx, "r1", runnerwire.WorkspaceOutcome{ID: b.ID, RunID: r.ID, PRClosed: true})
	if err != nil || late.Workspace != runnerwire.RunWorkspaceDiscarded || late.WorkspaceAction.Status != runnerwire.WorkspaceDone {
		t.Fatalf("late answer = %+v %v", late.WorkspaceAction, err)
	}
	// A discard is no reason by itself to refuse a PR merge: one that closed
	// the PR is refused for that, one that left it open lets it merge.
	if err := CheckMergeable(late, testHead); err == nil || err.Error() != MsgNoOpenPR {
		t.Fatalf("PR merge after a discard: %v", err)
	}
	openPR := late
	openPR.PRURL, openPR.PRState, openPR.CIStatus, openPR.PRHeadSHA = "https://x/pull/1", runnerwire.PROpen, runnerwire.CISuccess, testHead
	if err := CheckMergeable(openPR, testHead); err != nil {
		t.Fatalf("PR left open by a discard: %v", err)
	}

	// ExpireWorkspaceActions reports what it retired.
	r2 := finishedRun(t, s)
	s.SetClock(time.Now)
	c, err := s.CreateWorkspaceAction(ctx, r2.ID, runnerwire.WorkspaceDiscard, "", "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return later.Add(time.Hour) })
	expired, err := s.ExpireWorkspaceActions(ctx)
	if err != nil || len(expired) != 1 || expired[0].ID != c.ID || expired[0].Status != runnerwire.WorkspaceExpired {
		t.Fatalf("expired = %+v %v", expired, err)
	}
}

func TestMarkDiscarded(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, rec := newTestDispatcher(s)
	r := createRun(t, s, "n1")
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	ann := Approver{ID: "u-ann", Name: "ann"}
	// Never accepted: discarded on the hub at once, whatever its runner.
	done, err := d.WorkspaceAction(ctx, r.ID, ann, runnerwire.WorkspaceDiscard, "")
	if err != nil {
		t.Fatal(err)
	}
	if done.Workspace != runnerwire.RunWorkspaceDiscarded || done.WorkspaceAction == nil ||
		done.WorkspaceAction.Status != runnerwire.WorkspaceDone || done.Status != runnerwire.RunCancelled {
		t.Fatalf("discarded = %+v", done.Run)
	}
	if len(rec.audited) != 2 || rec.audited[0].Action != "run.workspace.discard.request" || rec.audited[1].Action != "run.workspace.discard.done" {
		t.Fatalf("audit = %+v", rec.audited)
	}
	// Again: nothing new.
	again, err := d.WorkspaceAction(ctx, r.ID, ann, runnerwire.WorkspaceDiscard, "")
	if err != nil || again.WorkspaceAction.ID != done.WorkspaceAction.ID || len(rec.audited) != 2 {
		t.Fatalf("again = %+v %v", again.WorkspaceAction, err)
	}
	// A run with a worktree is not the hub's to discard alone.
	if _, _, err := s.MarkDiscarded(ctx, finishedRun(t, s).ID, "u-ann", "ann"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("MarkDiscarded with a branch: %v", err)
	}
}

func TestWorkspaceDispatcher(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, rec := newTestDispatcher(s)
	r := finishedRun(t, s)
	ann := Approver{ID: "u-ann", Name: "ann"}
	// The registry knows no runner: an outdated, offline one.
	if _, err := d.WorkspaceAction(ctx, r.ID, ann, runnerwire.WorkspaceMergeLocal, wsCommit); BlockedCode(err) != runnerwire.WorkspaceGateOutdated {
		t.Fatalf("no runner: %v", err)
	}
	if len(rec.audited) != 0 {
		t.Fatalf("a refusal was audited as a request: %+v", rec.audited)
	}

	// A request recorded directly (as a reachable runner would get it) is
	// answered and audited; whoever waits on it wakes up.
	a, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceMergeLocal, wsCommit, "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	woke := make(chan struct{})
	go func() {
		d.AwaitWorkspaceAction(ctx, a.ID, time.Minute)
		close(woke)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := d.WorkspaceResult(ctx, "r1", runnerwire.WorkspaceOutcome{ID: a.ID, RunID: r.ID, Code: runnerwire.WorkspaceErrConflict,
		Error: "README"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-woke:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiter never woke")
	}
	if len(rec.audited) != 1 || rec.audited[0].Action != "run.workspace.merge.failed" || rec.audited[0].ActorID != "u-ann" ||
		rec.audited[0].Detail["code"] != runnerwire.WorkspaceErrConflict {
		t.Fatalf("audit = %+v", rec.audited)
	}
	if len(rec.notified) != 1 || rec.notified[0] != r.ID {
		t.Fatalf("notified = %+v", rec.notified)
	}
	// An answered action does not keep anyone waiting.
	start := time.Now()
	d.AwaitWorkspaceAction(ctx, a.ID, time.Minute)
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited on an answered action")
	}

	// Expiry is audited as the hub's.
	b, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceDiscard, "", "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(WorkspaceActionTTL + time.Minute)
	s.SetClock(func() time.Time { return later })
	d.SweepWorkspaceActions(ctx)
	last := rec.audited[len(rec.audited)-1]
	if last.Action != "run.workspace.discard.expired" || last.Detail["actionId"] != b.ID {
		t.Fatalf("expiry audit = %+v", last)
	}
}

// An accept that comes after the run was cancelled still records its
// branch: the runner made a worktree, so a discard goes to it, and a discard
// the hub did alone meanwhile no longer stands.
func TestLateAcceptKeepsBranch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, _ := newTestDispatcher(s)
	r := createRun(t, s, "n1")
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	ann := Approver{ID: "u-ann", Name: "ann"}
	if done, err := d.WorkspaceAction(ctx, r.ID, ann, runnerwire.WorkspaceDiscard, ""); err != nil ||
		done.Workspace != runnerwire.RunWorkspaceDiscarded {
		t.Fatalf("hub-only discard = %+v %v", done.Run, err)
	}
	// Another runner's word changes nothing.
	if err := d.Accept(ctx, "r2", runnerwire.Accept{RunID: r.ID, Branch: "agent/n1-x"}); err == nil {
		t.Fatal("another runner's accept was taken")
	}
	if got, _ := s.Get(ctx, r.ID); got.Branch != "" {
		t.Fatalf("branch from another runner: %q", got.Branch)
	}
	if err := d.Accept(ctx, "r1", runnerwire.Accept{RunID: r.ID, Branch: "agent/n1-run"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("late accept: %v", err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.Branch != "agent/n1-run" || got.Workspace != runnerwire.RunWorkspaceLive || got.Status != runnerwire.RunCancelled {
		t.Fatalf("after the late accept = %+v %v", got.Run, err)
	}
	g := WorkspaceGate(got, fullRunner)
	if g.DiscardHubOnly || g.DiscardBlocked != "" || g.DiscardDone {
		t.Fatalf("gates = %+v", g)
	}
}

// Another unfinished run on the same branch or PR, or the run's pipeline
// still running, keeps the run's branch in use; the discard job carries the
// run's commit.
func TestWorkspaceInUse(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, _ := newTestDispatcher(s)
	r := finishedRun(t, s)
	if inUse, err := s.WorkspaceInUse(ctx, r); err != nil || inUse {
		t.Fatalf("alone: %v %v", inUse, err)
	}
	revise, err := s.Create(ctx, New{Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann", Agent: "claude",
		RepoID: "repo", Kind: runnerwire.JobRevise, JobBranch: r.Branch, CreatedBy: "u-ann"})
	if err != nil {
		t.Fatal(err)
	}
	if inUse, err := s.WorkspaceInUse(ctx, r); err != nil || !inUse {
		t.Fatalf("revise queued on the branch: %v %v", inUse, err)
	}
	if runner := d.WorkspaceRunnerOf(ctx, r); !runner.InUse {
		t.Fatalf("runner = %+v", runner)
	}
	if _, err := d.WorkspaceAction(ctx, r.ID, Approver{ID: "u-ann"}, runnerwire.WorkspaceDiscard, ""); BlockedCode(err) != runnerwire.WorkspaceGateInUse {
		t.Fatalf("discard while in use: %v", err)
	}
	if _, err := s.Transition(ctx, revise.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	if inUse, err := s.WorkspaceInUse(ctx, r); err != nil || inUse {
		t.Fatalf("revise ended: %v %v", inUse, err)
	}

	// A running pipeline.
	p, err := s.CreatePipeline(ctx, NewPipeline{OwnerID: "u-ann", CreatedBy: "u-ann", Project: ".", NodeID: "n1",
		Pipeline: runnerwire.Pipeline{Implement: am("claude", "opus"), Review: am("codex", "gpt-5"), MaxRounds: 2}})
	if err != nil {
		t.Fatal(err)
	}
	staged := r
	staged.PipelineID = p.ID
	if inUse, err := s.WorkspaceInUse(ctx, staged); err != nil || !inUse {
		t.Fatalf("pipeline running: %v %v", inUse, err)
	}
	if _, err := s.EndPipeline(ctx, p.ID, runnerwire.PipelineFailed, "done"); err != nil {
		t.Fatal(err)
	}
	if inUse, err := s.WorkspaceInUse(ctx, staged); err != nil || inUse {
		t.Fatalf("pipeline ended: %v %v", inUse, err)
	}

	// The discard job names the run's commit, so the runner keeps commits
	// added on top.
	a, err := s.CreateWorkspaceAction(ctx, r.ID, runnerwire.WorkspaceDiscard, r.Commit, "main", "u-ann", "ann")
	if err != nil {
		t.Fatal(err)
	}
	if job := WorkspaceJob(r, a); job.HeadSHA != r.Commit || job.Action != runnerwire.WorkspaceDiscard {
		t.Fatalf("job = %+v", job)
	}
}
