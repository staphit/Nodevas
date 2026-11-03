package db_test

import (
	"context"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

// 0025_agent_roles: the run's role and evidence, the staged pipeline's
// phase, the autopilot's goal phase and QA rounds, and per-owner role
// settings.
var agentRoleColumns = map[string][]string{
	"runs":           {"role", "verify", "checks", "override"},
	"run_pipelines":  {"stages", "phase", "test_files", "guard", "tries"},
	"autopilot_runs": {"stages", "goal_phase", "qa_round", "qa_run_id"},
	"run_role_settings": {"owner_id", "project", "role", "max_concurrent", "daily_cost_usd",
		"daily_tokens", "daily_runs", "updated_at"},
}

func checkAgentRoleSchema(t *testing.T, database *db.DB) {
	t.Helper()
	ctx := context.Background()
	var applied int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = '0025_agent_roles'`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("0025_agent_roles applied %d times", applied)
	}
	for table, columns := range agentRoleColumns {
		for _, column := range columns {
			// Selecting the column fails if it is missing.
			rows, err := database.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE 1 = 0`)
			if err != nil {
				t.Errorf("%s.%s: %v", table, column, err)
				continue
			}
			_ = rows.Close()
		}
	}
	// Existing rows read as legacy: the defaults are empty.
	if _, err := database.ExecContext(ctx, `INSERT INTO run_role_settings (owner_id, project, role, updated_at) VALUES ('u', '', 'reviewer', 'now')`); err != nil {
		t.Fatalf("insert role settings: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_role_settings (owner_id, project, role, updated_at) VALUES ('u', '', 'reviewer', 'again')`); err == nil {
		t.Fatal("two role settings rows for one owner, project and role")
	}
}

func TestMigration0025(t *testing.T) {
	checkAgentRoleSchema(t, testpg.New(t))
}
