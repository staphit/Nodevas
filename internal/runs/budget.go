package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"nodevas/internal/runnerwire"
)

// Budgets (W4): per runner owner, per UTC day, checked when a run is created.
// Zero means no limit. Cost is the CLIs' own estimate, so a budget is a brake,
// not an invoice; runs created at the same moment may overshoot it by one.

// Upper bounds on a budget: anything above is a typo, not a plan.
const (
	MaxDailyRuns    = 10_000
	MaxDailyCostUSD = 100_000.0
	MaxDailyTokens  = int64(1_000_000_000_000)
)

// Exceeded limits, as BudgetStatus.Exceeded names them.
const (
	ExceededRuns   = "runs"
	ExceededCost   = "cost"
	ExceededTokens = "tokens"
)

// ValidateBudget checks a budget a person typed.
func ValidateBudget(b runnerwire.Budget) error {
	switch {
	case b.DailyRuns < 0 || b.DailyCostUSD < 0 || b.DailyTokens < 0:
		return invalidf("budget limits cannot be negative")
	case math.IsNaN(b.DailyCostUSD) || math.IsInf(b.DailyCostUSD, 0):
		return invalidf("dailyCostUsd is not a number")
	case b.DailyRuns > MaxDailyRuns:
		return invalidf("dailyRuns is at most %d", MaxDailyRuns)
	case b.DailyCostUSD > MaxDailyCostUSD:
		return invalidf("dailyCostUsd is at most %.0f", MaxDailyCostUSD)
	case b.DailyTokens > MaxDailyTokens:
		return invalidf("dailyTokens is at most %d", MaxDailyTokens)
	}
	return nil
}

// Budget returns an owner's budget; the zero budget (no limits) when none
// was set.
func (s *Store) Budget(ctx context.Context, ownerID string) (runnerwire.Budget, error) {
	var b runnerwire.Budget
	var runs int64
	err := s.db.QueryRowContext(ctx,
		`SELECT daily_runs, daily_cost_usd, daily_tokens FROM run_budgets WHERE owner_id = ?`, ownerID).
		Scan(&runs, &b.DailyCostUSD, &b.DailyTokens)
	if errors.Is(err, sql.ErrNoRows) {
		return runnerwire.Budget{}, nil
	}
	b.DailyRuns = int(runs)
	return b, err
}

// SetBudget replaces an owner's budget.
func (s *Store) SetBudget(ctx context.Context, ownerID string, b runnerwire.Budget) error {
	if ownerID == "" {
		return invalidf("missing owner")
	}
	if err := ValidateBudget(b); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_budgets (owner_id, daily_runs, daily_cost_usd, daily_tokens, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (owner_id) DO UPDATE SET daily_runs = excluded.daily_runs,
    daily_cost_usd = excluded.daily_cost_usd, daily_tokens = excluded.daily_tokens,
    updated_at = excluded.updated_at`,
		ownerID, int64(b.DailyRuns), b.DailyCostUSD, b.DailyTokens, s.stamp())
	return err
}

// BudgetStatus is an owner's budget and what runs on their runners used
// today (UTC): the runs created today, and the usage those runs reported.
func (s *Store) BudgetStatus(ctx context.Context, ownerID string) (runnerwire.BudgetStatus, error) {
	var out runnerwire.BudgetStatus
	budget, err := s.Budget(ctx, ownerID)
	if err != nil {
		return out, err
	}
	out.Budget = budget
	now := s.clock().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := s.db.QueryContext(ctx,
		`SELECT usage, cost_usd, cost_settled FROM runs WHERE owner_id = ? AND created_at >= ? AND created_at < ? AND kind <> ?`,
		ownerID, formatStamp(day), formatStamp(day.AddDate(0, 0, 1)), runnerwire.JobVerify)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := addBudgetRow(rows, &out); err != nil {
			return out, err
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Exceeded = exceeded(out)
	return out, nil
}

// exceeded names the first limit a new run would break.
func exceeded(st runnerwire.BudgetStatus) string {
	b := st.Budget
	switch {
	case b.DailyRuns > 0 && st.Used.Runs >= b.DailyRuns:
		return ExceededRuns
	case b.DailyCostUSD > 0 && st.Used.CostUSD >= b.DailyCostUSD:
		return ExceededCost
	case b.DailyTokens > 0 && st.Used.Tokens >= b.DailyTokens:
		return ExceededTokens
	}
	return ""
}

// BudgetMessage says, for a person, why a new run is refused.
func BudgetMessage(st runnerwire.BudgetStatus) string {
	switch st.Exceeded {
	case ExceededRuns:
		return fmt.Sprintf("daily run budget reached: %d of %d runs today (UTC)", st.Used.Runs, st.Budget.DailyRuns)
	case ExceededCost:
		return fmt.Sprintf("daily cost budget reached: $%.2f of $%.2f today (UTC)", st.Used.CostUSD, st.Budget.DailyCostUSD)
	case ExceededTokens:
		return fmt.Sprintf("daily token budget reached: %d of %d tokens today (UTC)", st.Used.Tokens, st.Budget.DailyTokens)
	}
	return ""
}

// addBudgetRow counts one run (usage, cost_usd, cost_settled) toward a
// day's use: its settled cost (an estimate for CLIs that report $0), or a
// legacy run's reported cost, and its tokens.
func addBudgetRow(rows interface{ Scan(...any) error }, out *runnerwire.BudgetStatus) error {
	var raw string
	var cost float64
	var settled bool
	if err := rows.Scan(&raw, &cost, &settled); err != nil {
		return err
	}
	out.Used.Runs++
	if settled && cost > 0 && !math.IsInf(cost, 0) && !math.IsNaN(cost) {
		out.Used.CostUSD += cost
	}
	if raw == "" {
		return nil
	}
	var usage runnerwire.Usage
	if json.Unmarshal([]byte(raw), &usage) != nil {
		return nil
	}
	if !settled && usage.CostUSD > 0 && !math.IsInf(usage.CostUSD, 0) {
		out.Used.CostUSD += usage.CostUSD
	}
	if tokens := usage.InputTokens + usage.OutputTokens; tokens > 0 {
		out.Used.Tokens += tokens
	}
	return nil
}
