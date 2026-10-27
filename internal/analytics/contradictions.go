package analytics

import (
	"context"
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
	out := map[string]bool{}
	if len(verdictRunIDs) == 0 {
		return out, nil
	}
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
		c, err := contradicted(ctx, database, v)
		if err != nil {
			return nil, err
		}
		out[v.runID] = c
	}
	return out, nil
}

func contradicted(ctx context.Context, database *db.DB, v verdictRow) (bool, error) {
	exists := func(query string, args ...any) (bool, error) {
		var n int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+` LIMIT 1) hit`, args...).Scan(&n); err != nil {
			return false, err
		}
		return n > 0, nil
	}
	switch {
	case v.role == engine.RoleReviewer && v.pipeline != "" && v.outcome == "pass":
		// C1: CI failed after the pass, before a later round's verdict.
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
		// C2: a person asked for changes in the same round.
		return exists(`SELECT 1 FROM review_votes WHERE pipeline_id = ? AND round = ? AND reviewer = ? AND verdict = ?`,
			v.pipeline, v.round, humanReviewer, runnerwire.VerdictChanges)
	case v.role == engine.RoleReviewer && v.pipeline != "" && v.outcome == "fail":
		// C3: a person approved over it, and no revise followed.
		approved, err := exists(`SELECT 1 FROM review_votes WHERE pipeline_id = ? AND round = ? AND reviewer = ? AND verdict = ?`,
			v.pipeline, v.round, humanReviewer, runnerwire.VerdictApprove)
		if err != nil || !approved {
			return false, err
		}
		revised, err := exists(`SELECT 1 FROM runs WHERE pipeline_id = ? AND kind = ? AND created_at > ?`,
			v.pipeline, runnerwire.JobRevise, v.created)
		return !revised, err
	case v.role == engine.RoleQA && v.outcome == "pass":
		// C4: a QA fail on the node within 14 days.
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
