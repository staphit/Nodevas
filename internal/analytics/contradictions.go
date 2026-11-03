package analytics

import (
	"context"
	"database/sql"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// humanReviewer is review_votes.reviewer for a person's vote
// (runs.HumanReviewerKey).
const humanReviewer = "human"

// qaContradictionWindow is how long after a QA pass a QA fail on the same
// node contradicts it.
const qaContradictionWindow = 14 * 24 * time.Hour

// Contradicted reports, for each verdict run id given, whether later
// evidence contradicts its verdict (rules C1 to C4):
//
//	C1 a reviewer pass, then CI fails in its pipeline before a later round's
//	   verdict;
//	C2 a reviewer pass, and a person asked for changes in its round;
//	C3 a reviewer fail, a person approved in its round, and no revise run
//	   followed it;
//	C4 a QA pass, then a QA fail on the same node within 14 days.
//
// A QA fail is never contradicted.
func Contradicted(ctx context.Context, database *db.DB, verdictRunIDs []string) (map[string]bool, error) {
	return contradictedOn(ctx, database, verdictRunIDs)
}

// querier is the part of *db.DB Contradicted reads with, so a test can
// count what it asks.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// contradictedSQL decides every rule for every verdict in one statement.
// Each rule is an EXISTS on the tables the rule reads; C4's 14-day bound is
// checked in Go on the earliest later QA fail, because the stamps are text
// and an unparseable one must answer "not contradicted", not fail the query.
const contradictedSQL = `
SELECT v.run_id, v.role, v.pipeline_id, v.outcome, v.created_at,
       CASE WHEN v.role = ? AND v.pipeline_id <> '' AND v.outcome = 'pass' THEN
            EXISTS (SELECT 1 FROM run_evidence e
                     WHERE e.pipeline_id = v.pipeline_id AND e.kind = ? AND e.outcome = 'fail' AND e.at > v.created_at
                       AND NOT EXISTS (SELECT 1 FROM run_verdicts n
                                        WHERE n.pipeline_id = v.pipeline_id AND n.round > v.round
                                          AND n.created_at > v.created_at AND n.created_at <= e.at))
         OR EXISTS (SELECT 1 FROM review_votes h
                     WHERE h.pipeline_id = v.pipeline_id AND h.round = v.round AND h.reviewer = ? AND h.verdict = ?)
       ELSE false END AS c1c2,
       CASE WHEN v.role = ? AND v.pipeline_id <> '' AND v.outcome = 'fail' THEN
            EXISTS (SELECT 1 FROM review_votes h
                     WHERE h.pipeline_id = v.pipeline_id AND h.round = v.round AND h.reviewer = ? AND h.verdict = ?)
        AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.pipeline_id = v.pipeline_id AND r.kind = ? AND r.created_at > v.created_at)
       ELSE false END AS c3,
       CASE WHEN v.role = ? AND v.outcome = 'pass' THEN
            (SELECT MIN(q.created_at) FROM run_verdicts q
              WHERE q.project = v.project AND q.node_id = v.node_id AND q.role = ? AND q.outcome = 'fail'
                AND q.created_at > v.created_at AND q.run_id <> v.run_id)
       END AS qa_fail
  FROM run_verdicts v
 WHERE v.run_id = ANY(?)`

func contradictedOn(ctx context.Context, database querier, verdictRunIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(verdictRunIDs) == 0 {
		return out, nil
	}
	rows, err := database.QueryContext(ctx, contradictedSQL,
		engine.RoleReviewer, runnerwire.PhaseCI, humanReviewer, runnerwire.VerdictChanges,
		engine.RoleReviewer, humanReviewer, runnerwire.VerdictApprove, runnerwire.JobRevise,
		engine.RoleQA, engine.RoleQA,
		verdictRunIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, role, pipeline, outcome, created string
		var c1c2, c3 bool
		var qaFail sql.NullString
		if err := rows.Scan(&id, &role, &pipeline, &outcome, &created, &c1c2, &c3, &qaFail); err != nil {
			return nil, err
		}
		c := c1c2 || c3
		if role == engine.RoleQA && outcome == "pass" && qaFail.Valid {
			// C4: the earliest later QA fail falls within 14 days.
			if at, ok := parseStamp(created); ok {
				c = qaFail.String <= formatStamp(at.Add(qaContradictionWindow))
			}
		}
		out[id] = c
	}
	return out, rows.Err()
}
