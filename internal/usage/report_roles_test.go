package usage

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/db"
)

// seedRoleRun inserts an ended run with a role (or a legacy kind) and a
// settled cost.
func seedRoleRun(t *testing.T, database *db.DB, id, node, role, kind, agent, model string, cost float64, estimated bool, created time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, model, repo_id, status, usage,
    node_title, created_by, created_by_name, created_at, role, kind, cost_usd, cost_estimated, cost_settled)
VALUES (?, 'p', ?, 'r1', 'bea', ?, ?, 'repo', 'succeeded', '{"inputTokens":1000,"outputTokens":100}', ?, 'bea', 'Bea', ?, ?, ?, ?, ?, true)`,
		id, node, agent, model, "Title "+node, created.UTC().Format(stampLayout), role, kind, cost, estimated); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func bucketByKey(buckets []KeyBucket, key string) (KeyBucket, bool) {
	for _, b := range buckets {
		if b.Key == key {
			return b, true
		}
	}
	return KeyBucket{}, false
}

// B-05: usage by role (legacy runs by their kind, sessions, copilot), and
// the cross-run cost rollup by node.
func TestReportByRoleAndNode(t *testing.T) {
	s, database := newTestStore(t)
	ctx := context.Background()
	at := testNow.Add(-time.Hour)
	seedRoleRun(t, database, "exec1", "n1", "executor", "", "claude", "sonnet", 1.00, false, at)
	seedRoleRun(t, database, "exec2", "n1", "executor", "revise", "claude", "sonnet", 0.50, false, at)
	seedRoleRun(t, database, "rev1", "n2", "", "review", "codex", "default", 0.25, true, at)
	if err := s.RecordSession(ctx, SessionReport{Project: "p", SessionID: "claude:s1", Agent: "claude",
		Models: []ModelUsage{{Model: "claude-opus-5-5", InputTokens: 1_000_000}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO copilot_usage (conversation_id, account_id, project, provider, model, input_tokens, output_tokens, created_at)
VALUES ('c1', 'bea', 'p', 'anthropic', 'claude-sonnet-4', 1000000, 0, ?)`, at.UTC().Format(stampLayout)); err != nil {
		t.Fatalf("seed copilot usage: %v", err)
	}

	report, err := s.Report(ctx, Query{Project: "p", Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"executor": 1.5, "reviewer": 0.25, "session": 4, "copilot": 3}
	for role, cost := range want {
		b, ok := bucketByKey(report.ByRole, role)
		if !ok || !near(b.CostUSD, cost) {
			t.Errorf("byRole[%s] = %+v (found %v), want cost %v", role, b, ok, cost)
		}
	}
	if b, _ := bucketByKey(report.ByRole, "reviewer"); !near(b.EstimatedCostUSD, 0.25) || b.ReportedCostUSD != 0 {
		t.Errorf("an estimated run cost counts as estimated: %+v", b)
	}
	if b, ok := bucketByKey(report.ByAgent, "copilot"); !ok || !near(b.CostUSD, 3) {
		t.Errorf("byAgent copilot = %+v (found %v)", b, ok)
	}
	if b, ok := bucketByKey(report.ByNode, "n1"); !ok || !near(b.CostUSD, 1.5) || b.Runs != 2 {
		t.Errorf("byNode n1 = %+v (found %v), want 1.5 over 2 runs", b, ok)
	}
	if len(report.ByNode) == 0 || report.ByNode[0].Key != "n1" {
		t.Errorf("byNode not sorted by cost: %+v", report.ByNode)
	}
}

// B-06: an interactive run's session usage is counted once, on the run.
func TestReportSkipsSessionUsageOfARun(t *testing.T) {
	s, database := newTestStore(t)
	ctx := context.Background()
	seedRoleRun(t, database, "inter1", "n1", "executor", "", "claude", "sonnet", 2.00, false, testNow.Add(-time.Hour))
	if err := s.RecordSession(ctx, SessionReport{Project: "p", SessionID: "claude:run", Agent: "claude",
		Models: []ModelUsage{{Model: "claude-opus-5-5", InputTokens: 500_000}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE agent_session_usage SET run_id = 'inter1' WHERE session_id = 'claude:run'`); err != nil {
		t.Fatalf("tie the session to its run: %v", err)
	}
	report, err := s.Report(ctx, Query{Project: "p", Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if !near(report.Totals.CostUSD, 2) || report.Totals.Sessions != 0 || report.Totals.Runs != 1 {
		t.Fatalf("totals = %+v, want the run's $2 only", report.Totals)
	}
}
