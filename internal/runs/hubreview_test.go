package runs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// Tests for the hub findings of the terminal dialog acceptance review.

// queuedGlobal records a queued global run straight in the store, so a test
// can lay out a queue no start path would build in one step.
func queuedGlobal(t *testing.T, s *Store, runner runnerwire.Runner, repo runnerwire.Repo, edit func(*New)) Record {
	t.Helper()
	in := New{
		Global: true, NodeTitle: "Task", BranchSlug: "task", NoPR: true,
		RunnerID: runner.ID, RunnerName: runner.Name, OwnerID: runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: repo.ID, RepoName: repo.Name, Base: "main",
		RepoKey: runnerwire.RepoKey(runner.ID, repo), JobPrompt: "# Task",
		CreatedBy: runner.OwnerID, CreatedByName: "ann", Origin: runnerwire.OriginPerson,
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

// overSpend puts the owner over a daily cost budget with one finished run
// that cost more than the limit.
func overSpend(t *testing.T, f *stepUpFixture, ownerID string) {
	t.Helper()
	ctx := context.Background()
	spent := f.submit(t, "")
	if _, err := f.s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, cost_usd = 5, cost_settled = TRUE WHERE id = ?`,
		runnerwire.RunSucceeded, spent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetBudget(ctx, ownerID, runnerwire.Budget{DailyRuns: 100, DailyCostUSD: 1}); err != nil {
		t.Fatal(err)
	}
}

// Finding 1: Pump cancelled an over-budget group member while holding its
// runner's lock, and the cancel's busyEnded pumped other runners
// synchronously. Two runners whose queues wait on each other's repositories
// then locked each other: Pump(A) -> cancel -> Pump(B) -> cancel -> Pump(A).
func TestPumpBudgetCancelAcrossTwoRunnersDoesNotDeadlock(t *testing.T) {
	ctx := context.Background()
	hello := groupHello(4, "api", "web")
	f, _ := newGlobalFixture(t, hello)
	runnerA, err := f.d.reg.Get(ctx, f.runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-ann", Name: "ann"}, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, created.ID, hello); err != nil {
		t.Fatal(err)
	}
	f.d.reg.Attach(created.ID, &offerConn{})
	runnerB, err := f.d.reg.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	api, web := hello.Repos[0], hello.Repos[1]

	// Runner A: an over-budget group member on api, then a run waiting on web.
	// Runner B: an over-budget group member on web, then a run waiting on api.
	overSpend(t, f, "u-ann")
	memberA := queuedGlobal(t, f.s, runnerA, api, func(in *New) { in.GroupID = "group-a" })
	memberB := queuedGlobal(t, f.s, runnerB, web, func(in *New) { in.GroupID = "group-b" })
	queuedGlobal(t, f.s, runnerA, web, func(in *New) {
		in.BusyChoice, in.AfterRun = runnerwire.OnBusyQueue, memberB.ID
	})
	queuedGlobal(t, f.s, runnerB, api, func(in *New) {
		in.BusyChoice, in.AfterRun = runnerwire.OnBusyQueue, memberA.ID
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.d.Pump(ctx, runnerA.ID)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Pump deadlocked: two runners' budget cancels pumped each other under their locks")
	}
	for _, id := range []string{memberA.ID, memberB.ID} {
		got, err := f.s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != runnerwire.RunCancelled || got.FailureCode != FailureBudget {
			t.Fatalf("member %s = %s/%q, want cancelled/budget", id, got.Status, got.FailureCode)
		}
	}
}

// Finding 4: Pump's cost/token re-check is for queued group members (§10.2);
// any other queued run was checked when it started and is offered as before.
func TestPumpBudgetRecheckOnlyForGroupMembers(t *testing.T) {
	ctx := context.Background()
	hello := groupHello(4, "api", "web")
	f, _ := newGlobalFixture(t, hello)
	runner, err := f.d.reg.Get(ctx, f.runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	overSpend(t, f, "u-ann")
	plain := queuedGlobal(t, f.s, runner, hello.Repos[0], nil)
	member := queuedGlobal(t, f.s, runner, hello.Repos[1], func(in *New) { in.GroupID = "group-1" })

	f.d.Pump(ctx, runner.ID)
	gotPlain, err := f.s.Get(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlain.Status != runnerwire.RunOffered || gotPlain.FailureCode != "" {
		t.Fatalf("plain queued run = %s/%q, want offered (no Pump budget cancel)", gotPlain.Status, gotPlain.FailureCode)
	}
	gotMember, err := f.s.Get(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotMember.Status != runnerwire.RunCancelled || gotMember.FailureCode != FailureBudget {
		t.Fatalf("group member = %s/%q, want cancelled/budget", gotMember.Status, gotMember.FailureCode)
	}
}

// Finding 2: a follow-up's offer carries the prior run's task and its
// summary, the summary redacted and cut to 4 KiB (§6.2).
func TestFollowUpOfferCarriesPriorTaskAndSummary(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Fix the login button", nil)
	secret := "ghp_" + strings.Repeat("A", 36)
	summary := "Changed the button. token=" + secret + "\n" + strings.Repeat("long summary line\n", 400)
	if _, err := f.s.db.ExecContext(ctx, `UPDATE runs SET summary = ? WHERE id = ?`, summary, root.ID); err != nil {
		t.Fatal(err)
	}

	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID, Body: runnerwire.FollowUpRequest{Message: "Make it bigger"},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := f.conn.last(t, fu.ID)
	if job.ContinueTask != root.JobPrompt {
		t.Fatalf("ContinueTask = %q, want the prior's task %q", job.ContinueTask, root.JobPrompt)
	}
	if job.ContinueSummary == "" || !strings.HasPrefix(job.ContinueSummary, "Changed the button.") {
		t.Fatalf("ContinueSummary = %q, want the prior's summary", job.ContinueSummary)
	}
	if strings.Contains(job.ContinueSummary, secret) {
		t.Fatalf("ContinueSummary carries the secret unredacted: %q", job.ContinueSummary[:80])
	}
	if len(job.ContinueSummary) > runnerwire.MaxSummaryBytes {
		t.Fatalf("ContinueSummary is %d bytes, want <= %d", len(job.ContinueSummary), runnerwire.MaxSummaryBytes)
	}
	// A first turn carries neither.
	first := f.conn.last(t, root.ID)
	if first.ContinueTask != "" || first.ContinueSummary != "" {
		t.Fatalf("first turn job carries continue fields: %+v", first)
	}
}

// Finding 3: the continuedBy subquery in every run read must be able to use
// the partial unique index runs_one_continuation; unless the subquery says
// continue_of is not empty, the planner cannot prove the index predicate and
// scans runs once per row.
func TestContinuedBySubqueryUsesPartialIndex(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := createRun(t, s, "n1")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Turning sequential scans off leaves only plans the indexes allow, so the
	// plan names runs_one_continuation exactly when the index is usable.
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, `EXPLAIN SELECT `+runColumns+` FROM runs WHERE id = ?`, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "runs_one_continuation") {
		t.Fatalf("continuedBy subquery does not use runs_one_continuation:\n%s", strings.Join(plan, "\n"))
	}
}

// Finding 5: a follow-up and a merge approval on the same work must not both
// pass: each checks the other under the work's lock in one transaction.
//
// A smoke test only: whether the two calls really overlap is up to the
// scheduler, so a round may pass on code without the locked re-checks.
// TestFollowUpAndMergeWaitForTheWorkLock lines them up deterministically.
func TestFollowUpAndMergeApprovalNeverBothPass(t *testing.T) {
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f, _ := newGlobalFixture(t, continueHello())
			ctx := context.Background()
			t1 := startAndFinishGlobal(t, f, "Turn 1", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
			if err := f.d.PR(ctx, f.runner.ID, t1.ID, runnerwire.PRInfo{
				URL: "https://github.com/o/r/pull/31", Number: 31, State: runnerwire.PROpen,
				CI: runnerwire.CISuccess, HeadSHA: testHead,
			}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			var fuErr, mergeErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, fuErr = f.d.FollowUp(ctx, FollowUpRequest{
					PriorID: t1.ID, Body: runnerwire.FollowUpRequest{Message: "Turn 2"},
					ActorID: "u-ann", ActorName: "ann",
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, mergeErr = f.d.Merge(ctx, t1.ID, Approver{ID: "u-ann", Name: "ann"}, testHead)
			}()
			close(start)
			wg.Wait()
			if fuErr == nil && mergeErr == nil {
				t.Fatal("a follow-up and a merge approval both passed on the same work")
			}
			if fuErr != nil && mergeErr != nil {
				t.Fatalf("both refused: follow-up %v, merge %v", fuErr, mergeErr)
			}
			if fuErr != nil {
				var se *StartError
				if !errors.As(fuErr, &se) || se.Reason != CodeMergePending {
					t.Fatalf("follow-up refusal = %#v, want MERGE_PENDING", fuErr)
				}
			}
			if mergeErr != nil && BlockedCode(mergeErr) != CodeTurnRunning {
				t.Fatalf("merge refusal = %#v, want TURN_RUNNING", mergeErr)
			}
		})
	}
}

// The deterministic form of the test above. The test takes the work's
// advisory lock on a connection of its own (as another hub would), lets the
// first call (follow-up or merge approval) pass its unlocked checks and park
// on that lock inside its transaction, holding the hub's one writer
// connection, then lets the second call pass its unlocked checks too, up to
// where it waits for that connection, and only then lets go. The first call
// passes; the second has read nothing of it before it waited, so only the
// re-check it makes under the lock can refuse it. Code without the lock, or
// without that re-check, lets both through.
func TestFollowUpAndMergeWaitForTheWorkLock(t *testing.T) {
	for _, mergeFirst := range []bool{true, false} {
		name := "follow-up first"
		if mergeFirst {
			name = "merge first"
		}
		t.Run(name, func(t *testing.T) {
			f, _ := newGlobalFixture(t, continueHello())
			ctx := context.Background()
			t1 := startAndFinishGlobal(t, f, "Turn 1", func(r *GlobalStartRequest) { r.Body.OpenPR = true })
			if err := f.d.PR(ctx, f.runner.ID, t1.ID, runnerwire.PRInfo{
				URL: "https://github.com/o/r/pull/32", Number: 32, State: runnerwire.PROpen,
				CI: runnerwire.CISuccess, HeadSHA: testHead,
			}); err != nil {
				t.Fatal(err)
			}

			var fuErr, mergeErr error
			fuDone, mergeDone := make(chan struct{}), make(chan struct{})
			followUp := func() {
				defer close(fuDone)
				_, fuErr = f.d.FollowUp(ctx, FollowUpRequest{
					PriorID: t1.ID, Body: runnerwire.FollowUpRequest{Message: "Turn 2"},
					ActorID: "u-ann", ActorName: "ann",
				})
			}
			merge := func() {
				defer close(mergeDone)
				_, mergeErr = f.d.Merge(ctx, t1.ID, Approver{ID: "u-ann", Name: "ann"}, testHead)
			}
			first, second := followUp, merge
			firstDone, secondDone := fuDone, mergeDone
			if mergeFirst {
				first, second = merge, followUp
				firstDone, secondDone = mergeDone, fuDone
			}

			// Not the writer pool: its one connection is what the calls
			// queue for.
			holder, err := f.d.store.db.Search().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			if err := lockWork(ctx, holder, workRootOf(t1)); err != nil {
				t.Fatal(err)
			}

			go first()
			waitFor(t, "the first call to wait on the work lock", firstDone, func() bool {
				return workLockWaiters(t, f.d.store, workRootOf(t1)) == 1
			})
			writer := f.d.store.db.Writer()
			waits := writer.Stats().WaitCount
			go second()
			waitFor(t, "the second call to wait for the writer connection", secondDone, func() bool {
				return writer.Stats().WaitCount > waits
			})
			for call, done := range map[string]chan struct{}{"first": firstDone, "second": secondDone} {
				select {
				case <-done:
					t.Errorf("the %s call finished while the test held the work lock", call)
				default:
				}
			}
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			<-fuDone
			<-mergeDone

			if mergeFirst {
				if mergeErr != nil {
					t.Fatalf("merge approval (first to the lock) = %v, want it to pass", mergeErr)
				}
				var se *StartError
				if !errors.As(fuErr, &se) || se.Reason != CodeMergePending {
					t.Fatalf("follow-up after the approval = %#v, want MERGE_PENDING", fuErr)
				}
			} else {
				if fuErr != nil {
					t.Fatalf("follow-up (first to the lock) = %v, want it to pass", fuErr)
				}
				if BlockedCode(mergeErr) != CodeTurnRunning {
					t.Fatalf("merge approval after the follow-up = %#v, want TURN_RUNNING", mergeErr)
				}
			}
		})
	}
}

// waitFor waits until cond holds or done is closed (the call it watches
// finished early, which the caller's checks then catch).
func waitFor(t *testing.T, what string, done chan struct{}, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 10s for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// workLockWaiters counts the sessions in this test's database waiting for
// lockWork's advisory lock on workRoot. A bigint advisory key shows as its
// high half in classid and its low half in objid.
func workLockWaiters(t *testing.T, s *Store, workRoot string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM pg_locks
WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
  AND ((classid::bigint << 32) | objid::bigint) = hashtextextended(?, 0)`,
		"busy:work:"+workRoot).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Finding 6: an automatic start that asks to queue on a runner without
// CapCoord waits behind the holder in Pump order instead of failing; a
// person's start there is still refused (they are offered take-over).
func TestAutopilotQueueOnOutdatedRunnerWaits(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)
	oldHello := busyHello(4, runnerwire.CapHeadless, runnerwire.CapInteractive)
	if err := f.d.reg.SetHello(ctx, f.ann.ID, oldHello); err != nil {
		t.Fatal(err)
	}
	holder, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, holder)

	auto, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Origin = runnerwire.OriginAutopilot
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatalf("autopilot start on an outdated runner: %v", err)
	}
	if auto.Status != runnerwire.RunQueued || auto.AfterRun != holder.ID {
		t.Fatalf("autopilot run = %s after %q, want queued after %s", auto.Status, auto.AfterRun, holder.ID)
	}
	f.finishRun(t, holder, runnerwire.RunSucceeded, "done")
	got, err := f.s.Get(ctx, auto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runnerwire.RunOffered {
		t.Fatalf("autopilot run after the holder ended = %s, want offered", got.Status)
	}

	// A person asking to queue on the outdated runner is still refused.
	holder2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, holder2)
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	var refused *StartError
	if !errors.As(err, &refused) || refused.Field != "onBusy" {
		t.Fatalf("person queue on outdated runner = %#v, want refused on onBusy", err)
	}
}

