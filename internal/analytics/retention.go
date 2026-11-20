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
	// TouchedRetentionDays is the fixed retention for Layer 2 touched-file and
	// overlap records (§C.5); it runs even when analytics retention is off.
	TouchedRetentionDays = 7
	PruneBatch           = 5000
)

// PruneResult counts what Prune removed.
type PruneResult struct {
	Events  int `json:"events"`
	Actions int `json:"actions"`
	Picks   int `json:"picks"`
	Patches int `json:"patches"`
	// Refusals are audit rows of refused attempts (AuditRefusalActions).
	Refusals int `json:"refusals"`
	// Touched and Overlaps are Layer 2 coordination rows aged out past 7 days.
	Touched  int `json:"touched"`
	Overlaps int `json:"overlaps"`
}

// Total is every row Prune removed or cleared.
func (r PruneResult) Total() int {
	return r.Events + r.Actions + r.Picks + r.Patches + r.Refusals + r.Touched + r.Overlaps
}

// ValidRetentionDays reports whether days may be stored.
func ValidRetentionDays(days int) bool {
	return days == 0 || (days >= MinRetentionDays && days <= MaxRetentionDays)
}

var batchLimit = strconv.Itoa(PruneBatch)

// AuditRefusalActions are the audit actions of refused attempts: a sign-in
// step, a step-up, an authenticator or recovery code, a device-link redeem.
// A stranger can add them for as long as the limits in front let them, so
// they age out with the retention setting; the rest of the trail stays.
var AuditRefusalActions = []string{"auth.signin.failed", "auth.stepup.failed", "auth.totp.failed", "runner.link.failed"}

var auditRefusalList = func() string {
	quoted := make([]string, len(AuditRefusalActions))
	for i, action := range AuditRefusalActions {
		quoted[i] = "'" + action + "'"
	}
	return strings.Join(quoted, ", ")
}()

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
// patches of old changes (marked patch_pruned; their file lists and counts
// stay). Runs, verdicts, evidence, QA rounds and usage are
// the aggregates and stay. Audit rows of refused attempts
// (AuditRefusalActions) age out too. days 0 keeps everything.
func Prune(ctx context.Context, database *db.DB, now time.Time, days int) (PruneResult, error) {
	var out PruneResult
	// Layer 2 touched-file and overlap rows always age out after 7 days, even
	// when analytics retention is turned off (days <= 0; §C.5).
	touchedCutoff := formatStamp(now.AddDate(0, 0, -TouchedRetentionDays))
	touchedSteps := []struct {
		count *int
		query string
		args  []any
	}{
		{&out.Touched, `DELETE FROM run_touched_files WHERE ctid IN (
  SELECT t.ctid FROM run_touched_files t JOIN runs r ON r.id = t.run_id
   WHERE r.finished_at <> '' AND r.finished_at < ? LIMIT ` + batchLimit + `)`, []any{touchedCutoff}},
		{&out.Overlaps, `DELETE FROM run_overlaps WHERE ctid IN (
  SELECT o.ctid FROM run_overlaps o
    JOIN runs ra ON ra.id = o.run_a
    JOIN runs rb ON rb.id = o.run_b
   WHERE ra.finished_at <> '' AND ra.finished_at < ?
     AND rb.finished_at <> '' AND rb.finished_at < ?
   LIMIT ` + batchLimit + `)`, []any{touchedCutoff, touchedCutoff}},
	}
	for _, step := range touchedSteps {
		for {
			res, err := database.ExecContext(ctx, step.query, step.args...)
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
	if days <= 0 {
		return out, nil
	}
	cutoff := formatStamp(now.AddDate(0, 0, -days))
	// The audit trail writes its times as RFC3339 seconds.
	auditCutoff := now.AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	steps := []struct {
		count  *int
		query  string
		cutoff string
	}{
		{&out.Events, `DELETE FROM run_events WHERE ctid IN (
  SELECT e.ctid FROM run_events e JOIN runs r ON r.id = e.run_id
   WHERE r.finished_at <> '' AND r.finished_at < ? LIMIT ` + batchLimit + `)`, ""},
		{&out.Actions, `DELETE FROM agent_session_actions WHERE id IN (
  SELECT id FROM agent_session_actions WHERE at < ? LIMIT ` + batchLimit + `)`, ""},
		{&out.Picks, `DELETE FROM route_picks WHERE id IN (
  SELECT id FROM route_picks WHERE created_at < ? LIMIT ` + batchLimit + `)`, ""},
		{&out.Patches, `UPDATE run_changes SET patch = '', patch_pruned = TRUE WHERE run_id IN (
  SELECT run_id FROM run_changes WHERE created_at < ? AND patch <> '' LIMIT ` + batchLimit + `)`, ""},
		{&out.Refusals, `DELETE FROM audit_events WHERE id IN (
  SELECT id FROM audit_events WHERE at < ? AND action IN (` + auditRefusalList + `) LIMIT ` + batchLimit + `)`, auditCutoff},
	}
	for _, step := range steps {
		for {
			stepCutoff := cutoff
			if step.cutoff != "" {
				stepCutoff = step.cutoff
			}
			res, err := database.ExecContext(ctx, step.query, stepCutoff)
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
