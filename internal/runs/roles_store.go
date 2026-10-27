package runs

// Per-owner role settings: max concurrent runs and daily budget per role
// (table run_role_settings). Agent, model and fallbacks per role are routing
// rules with match "role".

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// MaxRoleSettings caps one owner's role settings rows.
const MaxRoleSettings = 256

// MaxRoleConcurrent caps a role's concurrency setting.
const MaxRoleConcurrent = 64

// ValidateRoleSettings checks a whole role settings list.
func ValidateRoleSettings(settings []runnerwire.RoleSettings) ([]runnerwire.RoleSettings, error) {
	if len(settings) > MaxRoleSettings {
		return nil, invalidf("at most %d role settings", MaxRoleSettings)
	}
	out := make([]runnerwire.RoleSettings, 0, len(settings))
	seen := map[string]bool{}
	for i, setting := range settings {
		setting.Role = strings.TrimSpace(setting.Role)
		setting.Project = strings.TrimSpace(setting.Project)
		if !engine.ValidRoleID(setting.Role) {
			return nil, invalidf("role setting %d: %q is not a role", i+1, setting.Role)
		}
		if len(setting.Project) > maxRouteProject || !utf8.ValidString(setting.Project) || strings.ContainsAny(setting.Project, "\x00\r\n") {
			return nil, invalidf("role setting %d: project is not a valid project name", i+1)
		}
		key := setting.Project + "\x00" + setting.Role
		if seen[key] {
			return nil, invalidf("role setting %d: %s is set twice for this project", i+1, setting.Role)
		}
		seen[key] = true
		if setting.MaxConcurrent < 0 || setting.MaxConcurrent > MaxRoleConcurrent {
			return nil, invalidf("role setting %d: maxConcurrent must be 0 (no limit) to %d", i+1, MaxRoleConcurrent)
		}
		if err := ValidateBudget(runnerwire.Budget{DailyRuns: setting.DailyRuns, DailyCostUSD: setting.DailyCostUSD,
			DailyTokens: setting.DailyTokens}); err != nil {
			return nil, err
		}
		out = append(out, setting)
	}
	return out, nil
}