// Finding 8: whether a holder was taken over is read from the stored
// take-over run, not from memory, so a hub restarted between the take-over
// and the holder's end still releases the holder's board claim, even when
// the take-over run itself was withdrawn in the meantime.
func TestTakenOverHolderClaimReleasedAfterRestart(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)
	holder, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, holder)
	// The take-over run as an earlier hub process stored it, since withdrawn.
	over, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "Fix n1", RunnerID: f.ann.ID, RunnerName: f.ann.Name,
		OwnerID: "u-ann", Agent: "claude", Model: "opus", RepoID: "repo-1", RepoName: "api",
		JobPrompt: "# Fix n1", CreatedBy: "u-ann", CreatedByName: "ann",
		AfterRun: holder.ID, BusyChoice: runnerwire.OnBusyTakeOver, Origin: runnerwire.OriginPerson,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Transition(ctx, over.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}

	// A fresh dispatcher over the same store: nothing in memory.
	reg := runners.NewRegistry(f.s.db, func() {})
	reg.Attach(f.ann.ID, &busyConn{})
	d := NewDispatcher(f.s, reg, nil, nil)
	var mu sync.Mutex
	var released []string
	d.SetRunClaimRelease(func(_ context.Context, project, nodeID, runID string) {
		mu.Lock()
		released = append(released, project+"/"+nodeID+"/"+runID)
		mu.Unlock()
	})
	if err := d.Finish(ctx, f.ann.ID, holder.ID, runnerwire.RunResult{Status: runnerwire.RunCancelled}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := "./n1/" + holder.ID; !slices.Contains(released, want) {
		t.Fatalf("released = %v, want %q", released, want)
	}
}

