package analytics

import (
	"context"

	"nodevas/internal/db"
)

// ScorecardsFor computes the scorecards of q.OwnerID's runs over q's window
// on q.Track.
func ScorecardsFor(ctx context.Context, database *db.DB, q Query) (Scorecards, error) {
	// TODO(green): implement (metrics.go, units.go, load.go).
	return Scorecards{}, nil
}

// InsightsFor computes q.Project's insights over q's window (without
// suggestions; see Suggestions).
func InsightsFor(ctx context.Context, database *db.DB, q Query) (Insights, error) {
	// TODO(green): implement (insights.go).
	return Insights{}, nil
}

// Contradicted reports, for each verdict run id given, whether later
// evidence contradicts its verdict (rules C1 to C4).
func Contradicted(ctx context.Context, database *db.DB, verdictRunIDs []string) (map[string]bool, error) {
	// TODO(green): implement (contradictions.go).
	return map[string]bool{}, nil
}
