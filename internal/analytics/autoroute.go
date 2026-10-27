package analytics

import (
	"context"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

// RunnerCandidate is one of the owner's runners as auto routing sees it.
type RunnerCandidate struct {
	ID     string
	Name   string
	Online bool
	Info   runnerwire.Info
}

// RouteInput is one routing decision's inputs. The caller resolves the
// manual rules (runs.Suggest) and the budgets, so this package needs
// neither internal/runs nor internal/runners.
type RouteInput struct {
	OwnerID, Project, NodeID string
	// Role is the run's role id; Slot its built-in base role.
	Role, Slot string
	// Manual is what the owner's routing rules picked (RuleID "" none).
	Manual     runnerwire.RouteSuggestion
	Runners    []RunnerCandidate
	Budget     runnerwire.BudgetStatus
	RoleBudget runnerwire.BudgetStatus
	Prices     usage.Table
	Now        time.Time
}

// Route picks an agent and model for a node: the owner's manual rule when
// one matches, else (auto routing on) the cheapest combination meeting the
// role's required tier. Every decision while auto routing is on is logged.
func Route(ctx context.Context, database *db.DB, in RouteInput) (runnerwire.RouteSuggestion, error) {
	// TODO(green): implement.
	return in.Manual, nil
}

// ExploreBucket is the deterministic 0..99 bucket exploration compares
// with the exploration share: fnv32a(project NUL node NUL role) % 100.
func ExploreBucket(project, nodeID, role string) int {
	// TODO(green): implement.
	return -1
}
