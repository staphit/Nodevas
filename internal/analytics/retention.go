package analytics

import (
	"context"
	"strconv"
	"strings"
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

// Total is every row Prune removed or cleared.
func (r PruneResult) Total() int { return r.Events + r.Actions + r.Picks + r.Patches }

// ValidRetentionDays reports whether days may be stored.
func ValidRetentionDays(days int) bool {
	return days == 0 || (days >= MinRetentionDays && days <= MaxRetentionDays)
}

var batchLimit = strconv.Itoa(PruneBatch)

// RetentionDays is the stored retention setting, the default when it is
// unset or not a valid value.
func RetentionDays(ctx context.Context, database *db.DB) (int, error) {
	raw, err := database.Setting(ctx, RetentionSettingKey, strconv.Itoa(DefaultRetentionDays))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !ValidRetentionDays(n) {
		return DefaultRetentionDays, nil
	}
	return n, nil
}

// Prune deletes raw history older than days before now: the events of runs
// that finished before the cutoff, session actions, route picks, and the
// patches of old changes. Runs, verdicts, evidence, QA rounds and usage are
// the aggregates and stay. days 0 keeps everything.
func Prune(ctx context.Context, database *db.DB, now time.Time, days int) (PruneResult, error) {
	var out PruneResult
	if days <= 0 {
		return out, nil
	}
	cutoff := formatStamp(now.AddDate(0, 0, -days))
	steps := []struct {
		count *int
		query string
	}{
		{&out.Events, `DELETE FROM run_events WHERE ctid IN (
  SELECT e.ctid FROM run_events e JOIN runs r ON r.id = e.run_id
   WHERE r.finished_at <> '' AND r.finished_at < ? LIMIT ` + batchLimit + `)`},
		{&out.Actions, `DELETE FROM agent_session_actions WHERE id IN (
  SELECT id FROM agent_session_actions WHERE at < ? LIMIT ` + batchLimit + `)`},
		{&out.Picks, `DELETE FROM route_picks WHERE id IN (
  SELECT id FROM route_picks WHERE created_at < ? LIMIT ` + batchLimit + `)`},
		{&out.Patches, `UPDATE run_changes SET patch = '', patch_truncated = 1 WHERE run_id IN (
  SELECT run_id FROM run_changes WHERE created_at < ? AND patch <> '' LIMIT ` + batchLimit + `)`},
	}
	for _, step := range steps {
		for {
			res, err := database.ExecContext(ctx, step.query, cutoff)
			if err != nil {
				return out, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return out, err
			}
			*step.count += int(n)
			if n < PruneBatch {
				break
			}
		}
	}
	return out, nil
}
