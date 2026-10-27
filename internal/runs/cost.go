package runs

import (
	"context"

	"nodevas/internal/usage"
)

// Run cost (agent analytics): runs.cost_usd is settled from a run's usage
// when it reports one, priced by usage.RunCost, so budgets and analytics sum
// one column instead of re-parsing every usage figure.

// BackfillCosts settles the cost of runs recorded before cost_usd existed
// (cost_settled false, usage set), in batches, and reports how many.
func (s *Store) BackfillCosts(ctx context.Context) (int, error) {
	// TODO(green): implement.
	return 0, nil
}

// SetUsageFromSession records an interactive run's usage from its agent
// session's report: the models summed (model = the one with the most
// tokens) and the cost settled. Only an interactive run whose usage is
// empty or came from an earlier session report is changed.
func (s *Store) SetUsageFromSession(ctx context.Context, runID string, models []usage.ModelUsage) error {
	// TODO(green): implement.
	return nil
}
