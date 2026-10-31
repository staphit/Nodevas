package analytics

import (
	"context"
	"testing"
)

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count: %v", err)
	}
	return n
}

// B-12: raw events, session actions, route picks and old patches age out;
// runs, verdicts and evidence (the aggregates) stay.
func TestPruneKeepsAggregates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old, recent := testNow.AddDate(0, 0, -200), testNow.AddDate(0, 0, -10)
	f.run(seedRun{id: "old", node: "n1", role: "executor", cost: 1, created: old, seconds: 60})
	f.run(seedRun{id: "new", node: "n2", role: "executor", cost: 1, created: recent, seconds: 60})
	for _, id := range []string{"old", "new"} {
		for seq := 1; seq <= 3; seq++ {
			f.exec(`INSERT INTO run_events (run_id, seq, type, at, payload) VALUES (?, ?, 'text', '', '{}')`, id, seq)
		}
	}
	f.verdict("old", "p", "n1", "", "reviewer", 1, "pass", "none", "", "codex", "gpt-5.5", old)
	f.evidence("old", "", "p", "n1", "ci", "pass", old)
	f.exec(`INSERT INTO agent_session_actions (project, session_id, at, action) VALUES ('p', 's1', ?, 'joined'), ('p', 's1', ?, 'left')`, ts(old), ts(recent))
	f.exec(`INSERT INTO route_picks (id, owner_id, project, node_id, source, reason, created_at) VALUES ('k1', 'u1', 'p', 'n1', 'auto', '{}', ?), ('k2', 'u1', 'p', 'n1', 'auto', '{}', ?)`, ts(old), ts(recent))
	f.exec(`INSERT INTO run_changes (run_id, files, patch, created_at) VALUES ('old', '[]', 'diff --git a b', ?), ('new', '[]', 'diff --git c d', ?)`, ts(old), ts(recent))

	if !ValidRetentionDays(0) || !ValidRetentionDays(30) || !ValidRetentionDays(3650) || ValidRetentionDays(7) || ValidRetentionDays(4000) {
		t.Error("ValidRetentionDays bounds: 0 or 30..3650")
	}

	// days == 0 keeps everything.
	if res, err := Prune(ctx, f.db, testNow, 0); err != nil || res != (PruneResult{}) {
		t.Fatalf("Prune(0) = %+v, %v", res, err)
	}
	res, err := Prune(ctx, f.db, testNow, DefaultRetentionDays)
	if err != nil {
		t.Fatal(err)
	}
	if res != (PruneResult{Events: 3, Actions: 1, Picks: 1, Patches: 1}) {
		t.Fatalf("Prune(180) = %+v, want 3 events, 1 action, 1 pick, 1 patch", res)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_events WHERE run_id = 'old'`); n != 0 {
		t.Errorf("old run's events left: %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_events WHERE run_id = 'new'`); n != 3 {
		t.Errorf("recent run's events = %d, want 3", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM runs`); n != 2 {
		t.Errorf("runs = %d, want both kept", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_verdicts`) + f.count(`SELECT COUNT(*) FROM run_evidence`); n != 2 {
		t.Errorf("verdicts + evidence = %d, want both kept", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_changes WHERE patch = '' AND patch_truncated = 1 AND run_id = 'old'`); n != 1 {
		t.Errorf("old patch not cleared")
	}
	if n := f.count(`SELECT COUNT(*) FROM run_changes WHERE patch <> '' AND run_id = 'new'`); n != 1 {
		t.Errorf("recent patch cleared")
	}
}

// Batches: more rows than one batch all go.
func TestPruneInBatches(t *testing.T) {
	f := newFixture(t)
	old := testNow.AddDate(0, 0, -400)
	f.run(seedRun{id: "old", node: "n1", role: "executor", created: old, seconds: 60})
	const rows = 12000
	f.exec(`INSERT INTO run_events (run_id, seq, type, at, payload) SELECT 'old', g, 'text', '', '{}' FROM generate_series(1, ?) AS g`, rows)
	f.exec(`INSERT INTO agent_session_actions (project, session_id, at, action) SELECT 'p', 's' || g, ?, 'doing' FROM generate_series(1, ?) AS g`, ts(old), rows)
	res, err := Prune(context.Background(), f.db, testNow, 30)
	if err != nil {
		t.Fatal(err)
	}
	if res.Events != rows || res.Actions != rows {
		t.Fatalf("pruned %+v, want %d events and %d actions", res, rows, rows)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_events`); n != 0 {
		t.Fatalf("%d events left", n)
	}
}