// A run that simply ends on a node nobody took over keeps its claim.
func TestEndedRunNotTakenOverKeepsClaim(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)
	var released []string
	var mu sync.Mutex
	f.d.SetRunClaimRelease(func(_ context.Context, project, nodeID, runID string) {
		mu.Lock()
		released = append(released, runID)
		mu.Unlock()
	})
	r, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, r)
	f.finishRun(t, r, runnerwire.RunSucceeded, "done")
	mu.Lock()
	defer mu.Unlock()
	if len(released) != 0 {
		t.Fatalf("released = %v, want none", released)
	}
}

// Finding 10: a follow-up asked for in Simple mode carries the plain-words
// instruction (the HTTP bodies now take simple; api_test.go).
func TestFollowUpSimpleAddsInstruction(t *testing.T) {
	f, _ := newGlobalFixture(t, continueHello())
	ctx := context.Background()
	root := startAndFinishGlobal(t, f, "Fix the login button", nil)
	fu, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID: root.ID, Body: runnerwire.FollowUpRequest{Message: "Make it bigger"},
		ActorID: "u-ann", ActorName: "ann", Simple: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fu.JobPrompt, SimpleInstruction) {
		t.Fatalf("simple follow-up prompt lacks the instruction: %q", fu.JobPrompt)
	}
}
