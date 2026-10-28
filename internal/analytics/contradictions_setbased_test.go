package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// countingQuerier counts the statements sent through it.
type countingQuerier struct {
	inner querier
	n     atomic.Int64
}

func (c *countingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.inner.QueryContext(ctx, query, args...)
}

func (c *countingQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.n.Add(1)
	return c.inner.QueryRowContext(ctx, query, args...)
}

// seedContradictions writes a mixed history: reviewer passes and fails in
// pipelines with CI evidence, human votes and revises in every order, and
// QA passes and fails on shared nodes at every distance around 14 days. It
// returns every verdict run id.
func seedContradictions(f *fixture, seed int64, pipelines int) []string {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	var ids []string
	minute := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	for p := range pipelines {
		pid := fmt.Sprintf("P%03d", p)
		node := fmt.Sprintf("n%03d", p)
		start := p * 300
		f.pipeline(pid, "p", node, "approved", 3, minute(start))
		for round := 1; round <= 1+rng.Intn(3); round++ {
			at := start + round*60
			id := fmt.Sprintf("rv%03d_%d", p, round)
			outcome := []string{"pass", "fail"}[rng.Intn(2)]
			f.run(seedRun{id: id, node: node, pipeline: pid, kind: "review", role: "reviewer", created: minute(at), seconds: 60})
			f.verdict(id, "p", node, pid, "reviewer", round, outcome, "low", "", "codex", "gpt-5.5", minute(at+1))
			ids = append(ids, id)
			if rng.Intn(2) == 0 {
				ci := []string{"fail", "pass"}[rng.Intn(2)]
				f.evidence(id, pid, "p", node, runnerwire.PhaseCI, ci, minute(at+rng.Intn(120)-20))
			}
			if rng.Intn(2) == 0 {
				vote := []string{runnerwire.VerdictApprove, runnerwire.VerdictChanges}[rng.Intn(2)]
				f.vote(pid, round, humanReviewer, vote, minute(at+10))
			}
			if rng.Intn(3) == 0 {
				f.vote(pid, round, "codex", runnerwire.VerdictChanges, minute(at+11))
			}
			if rng.Intn(2) == 0 {
				f.run(seedRun{id: fmt.Sprintf("fx%03d_%d", p, round), node: node, pipeline: pid, kind: runnerwire.JobRevise,
					role: "executor", created: minute(at + rng.Intn(40) - 20), seconds: 60})
			}
		}
		// A reviewer verdict with no pipeline is never contradicted.
		if rng.Intn(4) == 0 {
			id := fmt.Sprintf("lone%03d", p)
			f.run(seedRun{id: id, node: node, kind: "review", role: "reviewer", created: minute(start + 5), seconds: 60})
			f.verdict(id, "p", node, "", "reviewer", 1, "pass", "none", "", "codex", "gpt-5.5", minute(start+6))
			ids = append(ids, id)
		}
	}
	for g := range pipelines / 2 {
		node := fmt.Sprintf("goal%03d", g)
		day := base.AddDate(0, 0, g%7)
		for k := range 1 + rng.Intn(3) {
			id := fmt.Sprintf("qa%03d_%d", g, k)
			when := day.Add(time.Duration(rng.Intn(20*24)) * time.Hour)
			if rng.Intn(5) == 0 {
				when = day.Add(qaContradictionWindow) // exactly on the bound
			}
			outcome := []string{"pass", "fail"}[rng.Intn(2)]
			f.run(seedRun{id: id, node: node, nodeType: "goal", kind: "judge", role: engine.RoleQA, created: when, seconds: 60})
			f.verdict(id, "p", node, "", engine.RoleQA, k+1, outcome, "medium", "", "claude", "sonnet", when)
			ids = append(ids, id)
		}
	}
	return ids
}

// Contradicted asks one question of the database however many verdicts it
// is given; it was 1 to 3 per verdict, which over a 365-day window kept the
// read pool busy with round trips.
func TestContradictedIsOneQuery(t *testing.T) {
	f := newFixture(t)
	ids := seedContradictions(f, 1, 40)
	counter := &countingQuerier{inner: f.db}
	if _, err := contradictedOn(context.Background(), counter, ids); err != nil {
		t.Fatal(err)
	}
	if n := counter.n.Load(); n != 1 {
		t.Fatalf("%d verdicts took %d queries, want 1", len(ids), n)
	}

	// Past the old 500-id chunking too, with ids that have no verdict.
	many := append([]string(nil), ids...)
	for i := range 1200 {
		many = append(many, fmt.Sprintf("missing%04d", i))
	}
	counter.n.Store(0)
	if _, err := contradictedOn(context.Background(), counter, many); err != nil {
		t.Fatal(err)
	}
	if n := counter.n.Load(); n != 1 {
		t.Fatalf("%d ids took %d queries, want 1", len(many), n)
	}
}

