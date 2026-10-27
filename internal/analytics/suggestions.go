package analytics

import (
	"context"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

// SuggestInput is what the suggestion rules read besides run history.
type SuggestInput struct {
	OwnerID, Project string
	Days             int
	Now              time.Time
	Rules            []runnerwire.RouteRule
	Settings         Settings
	// RoleDailyCost is each role's project-"" dailyCostUsd budget.
	RoleDailyCost map[string]float64
	// TesterStage: the project's workflow has a Tester stage.
	TesterStage bool
	// IncludeDismissed ignores dismissals (apply recomputes with it).
	IncludeDismissed bool
}

// Suggestions computes the deterministic suggestions: by rule, then key, at
// most MaxSuggestions, dismissed keys hidden for 30 days.
func Suggestions(ctx context.Context, database *db.DB, in SuggestInput) ([]Suggestion, error) {
	// TODO(green): implement.
	return nil, nil
}

// Dismiss hides a suggestion key for owner for 30 days from now.
func Dismiss(ctx context.Context, database *db.DB, ownerID, key string, now time.Time) error {
	// TODO(green): implement.
	return nil
}
