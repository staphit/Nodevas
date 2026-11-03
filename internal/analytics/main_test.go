package analytics

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

// testNow is the clock every analytics test runs at (fixture S1).
var testNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

const stamp = "2006-01-02T15:04:05.000000Z07:00"

func ts(t time.Time) string { return t.UTC().Format(stamp) }

// at is a time on 2026-10-27, the day before testNow.
func at(hour, minute int) time.Time { return time.Date(2026, 10, 27, hour, minute, 0, 0, time.UTC) }

// fixture writes run history straight into a fresh database.
type fixture struct {
	t  *testing.T
	db *db.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, db: testpg.New(t)}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(context.Background(), query, args...); err != nil {
		f.t.Fatalf("seed: %v\n%s", err, query)
	}
}

// seedRun is one run as the fixture writes it. Zero fields take defaults:
// project p, owner u1, node type task, status succeeded, role from kind.
type seedRun struct {
	id, project, node, nodeType, pipeline, retryOf string
	kind, role, agent, model, status, failureKind  string
	verdict, owner, createdBy                      string
	acceptance                                     int
	cost                                           float64
	estimated                                      bool
	created                                        time.Time
	seconds                                        int
	active                                         bool // queued, not ended
}

func (f *fixture) run(r seedRun) {
	f.t.Helper()
	if r.project == "" {
		r.project = "p"
	}
	if r.owner == "" {
		r.owner = "u1"
	}
	if r.createdBy == "" {
		r.createdBy = r.owner
	}
	if r.nodeType == "" {
		r.nodeType = "task"
	}
	if r.status == "" {
		r.status = "succeeded"
	}
	if r.agent == "" {
		r.agent = "claude"
	}
	if r.model == "" {
		r.model = "sonnet"
	}
	started, finished := ts(r.created), ts(r.created.Add(time.Duration(r.seconds)*time.Second))
	if r.active {
		r.status, started, finished = "queued", "", ""
	}
	f.exec(`
INSERT INTO runs (id, project, node_id, node_title, runner_id, runner_name, owner_id, agent, model, repo_id,
    status, failure_kind, verdict, kind, role, retry_of, pipeline_id, created_by, created_by_name,
    created_at, started_at, finished_at, cost_usd, cost_estimated, cost_settled, node_type, acceptance_count, usage)
VALUES (?, ?, ?, ?, 'rn1', 'desk', ?, ?, ?, 'repo', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?, '{"inputTokens":1}')`,
		r.id, r.project, r.node, "Title "+r.node, r.owner, r.agent, r.model,
		r.status, r.failureKind, r.verdict, r.kind, r.role, r.retryOf, r.pipeline, r.createdBy, r.createdBy,
		ts(r.created), started, finished, r.cost, r.estimated, r.nodeType, r.acceptance)
}

func (f *fixture) pipeline(id, project, node, status string, round int, created time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO run_pipelines (id, owner_id, created_by, project, node_id, pipeline, round, status, created_at, updated_at)
VALUES (?, 'u1', 'u1', ?, ?, '{}', ?, ?, ?, ?)`, id, project, node, round, status, ts(created), ts(created))
}

func (f *fixture) verdict(runID, project, node, pipeline, role string, round int, outcome, severity, reasons, agent, model string, created time.Time) {
	f.t.Helper()
	if reasons == "" {
		reasons = "[]"
	}
	f.exec(`INSERT INTO run_verdicts (run_id, project, node_id, pipeline_id, role, round, agent, model, outcome, severity, reasons, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'mcp', ?, ?)`, runID, project, node, pipeline, role, round, agent, model, outcome, severity, reasons, ts(created), ts(created))
}

func (f *fixture) evidence(runID, pipeline, project, node, kind, outcome string, when time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO run_evidence (run_id, pipeline_id, project, node_id, kind, outcome, summary, at) VALUES (?, ?, ?, ?, ?, ?, '', ?)`,
		runID, pipeline, project, node, kind, outcome, ts(when))
}

func (f *fixture) vote(pipeline string, round int, reviewer, verdict string, when time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO review_votes (pipeline_id, round, reviewer, verdict, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		pipeline, round, reviewer, verdict, ts(when), ts(when))
}

// fixtureS1 is the spec's fixture S1 (owner u1, project p, task nodes).
func fixtureS1(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	// Pipeline pA: approved first time.
	f.pipeline("pA", "p", "nA", "approved", 1, at(9, 0))
	f.run(seedRun{id: "a1", node: "nA", pipeline: "pA", kind: "", role: "executor", acceptance: 3, cost: 1.00, created: at(9, 0), seconds: 600})
	f.run(seedRun{id: "r1", node: "nA", pipeline: "pA", kind: "review", role: "reviewer", agent: "codex", model: "gpt-5.5",
		verdict: "approve", acceptance: 3, cost: 0.20, created: at(9, 11), seconds: 240})
	f.verdict("r1", "p", "nA", "pA", "reviewer", 1, "pass", "none", "", "codex", "gpt-5.5", at(9, 15))
	// Pipeline pB: one round of changes.
	f.pipeline("pB", "p", "nB", "approved", 2, at(10, 0))
	f.run(seedRun{id: "b1", node: "nB", pipeline: "pB", role: "executor", acceptance: 5, cost: 1.00, created: at(10, 0), seconds: 900})
	f.run(seedRun{id: "r2", node: "nB", pipeline: "pB", kind: "review", role: "reviewer", agent: "codex", model: "gpt-5.5",
		verdict: "changes", acceptance: 5, cost: 0.20, created: at(10, 16), seconds: 240})
	f.verdict("r2", "p", "nB", "pB", "reviewer", 1, "fail", "medium", `["tests_weak"]`, "codex", "gpt-5.5", at(10, 20))
	f.run(seedRun{id: "b2", node: "nB", pipeline: "pB", kind: "revise", role: "executor", acceptance: 5, cost: 0.50, created: at(10, 21), seconds: 300})
	f.run(seedRun{id: "r3", node: "nB", pipeline: "pB", kind: "review", role: "reviewer", agent: "codex", model: "gpt-5.5",
		verdict: "approve", acceptance: 5, cost: 0.20, created: at(10, 27), seconds: 180})
	f.verdict("r3", "p", "nB", "pB", "reviewer", 2, "pass", "none", "", "codex", "gpt-5.5", at(10, 30))
	f.evidence("b2", "pB", "p", "nB", "ci", "fail", at(10, 40))
	// Lineage C: a rate limit, then a task failure.
	f.run(seedRun{id: "c1", node: "nC", role: "executor", status: "failed", failureKind: "rate_limit", cost: 0.10, created: at(11, 0), seconds: 60})
	f.run(seedRun{id: "c2", node: "nC", role: "executor", retryOf: "c1", status: "failed", failureKind: "task", cost: 0.40, created: at(11, 2), seconds: 60})
	return f
}

// round4 is a rate as the reports round it.
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func wantRate(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = null, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

func wantNull(t *testing.T, name string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want null", name, *got)
	}
}

func wantCost(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func findRow(rows []ScorecardRow, role, track, agent, model string) (ScorecardRow, bool) {
	for _, r := range rows {
		if r.Role == role && r.Track == track && r.Agent == agent && r.Model == model {
			return r, true
		}
	}
	return ScorecardRow{}, false
}
