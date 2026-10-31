package db_test

import (
	"context"
	"testing"

	"nodevas/internal/testpg"
)

// B-01: 0028_agent_analytics — cost and task shape on runs, session usage
// tied to its run, copilot usage, the session action log, run evidence, QA
// rounds, structured verdicts, analytics settings, route picks and
// dismissed suggestions.
var analyticsColumns = map[string][]string{
	"runs":                {"cost_usd", "cost_estimated", "cost_settled", "node_type", "acceptance_count"},
	"agent_session_usage": {"run_id"},
	"copilot_usage": {"id", "conversation_id", "account_id", "project", "provider", "model",
		"input_tokens", "output_tokens", "created_at"},
	"agent_session_actions": {"id", "project", "session_id", "run_id", "at", "action", "node_id", "detail"},
	"run_evidence":          {"id", "run_id", "pipeline_id", "project", "node_id", "kind", "outcome", "summary", "at"},
	"autopilot_qa_rounds":   {"autopilot_id", "round", "run_id", "outcome", "fix_tasks", "reason", "created_at"},
	"run_verdicts": {"id", "run_id", "project", "node_id", "pipeline_id", "role", "round", "subject_run_id",
		"agent", "model", "outcome", "severity", "reasons", "note", "source", "created_at", "updated_at"},
	"analytics_settings":  {"owner_id", "project", "auto_routing", "exploration_pct", "required_tiers", "updated_at"},
	"route_picks":         {"id", "owner_id", "project", "node_id", "role", "source", "rule_id", "runner_id", "agent", "model", "tier", "required_tier", "explore", "reason", "run_id", "created_at"},
	"analytics_dismissed": {"owner_id", "key", "dismissed_at"},
}

func TestMigration0028(t *testing.T) {
	ctx := context.Background()
	database := testpg.New(t)
	var applied int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = '0028_agent_analytics'`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("0028_agent_analytics applied %d times, want once", applied)
	}
	for table, columns := range analyticsColumns {
		for _, column := range columns {
			rows, err := database.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE 1 = 0`)
			if err != nil {
				t.Errorf("%s.%s: %v", table, column, err)
				continue
			}
			_ = rows.Close()
		}
	}
	if t.Failed() {
		return
	}

	exec := func(query string, args ...any) error {
		_, err := database.ExecContext(ctx, query, args...)
		return err
	}
	if err := exec(`INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at)
VALUES ('r1', 'p', 'n', 'rn', 'u', 'claude', 'repo', 'running', 'u', '2026-10-28T00:00:00.000000Z')`); err != nil {
		t.Fatal(err)
	}
	var cost float64
	var estimated, settled bool
	var nodeType string
	var acceptance int
	if err := database.QueryRowContext(ctx, `SELECT cost_usd, cost_estimated, cost_settled, node_type, acceptance_count FROM runs WHERE id = 'r1'`).
		Scan(&cost, &estimated, &settled, &nodeType, &acceptance); err != nil {
		t.Fatal(err)
	}
	if cost != 0 || estimated || settled || nodeType != "" || acceptance != 0 {
		t.Fatalf("legacy run defaults: cost %v estimated %v settled %v type %q acceptance %d", cost, estimated, settled, nodeType, acceptance)
	}

	verdict := func(outcome, severity string) error {
		return exec(`INSERT INTO run_verdicts (run_id, project, node_id, role, outcome, severity, created_at, updated_at)
VALUES ('r1', 'p', 'n', 'reviewer', ?, ?, 'now', 'now')`, outcome, severity)
	}
	if err := verdict("pass", "none"); err != nil {
		t.Fatalf("first verdict: %v", err)
	}
	if err := verdict("fail", "high"); err == nil {
		t.Fatal("a second verdict row for one run")
	}
	if err := exec(`DELETE FROM run_verdicts`); err != nil {
		t.Fatal(err)
	}
	if err := verdict("maybe", "none"); err == nil {
		t.Fatal("verdict outcome maybe accepted")
	}
	if err := verdict("pass", "huge"); err == nil {
		t.Fatal("verdict severity huge accepted")
	}
	if err := exec(`INSERT INTO run_verdicts (run_id, project, node_id, role, outcome, severity, created_at, updated_at)
VALUES ('r1', 'p', 'n', 'executor', 'pass', 'none', 'now', 'now')`); err == nil {
		t.Fatal("verdict role executor accepted")
	}
	if err := exec(`INSERT INTO agent_session_actions (project, session_id, at, action) VALUES ('p', 's', 'now', 'dance')`); err == nil {
		t.Fatal("session action dance accepted")
	}
	if err := exec(`INSERT INTO agent_session_actions (project, session_id, at, action) VALUES ('p', 's', 'now', 'joined')`); err != nil {
		t.Fatalf("session action joined: %v", err)
	}
	if err := exec(`INSERT INTO analytics_settings (owner_id, project, exploration_pct, updated_at) VALUES ('u', '', 51, 'now')`); err == nil {
		t.Fatal("exploration_pct 51 accepted")
	}
	if err := exec(`INSERT INTO analytics_settings (owner_id, project, exploration_pct, updated_at) VALUES ('u', '', 50, 'now')`); err != nil {
		t.Fatalf("exploration_pct 50: %v", err)
	}
	var auto bool
	var tiers string
	if err := database.QueryRowContext(ctx, `SELECT auto_routing, required_tiers FROM analytics_settings WHERE owner_id = 'u'`).Scan(&auto, &tiers); err != nil {
		t.Fatal(err)
	}
	if auto || tiers != "{}" {
		t.Fatalf("settings defaults: auto %v tiers %q", auto, tiers)
	}
	if err := exec(`INSERT INTO autopilot_qa_rounds (autopilot_id, round, outcome, created_at) VALUES ('a', 1, 'maybe', 'now')`); err == nil {
		t.Fatal("qa round outcome maybe accepted")
	}
	if err := exec(`INSERT INTO run_evidence (run_id, project, node_id, kind, outcome, at) VALUES ('r1', 'p', 'n', 'ci', 'bogus', 'now')`); err == nil {
		t.Fatal("evidence outcome bogus accepted")
	}
	if err := exec(`INSERT INTO route_picks (id, owner_id, project, node_id, source, reason, created_at) VALUES ('k', 'u', 'p', 'n', 'guess', '{}', 'now')`); err == nil {
		t.Fatal("route pick source guess accepted")
	}
}
