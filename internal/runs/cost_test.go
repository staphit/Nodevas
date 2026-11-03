package runs

import (
	"context"
	"math"
	"testing"

	"nodevas/internal/runnerwire"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// codexRun records a queued codex run on model "default" and starts it.
func codexRun(t *testing.T, s *Store, node string) Record {
	t.Helper()
	ctx := context.Background()
	r, err := s.Create(ctx, New{
		Project: ".", NodeID: node, NodeTitle: "Title", RunnerID: "r1", RunnerName: "desk",
		OwnerID: "u-ann", Agent: "codex", Model: "default", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann", Role: "executor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// A codex run of 1M input + 100k output tokens reporting $0: an estimate of
// $2.25 at gpt-5.5's price.
var codexUsage = runnerwire.Usage{InputTokens: 1_000_000, OutputTokens: 100_000}

// B-03: the run's cost is settled when its usage is recorded.
func TestFinishSettlesTheRunsCost(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := codexRun(t, s, "n1")
	usage := codexUsage
	done, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: &usage})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(done.CostUSD, 2.25) || !done.CostEstimated {
		t.Fatalf("finished run cost = %v estimated %v, want 2.25 estimated", done.CostUSD, done.CostEstimated)
	}

	// A reported cost wins and is not an estimate.
	r2 := codexRun(t, s, "n2")
	reported := runnerwire.Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.4}
	done, err = s.Finish(ctx, r2.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: &reported})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(done.CostUSD, 0.4) || done.CostEstimated {
		t.Fatalf("reported run cost = %v estimated %v, want 0.4 reported", done.CostUSD, done.CostEstimated)
	}
}

func TestUsageEventSettlesTheRunsCost(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := codexRun(t, s, "n1")
	usage := codexUsage
	if _, err := s.AppendEvents(ctx, r.ID, []runnerwire.RunEvent{{Seq: 1, Type: runnerwire.EventUsage, Usage: &usage}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(got.CostUSD, 2.25) || !got.CostEstimated {
		t.Fatalf("live usage cost = %v estimated %v, want 2.25 estimated", got.CostUSD, got.CostEstimated)
	}
}

func TestBackfillCostsSettlesLegacyRows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := codexRun(t, s, "n1")
	// A run recorded before cost_usd existed: usage stored, cost unsettled.
	if _, err := s.db.ExecContext(ctx, `UPDATE runs SET usage = ?, cost_usd = 0, cost_estimated = false, cost_settled = false WHERE id = ?`,
		`{"inputTokens":1000000,"outputTokens":100000}`, r.ID); err != nil {
		t.Fatalf("legacy row: %v", err)
	}
	n, err := s.BackfillCosts(ctx)
	if err != nil || n != 1 {
		t.Fatalf("backfill = %d, %v; want 1 row", n, err)
	}
	got, _ := s.Get(ctx, r.ID)
	if !approx(got.CostUSD, 2.25) || !got.CostEstimated {
		t.Fatalf("backfilled cost = %v estimated %v", got.CostUSD, got.CostEstimated)
	}
	if n, err := s.BackfillCosts(ctx); err != nil || n != 0 {
		t.Fatalf("second backfill = %d, %v; want nothing left", n, err)
	}
}

// B-04: a CLI that reports $0 still counts toward the cost budget.
func TestBudgetCountsEstimatedCost(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyCostUSD: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRoleSettings(ctx, "u-ann", []runnerwire.RoleSettings{{Role: "executor", DailyCostUSD: 2}}); err != nil {
		t.Fatal(err)
	}
	r := codexRun(t, s, "n1")
	usage := codexUsage
	if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: &usage}); err != nil {
		t.Fatal(err)
	}
	st, err := s.BudgetStatus(ctx, "u-ann")
	if err != nil {
		t.Fatal(err)
	}
	if st.Exceeded != ExceededCost || !approx(st.Used.CostUSD, 2.25) {
		t.Fatalf("owner budget = %+v, want cost exceeded at 2.25", st)
	}
	role, err := s.RoleBudgetStatus(ctx, "u-ann", "executor")
	if err != nil {
		t.Fatal(err)
	}
	if role.Exceeded != ExceededCost || !approx(role.Used.CostUSD, 2.25) {
		t.Fatalf("executor budget = %+v, want cost exceeded at 2.25", role)
	}
}