// The set-based query answers exactly as the rule-by-rule checks did.
func TestContradictedMatchesTheRuleByRuleChecks(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		f := newFixture(t)
		ids := seedContradictions(f, seed, 60)
		want, err := contradictedRuleByRule(context.Background(), f.db, ids)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Contradicted(context.Background(), f.db, ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d answers, want %d", seed, len(got), len(want))
		}
		hits := 0
		for id, w := range want {
			if g, ok := got[id]; !ok || g != w {
				t.Errorf("seed %d: contradicted[%s] = %v (present %v), want %v", seed, id, g, ok, w)
			}
			if w {
				hits++
			}
		}
		if hits == 0 || hits == len(want) {
			t.Fatalf("seed %d: %d of %d contradicted; the dataset does not tell the rules apart", seed, hits, len(want))
		}
	}
}

// contradictedRuleByRule is the implementation Contradicted replaced, one
// verdict and one rule at a time, kept as the reference it must match.
func contradictedRuleByRule(ctx context.Context, database querier, verdictRunIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	var verdicts []verdictRow
	for start := 0; start < len(verdictRunIDs); start += 500 {
		chunk := verdictRunIDs[start:min(start+500, len(verdictRunIDs))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := database.QueryContext(ctx, `
SELECT run_id, project, node_id, pipeline_id, role, round, outcome, created_at
  FROM run_verdicts WHERE run_id IN (`+marks(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v verdictRow
			var round int64
			if err := rows.Scan(&v.runID, &v.project, &v.node, &v.pipeline, &v.role, &round, &v.outcome, &v.created); err != nil {
				rows.Close()
				return nil, err
			}
			v.round = int(round)
			verdicts = append(verdicts, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, v := range verdicts {
		c, err := contradictedOneByOne(ctx, database, v)
		if err != nil {
			return nil, err
		}
		out[v.runID] = c
	}
	return out, nil
}

func contradictedOneByOne(ctx context.Context, database querier, v verdictRow) (bool, error) {
	exists := func(query string, args ...any) (bool, error) {
		var n int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+` LIMIT 1) hit`, args...).Scan(&n); err != nil {
			return false, err
		}
		return n > 0, nil
	}
	switch {
	case v.role == engine.RoleReviewer && v.pipeline != "" && v.outcome == "pass":
		next := ""
		if err := database.QueryRowContext(ctx, `
SELECT COALESCE(MIN(created_at), '') FROM run_verdicts WHERE pipeline_id = ? AND round > ? AND created_at > ?`,
			v.pipeline, v.round, v.created).Scan(&next); err != nil {
			return false, err
		}
		query := `SELECT 1 FROM run_evidence WHERE pipeline_id = ? AND kind = ? AND outcome = 'fail' AND at > ?`
		args := []any{v.pipeline, runnerwire.PhaseCI, v.created}
		if next != "" {
			query += ` AND at < ?`
			args = append(args, next)
		}
		if hit, err := exists(query, args...); err != nil || hit {
			return hit, err
		}
		return exists(`SELECT 1 FROM review_votes WHERE pipeline_id = ? AND round = ? AND reviewer = ? AND verdict = ?`,
			v.pipeline, v.round, humanReviewer, runnerwire.VerdictChanges)
	case v.role == engine.RoleReviewer && v.pipeline != "" && v.outcome == "fail":
		approved, err := exists(`SELECT 1 FROM review_votes WHERE pipeline_id = ? AND round = ? AND reviewer = ? AND verdict = ?`,
			v.pipeline, v.round, humanReviewer, runnerwire.VerdictApprove)
		if err != nil || !approved {
			return false, err
		}
		revised, err := exists(`SELECT 1 FROM runs WHERE pipeline_id = ? AND kind = ? AND created_at > ?`,
			v.pipeline, runnerwire.JobRevise, v.created)
		return !revised, err
	case v.role == engine.RoleQA && v.outcome == "pass":
		created, ok := parseStamp(v.created)
		if !ok {
			return false, nil
		}
		return exists(`SELECT 1 FROM run_verdicts WHERE project = ? AND node_id = ? AND role = ? AND outcome = 'fail'
   AND created_at > ? AND created_at <= ? AND run_id <> ?`,
			v.project, v.node, engine.RoleQA, v.created, formatStamp(created.Add(qaContradictionWindow)), v.runID)
	}
	return false, nil
}
