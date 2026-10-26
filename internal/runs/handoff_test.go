package runs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// newHandoffFixture is the step-up fixture's dispatcher with Claude and Codex
// on the runner, so fallbacks resolve.
func newHandoffFixture(t *testing.T) *stepUpFixture {
	t.Helper()
	f := newStepUpFixture(t)
	if err := f.d.reg.SetHello(context.Background(), f.runner.ID, runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless}, MaxConcurrent: 4,
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// start records, offers and accepts a run.
func (f *stepUpFixture) start(t *testing.T, edit func(*New)) Record {
	t.Helper()
	ctx := context.Background()
	in := New{
		Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: f.runner.ID, RunnerName: "desk",
		OwnerID: "u-ann", Agent: "claude", Model: "opus", RepoID: "repo", RepoName: "repo", Base: "main",
		JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann",
	}
	if edit != nil {
		edit(&in)
	}
	r, err := f.s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Submit(ctx, r)
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/x"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *stepUpFixture) fail(t *testing.T, id string, result runnerwire.RunResult) {
	t.Helper()
	result.Status = runnerwire.RunFailed
	if err := f.d.Finish(context.Background(), f.runner.ID, id, result); err != nil {
		t.Fatal(err)
	}
}

// follower is the run that retries id, if any.
func (f *stepUpFixture) follower(t *testing.T, id string) (Record, bool) {
	t.Helper()
	all, err := f.s.List(context.Background(), Filter{Project: ".", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.RetryOf == id {
			return r, true
		}
	}
	return Record{}, false
}

func (c *offerConn) count(runID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, job := range c.offers {
		if job.RunID == runID {
			n++
		}
	}
	return n
}

func TestFallbackHandsOffThePartialWork(t *testing.T) {
	f := newHandoffFixture(t)
	first := f.start(t, func(in *New) { in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}} })
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "usage limit",
		HandoffBranch: "nodevas/handoff/" + first.ID, HandoffNote: "did half"})

	next, ok := f.follower(t, first.ID)
	if !ok || next.Agent != "codex" || next.ContinuesFrom != "nodevas/handoff/"+first.ID || next.JobHandoffNote != "did half" {
		t.Fatalf("fallback = %+v", next)
	}
	job := f.conn.last(t, next.ID)
	if job.HandoffBranch != "nodevas/handoff/"+first.ID || job.HandoffNote != "did half" || job.Base != "main" {
		t.Fatalf("fallback offer = %+v", job)
	}
	failed, _ := f.s.Get(context.Background(), first.ID)
	if failed.HandoffBranch != job.HandoffBranch || failed.HandoffNote != "did half" {
		t.Fatalf("failed run = %+v", failed.Run)
	}
	events, _, _ := f.s.Events(context.Background(), first.ID, 0, 100)
	if len(events) == 0 || !strings.Contains(events[len(events)-1].Text, "handing off the partial work on nodevas/handoff/") {
		t.Errorf("notes = %+v", events)
	}

	// The fallback stops at the limit before changing anything: the next
	// one still continues from the first run's branch.
	if err := f.d.Accept(context.Background(), f.runner.ID, runnerwire.Accept{RunID: next.ID, Base: "main", Branch: "agent/y"}); err != nil {
		t.Fatal(err)
	}
	f.fail(t, next.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureRateLimit})
	wait, ok := f.follower(t, next.ID)
	if !ok || wait.ContinuesFrom != "nodevas/handoff/"+first.ID || wait.RetryAt == "" || wait.Agent != "codex" {
		t.Fatalf("wait after the fallback = %+v", wait)
	}
}

// A runner's handoff branch must be a handoff branch name, or it is dropped.
func TestFinishDropsAForeignHandoffBranch(t *testing.T) {
	f := newHandoffFixture(t)
	r := f.start(t, func(in *New) { in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}} })
	f.fail(t, r.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, HandoffBranch: "main", HandoffNote: "x"})
	got, _ := f.s.Get(context.Background(), r.ID)
	next, _ := f.follower(t, r.ID)
	if got.HandoffBranch != "" || got.HandoffNote != "" || next.ContinuesFrom != "" || f.conn.last(t, next.ID).HandoffBranch != "" {
		t.Fatalf("foreign branch kept: %+v / %+v", got.Run, next.Run)
	}
	// Nor may a run be created to continue one.
	if _, err := f.s.Create(context.Background(), New{Project: ".", NodeID: "n1", RunnerID: f.runner.ID, OwnerID: "u-ann",
		Agent: "claude", RepoID: "repo", CreatedBy: "u-ann", JobHandoffBranch: "refs/heads/main"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with a foreign handoff branch: %v", err)
	}
}

