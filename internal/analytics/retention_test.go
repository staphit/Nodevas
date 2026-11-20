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
	if n := f.count(`SELECT COUNT(*) FROM run_changes WHERE patch = '' AND patch_pruned AND patch_truncated = 0 AND run_id = 'old'`); n != 1 {
		t.Errorf("old patch not cleared and marked pruned (and only pruned, not too big)")
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

// W3: a patch retention cleared is marked pruned, apart from one too big to
// keep, so the diff view can say which it is. A too-big patch was never kept
// and is not pruned again; a recent one is neither.
func TestPruneMarksPatchesPruned(t *testing.T) {
	f := newFixture(t)
	old, recent := testNow.AddDate(0, 0, -200), testNow.AddDate(0, 0, -10)
	for _, id := range []string{"kept-old", "big-old", "new"} {
		created := old
		if id == "new" {
			created = recent
		}
		f.run(seedRun{id: id, node: "n1", role: "executor", created: created, seconds: 60})
	}
	f.exec(`INSERT INTO run_changes (run_id, files, patch, patch_truncated, created_at) VALUES
  ('kept-old', '[]', 'diff --git a b', 0, ?), ('big-old', '[]', '', 1, ?), ('new', '[]', 'diff --git c d', 0, ?)`, ts(old), ts(old), ts(recent))
	res, err := Prune(context.Background(), f.db, testNow, DefaultRetentionDays)
	if err != nil {
		t.Fatal(err)
	}
	if res.Patches != 1 {
		t.Fatalf("pruned %d patches, want 1", res.Patches)
	}
	type row struct {
		patch             string
		truncated, pruned bool
	}
	want := map[string]row{
		"kept-old": {patch: "", truncated: false, pruned: true},
		"big-old":  {patch: "", truncated: true, pruned: false},
		"new":      {patch: "diff --git c d", truncated: false, pruned: false},
	}
	for id, w := range want {
		var got row
		var truncated int
		if err := f.db.QueryRowContext(context.Background(),
			`SELECT patch, patch_truncated, patch_pruned FROM run_changes WHERE run_id = ?`, id).Scan(&got.patch, &truncated, &got.pruned); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		got.truncated = truncated != 0
		if got != w {
			t.Errorf("%s = %+v, want %+v", id, got, w)
		}
	}
}

// C2: run_touched_files and run_overlaps age out after a fixed 7-day cutoff
// even when analytics retention is turned off (days == 0).
func TestTouchedPrunedAfter7DaysEvenWithRetentionOff(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old8 := testNow.AddDate(0, 0, -8)
	recent3 := testNow.AddDate(0, 0, -3)

	// Two runs finished 8 days ago, one finished 3 days ago, and one still running.
	f.run(seedRun{id: "old-a", node: "n1", role: "executor", cost: 1, created: old8, seconds: 60})
	f.run(seedRun{id: "old-b", node: "n2", role: "executor", cost: 1, created: old8, seconds: 60})
	f.run(seedRun{id: "new-c", node: "n3", role: "executor", cost: 1, created: recent3, seconds: 60})
	f.exec(`INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, model, repo_id,
		status, created_by, created_at, started_at, finished_at)
		VALUES ('live-d', 'p', 'n4', 'r1', 'u1', 'claude', 'opus', 'repo-1',
		'running', 'u1', ?, ?, '')`, ts(old8), ts(old8))

	// Touched files on all four runs.
	f.exec(`INSERT INTO run_touched_files (run_id, path, add_only, at) VALUES
		('old-a', 'src/a.go', FALSE, ?),
		('old-a', 'src/b.go', TRUE, ?),
		('old-b', 'src/a.go', FALSE, ?),
		('new-c', 'src/a.go', FALSE, ?),
		('live-d', 'src/a.go', FALSE, ?)`, ts(old8), ts(old8), ts(old8), ts(recent3), ts(old8))

	// Overlaps:
	// - (old-a, old-b): both finished > 7d ago -> pruned
	// - (new-c, old-a): new-c finished 3d ago -> kept
	// - (live-d, old-a): live-d still running -> kept
	f.exec(`INSERT INTO run_overlaps (run_a, run_b, paths, path_count, strong_count, cross_member, first_at, updated_at) VALUES
		('old-a', 'old-b', '["src/a.go"]', 1, 1, FALSE, ?, ?),
		('new-c', 'old-a', '["src/a.go"]', 1, 1, FALSE, ?, ?),
		('live-d', 'old-a', '["src/a.go"]', 1, 1, FALSE, ?, ?)`,
		ts(old8), ts(old8), ts(recent3), ts(recent3), ts(old8), ts(old8))

	// Also seed an event on old-a to verify days == 0 still keeps regular retention tables.
	f.exec(`INSERT INTO run_events (run_id, seq, type, at, payload) VALUES ('old-a', 1, 'text', '', '{}')`)

	res, err := Prune(ctx, f.db, testNow, 0)
	if err != nil {
		t.Fatalf("Prune(0): %v", err)
	}
	if res.Touched != 3 || res.Overlaps != 1 {
		t.Fatalf("Prune(0) = %+v, want Touched=3, Overlaps=1", res)
	}
	if res.Events != 0 {
		t.Errorf("Prune(0) pruned %d events, want 0", res.Events)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_touched_files WHERE run_id IN ('old-a', 'old-b')`); n != 0 {
		t.Errorf("old touched files left: %d", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_touched_files WHERE run_id IN ('new-c', 'live-d')`); n != 2 {
		t.Errorf("recent/live touched files = %d, want 2", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_overlaps`); n != 2 {
		t.Errorf("remaining overlaps = %d, want 2", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_events WHERE run_id = 'old-a'`); n != 1 {
		t.Errorf("old run_events should remain when retention days == 0, got %d", n)
	}
}
