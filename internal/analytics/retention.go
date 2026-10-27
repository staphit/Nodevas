package analytics

import (
	"context"
	"time"

	"nodevas/internal/db"
)

// Retention: raw events age out; totals, verdicts and evidence are kept.

// RetentionSettingKey is the settings key of the retention days.
const RetentionSettingKey = "analytics.retention_days"

// Retention bounds, in days; 0 keeps everything forever.
const (
	DefaultRetentionDays = 180
	MinRetentionDays     = 30
	MaxRetentionDays     = 3650
	PruneBatch           = 5000
)

// PruneResult counts what Prune removed.
type PruneResult struct {
	Events  int `json:"events"`
	Actions int `json:"actions"`
	Picks   int `json:"picks"`
	Patches int `json:"patches"`
}

// ValidRetentionDays reports whether days may be stored.
func ValidRetentionDays(days int) bool {
	// TODO(green): implement.
	return false
}

// Prune deletes raw history older than days before now.
func Prune(ctx context.Context, database *db.DB, now time.Time, days int) (PruneResult, error) {
	// TODO(green): implement.
	return PruneResult{}, nil
}