func TestLimitWaitIsPersistedFiresOnceAndCanBeCancelled(t *testing.T) {
	f := newHandoffFixture(t)
	ctx := context.Background()
	first := f.start(t, nil)
	reset := f.clock.Add(2 * time.Hour).UTC().Truncate(time.Second)
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "Claude AI usage limit reached",
		HandoffBranch: "nodevas/handoff/" + first.ID, HandoffNote: "note", ResetAt: reset.Format(time.RFC3339)})

	wait, ok := f.follower(t, first.ID)
	if !ok || wait.Status != runnerwire.RunQueued || wait.Agent != "claude" || wait.Model != "opus" ||
		wait.ContinuesFrom != "nodevas/handoff/"+first.ID || wait.LimitWaits != 1 {
		t.Fatalf("wait = %+v", wait)
	}
	if want := formatStamp(reset.Add(runnerwire.LimitResetSlack)); wait.RetryAt != want {
		t.Fatalf("retry at %q, want %q", wait.RetryAt, want)
	}
	if f.conn.count(wait.ID) != 0 {
		t.Fatal("a waiting run was offered")
	}
	f.d.Pump(ctx, f.runner.ID)
	if n := f.d.ReleaseDue(ctx); n != 0 || f.conn.count(wait.ID) != 0 {
		t.Fatalf("released %d before the reset", n)
	}

	// A new dispatcher over the same database (a hub restart) picks the
	// wait up once it is over, and offers it once.
	restarted := NewDispatcher(f.s, f.d.reg, nil, nil)
	restarted.now = func() time.Time { return reset.Add(2 * time.Minute) }
	if n := restarted.ReleaseDue(ctx); n != 1 {
		t.Fatalf("released %d after the reset", n)
	}
	if n := restarted.ReleaseDue(ctx); n != 0 {
		t.Fatalf("released again: %d", n)
	}
	job := f.conn.last(t, wait.ID)
	if f.conn.count(wait.ID) != 1 || job.HandoffBranch != "nodevas/handoff/"+first.ID || job.SteppedUp {
		t.Fatalf("offer after the wait = %+v (%d offers)", job, f.conn.count(wait.ID))
	}
	if got, _ := f.s.Get(ctx, wait.ID); got.RetryAt != "" || got.Status != runnerwire.RunOffered {
		t.Fatalf("released run = %+v", got.Run)
	}

	// A wait cancelled is over for good.
	second := f.start(t, nil)
	f.fail(t, second.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureRateLimit})
	again, _ := f.follower(t, second.ID)
	if _, err := f.d.Cancel(ctx, again.ID, "cancelled by ann"); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(3 * time.Hour)
	if n := f.d.ReleaseDue(ctx); n != 0 || f.conn.count(again.ID) != 0 {
		t.Fatalf("a cancelled wait fired")
	}
	if _, ok := f.follower(t, again.ID); ok {
		t.Fatal("a cancelled wait was followed by another")
	}
	if _, err := f.d.RetryNow(ctx, again.ID, "ann"); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("retry now on a cancelled run: %v", err)
	}
}

func TestLimitWaitRespectsBudgetAndBound(t *testing.T) {
	f := newHandoffFixture(t)
	ctx := context.Background()
	r := f.start(t, nil)
	f.fail(t, r.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota})
	wait, _ := f.follower(t, r.ID)
	// The budget runs out while it waits: it is not offered.
	used, _ := f.s.BudgetStatus(ctx, "u-ann")
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: used.Used.Runs}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	f.d.ReleaseDue(ctx)
	got, _ := f.s.Get(ctx, wait.ID)
	if got.Status != runnerwire.RunCancelled || !strings.Contains(got.Error, "daily run budget reached") || f.conn.count(wait.ID) != 0 {
		t.Fatalf("over the budget = %+v", got.Run)
	}
	// Over the budget, a limit is not waited out at all.
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{}); err != nil {
		t.Fatal(err)
	}
	over := f.start(t, nil)
	used, _ = f.s.BudgetStatus(ctx, "u-ann")
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: used.Used.Runs}); err != nil {
		t.Fatal(err)
	}
	f.fail(t, over.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota})
	if _, ok := f.follower(t, over.ID); ok {
		t.Fatal("waited past the budget")
	}
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{}); err != nil {
		t.Fatal(err)
	}

	// A lineage waits at most MaxLimitWaits times.
	tired := f.start(t, func(in *New) { in.LimitWaits = runnerwire.MaxLimitWaits })
	f.fail(t, tired.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota})
	if _, ok := f.follower(t, tired.ID); ok {
		t.Fatal("waited past MaxLimitWaits")
	}
	// A model the login cannot use, or a task failure, is not waited out.
	for _, kind := range []string{runnerwire.FailureModel, runnerwire.FailureTask} {
		other := f.start(t, nil)
		f.fail(t, other.ID, runnerwire.RunResult{FailureKind: kind})
		if _, ok := f.follower(t, other.ID); ok {
			t.Fatalf("waited out a %s failure", kind)
		}
	}
	// Retry now ends a wait early.
	early := f.start(t, nil)
	f.fail(t, early.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureRateLimit})
	w, _ := f.follower(t, early.ID)
	now, err := f.d.RetryNow(ctx, w.ID, "ann")
	if err != nil || now.Status != runnerwire.RunOffered || f.conn.count(w.ID) != 1 {
		t.Fatalf("retry now = %+v, %v", now.Run, err)
	}
	if _, err := f.d.RetryNow(ctx, w.ID, "ann"); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("second retry now: %v", err)
	}
}

// A runner from before handoffs sends no handoff and no reset time: the hub
// waits the default backoff, or what the error text says without a zone,
// and offers jobs whose JSON carries no handoff fields.
func TestOldRunnerLimitWait(t *testing.T) {
	f := newHandoffFixture(t)
	r := f.start(t, nil)
	f.fail(t, r.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "You've hit your usage limit."})
	wait, _ := f.follower(t, r.ID)
	if want := formatStamp(f.clock.Add(runnerwire.DefaultLimitBackoff(runnerwire.FailureQuota))); wait.RetryAt != want || wait.ContinuesFrom != "" {
		t.Fatalf("default backoff: %+v (want %s)", wait.Run, want)
	}
	other := f.start(t, nil)
	f.fail(t, other.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "Upgrade to Pro or try again in 3 hours."})
	w2, _ := f.follower(t, other.ID)
	if want := formatStamp(f.clock.Add(3*time.Hour + runnerwire.LimitResetSlack)); w2.RetryAt != want {
		t.Fatalf("parsed backoff: %s, want %s", w2.RetryAt, want)
	}
	raw, _ := json.Marshal(f.conn.last(t, r.ID))
	if strings.Contains(string(raw), "handoff") {
		t.Fatalf("a plain offer carries handoff fields: %s", raw)
	}
}