// RoleSettings returns an owner's role settings, by project then role.
func (s *Store) RoleSettings(ctx context.Context, ownerID string) ([]runnerwire.RoleSettings, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT role, project, max_concurrent, daily_cost_usd, daily_tokens, daily_runs
FROM run_role_settings WHERE owner_id = ? ORDER BY project, role`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []runnerwire.RoleSettings{}
	for rows.Next() {
		var setting runnerwire.RoleSettings
		var maxConcurrent, runs int64
		if err := rows.Scan(&setting.Role, &setting.Project, &maxConcurrent, &setting.DailyCostUSD,
			&setting.DailyTokens, &runs); err != nil {
			return nil, err
		}
		setting.MaxConcurrent, setting.DailyRuns = int(maxConcurrent), int(runs)
		out = append(out, setting)
	}
	return out, rows.Err()
}

// SetRoleSettings replaces an owner's role settings and returns what was
// stored.
func (s *Store) SetRoleSettings(ctx context.Context, ownerID string, settings []runnerwire.RoleSettings) ([]runnerwire.RoleSettings, error) {
	if ownerID == "" {
		return nil, invalidf("missing owner")
	}
	clean, err := ValidateRoleSettings(settings)
	if err != nil {
		return nil, err
	}
	now := s.stamp()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM run_role_settings WHERE owner_id = ?`, ownerID); err != nil {
			return err
		}
		for _, setting := range clean {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO run_role_settings (owner_id, project, role, max_concurrent, daily_cost_usd, daily_tokens, daily_runs, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				ownerID, setting.Project, setting.Role, int64(setting.MaxConcurrent), setting.DailyCostUSD,
				setting.DailyTokens, int64(setting.DailyRuns), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.RoleSettings(ctx, ownerID)
}

// roleSetting is the setting that applies to a role's runs in project: the
// project's own, else the owner's for every project. ok is false for none.
func (s *Store) roleSetting(ctx context.Context, ownerID, project, role string) (runnerwire.RoleSettings, bool, error) {
	settings, err := s.RoleSettings(ctx, ownerID)
	if err != nil {
		return runnerwire.RoleSettings{}, false, err
	}
	var global *runnerwire.RoleSettings
	for i := range settings {
		if settings[i].Role != role {
			continue
		}
		if settings[i].Project == project && project != "" {
			return settings[i], true, nil
		}
		if settings[i].Project == "" {
			global = &settings[i]
		}
	}
	if global != nil {
		return *global, true, nil
	}
	return runnerwire.RoleSettings{}, false, nil
}

// roleBusy counts an owner's offered and running runs in role (in project,
// when scoped).
func (s *Store) roleBusy(ctx context.Context, ownerID, project, role string) (int, error) {
	query := `SELECT COUNT(*) FROM runs WHERE owner_id = ? AND role = ? AND status IN (?, ?)`
	args := []any{ownerID, role, runnerwire.RunOffered, runnerwire.RunRunning}
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// roleHasRoom reports whether a queued run in role may be offered now under
// its owner's max concurrent setting for the role.
func (s *Store) roleHasRoom(ctx context.Context, r Record) (bool, error) {
	if r.Role == "" {
		return true, nil
	}
	setting, ok, err := s.roleSetting(ctx, r.OwnerID, r.Project, r.Role)
	if err != nil || !ok || setting.MaxConcurrent <= 0 {
		return err == nil, err
	}
	busy, err := s.roleBusy(ctx, r.OwnerID, setting.Project, r.Role)
	if err != nil {
		return false, err
	}
	return busy < setting.MaxConcurrent, nil
}

// RoleBudgetStatus is an owner's budget use today for runs in one role,
// against the owner's role setting for every project.
func (s *Store) RoleBudgetStatus(ctx context.Context, ownerID, role string) (runnerwire.BudgetStatus, error) {
	var out runnerwire.BudgetStatus
	setting, ok, err := s.roleSetting(ctx, ownerID, "", role)
	if err != nil {
		return out, err
	}
	if ok {
		out.Budget = runnerwire.Budget{DailyRuns: setting.DailyRuns, DailyCostUSD: setting.DailyCostUSD, DailyTokens: setting.DailyTokens}
	}
	now := s.clock().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := s.db.QueryContext(ctx,
		`SELECT usage, cost_usd, cost_settled FROM runs WHERE owner_id = ? AND role = ? AND created_at >= ? AND created_at < ? AND kind <> ?`,
		ownerID, role, formatStamp(day), formatStamp(day.AddDate(0, 0, 1)), runnerwire.JobVerify)
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

// RoleBudgetMessage says, for a person, why a run in role is refused.
func RoleBudgetMessage(role string, st runnerwire.BudgetStatus) string {
	return "role " + role + ": " + BudgetMessage(st)
}

// SetOnApproved sets the hook a staged pipeline calls when it is approved:
// the server marks the node done (by "pipeline") with the evidence summary.
// Call it before serving.
func (d *Dispatcher) SetOnApproved(fn func(project, nodeID, evidence string)) { d.onApproved = fn }

// SetEvidence sets the hook that records a pipeline evidence event on a
// node's journal (store.AppendEvidence). Call it before serving.
func (d *Dispatcher) SetEvidence(fn func(project, nodeID, runID, kind, outcome, summary string)) {
	d.evidence = fn
}

// recordEvidence stores a run_evidence row for r and notes it on the node's
// journal, naming the run.
func (d *Dispatcher) recordEvidence(r Record, kind, outcome, summary string) {
	if d.store != nil {
		if err := d.store.AddEvidence(context.Background(), r, kind, outcome, summary); err != nil {
			slog.Warn("runs: store evidence", slog.String("run", r.ID), slog.Any("error", err))
		}
	}
	if d.evidence != nil {
		d.evidence(r.Project, r.NodeID, r.ID, kind, outcome, summary+" (run "+shortRunID(r.ID)+")")
	}
}

func shortRunID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
