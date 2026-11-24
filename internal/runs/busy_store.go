package runs

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"nodevas/internal/runnerwire"
)

// isWritingKind reports whether kind writes code or plans on a node (or repo).
// Read-only kinds (review, judge, verify, compare) never hold and never wait (§C.0.2).
// Plan counts only in the node check (§C.2.1).
func isWritingKind(kind string, nodeCheck bool) bool {
	switch kind {
	case runnerwire.JobImplement, runnerwire.JobTest, runnerwire.JobRevise:
		return true
	case runnerwire.JobPlan:
		return nodeCheck
	}
	return false
}

func workRootOf(r Record) string {
	if r.WorkRoot != "" {
		return r.WorkRoot
	}
	if r.Global {
		return r.ID
	}
	return ""
}

func canSeeQuery(bq BusyQuery, r Record) bool {
	return bq.IsAdmin || (bq.ActorID != "" && (r.CreatedBy == bq.ActorID || r.OwnerID == bq.ActorID))
}

func excludedByLineage(ex Lineage, r Record) bool {
	if ex.PipelineID != "" && r.PipelineID == ex.PipelineID {
		return true
	}
	if ex.GroupID != "" && r.GroupID == ex.GroupID {
		return true
	}
	if ex.ContestID != "" && r.ContestID == ex.ContestID {
		return true
	}
	if ex.WorkRoot != "" && r.Global && workRootOf(r) == ex.WorkRoot {
		return true
	}
	return false
}

func (s *Store) queryWith(ctx context.Context, q querier, query string, args ...any) ([]Record, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// busyHolders reads every unfinished writing holder matching bq through q
// (either *db.DB for preflight/Pump or *sql.Tx inside a start transaction),
// deduplicated and ordered oldest first (§C.2.1).
func (s *Store) busyHolders(ctx context.Context, q querier, bq BusyQuery) ([]Record, error) {
	seen := map[string]bool{}
	seenPipelines := map[string]bool{}
	var out []Record
	add := func(list []Record) {
		for _, r := range list {
			if seen[r.ID] || excludedByLineage(bq.Exclude, r) {
				continue
			}
			seen[r.ID] = true
			if r.PipelineID != "" {
				seenPipelines[r.PipelineID] = true
			}
			out = append(out, r)
		}
	}

	if !bq.Global {
		// Node check: unfinished writing runs on the same node, any member.
		if bq.Project != "" && bq.NodeID != "" {
			nodeRuns, err := s.queryWith(ctx, q, `SELECT `+runColumns+` FROM runs
 WHERE NOT is_global AND project = ? AND node_id = ?
   AND status IN (?, ?, ?)
   AND kind IN ('', 'test', 'revise', 'plan')
 ORDER BY created_at ASC, id ASC`,
				bq.Project, bq.NodeID,
				runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning)
			if err != nil {
				return nil, err
			}
			add(nodeRuns)

			// Running pipelines on the node between steps (no unfinished writing
			// run currently in seenPipelines) also hold the node, shown as the
			// pipeline's last run (§C.2.1).
			pRows, err := q.QueryContext(ctx, `SELECT id FROM run_pipelines
 WHERE project = ? AND node_id = ? AND status = ?
 ORDER BY created_at ASC, id ASC`,
				bq.Project, bq.NodeID, runnerwire.PipelineRunning)
			if err != nil {
				return nil, err
			}
			var pipeIDs []string
			for pRows.Next() {
				var pid string
				if err := pRows.Scan(&pid); err != nil {
					pRows.Close()
					return nil, err
				}
				if pid != "" && pid != bq.Exclude.PipelineID && !seenPipelines[pid] {
					pipeIDs = append(pipeIDs, pid)
				}
			}
			pRows.Close()
			if err := pRows.Err(); err != nil {
				return nil, err
			}
			for _, pid := range pipeIDs {
				last, err := scanRun(q.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs
 WHERE pipeline_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, pid))
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, err
				}
				add([]Record{last})
			}
		}

		// Repo check for a node start: actor's own unfinished global runs in the same repo.
		if bq.RepoKey != "" && bq.ActorID != "" {
			repoRuns, err := s.queryWith(ctx, q, `SELECT `+runColumns+` FROM runs
 WHERE is_global AND repo_key = ?
   AND status IN (?, ?, ?)
   AND kind IN ('', 'test', 'revise')
   AND (created_by = ? OR owner_id = ?)
 ORDER BY created_at ASC, id ASC`,
				bq.RepoKey,
				runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning,
				bq.ActorID, bq.ActorID)
			if err != nil {
				return nil, err
			}
			add(repoRuns)
		}
	} else {
		// Global start repo check: actor's own unfinished writing runs (global or node)
		// in the same repo. Plan counts only on a node (§C.2.1).
		if bq.RepoKey != "" && bq.ActorID != "" {
			repoRuns, err := s.queryWith(ctx, q, `SELECT `+runColumns+` FROM runs
 WHERE repo_key = ?
   AND status IN (?, ?, ?)
   AND kind IN ('', 'test', 'revise')
   AND (created_by = ? OR owner_id = ?)
 ORDER BY created_at ASC, id ASC`,
				bq.RepoKey,
				runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning,
				bq.ActorID, bq.ActorID)
			if err != nil {
				return nil, err
			}
			add(repoRuns)
		}
	}

	slices.SortStableFunc(out, func(a, b Record) int {
		if c := strings.Compare(a.CreatedAt, b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// latestTurnWith returns the latest non-withdrawn turn of a global work chain
// through q (*db.DB or *sql.Tx).
func (s *Store) latestTurnWith(ctx context.Context, q querier, workRoot string) (Record, bool, error) {
	if workRoot == "" {
		return Record{}, false, nil
	}
	cur, err := scanRun(q.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, workRoot))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	for range 500 {
		next, err := scanRun(q.QueryRowContext(ctx,
			`SELECT `+runColumns+` FROM runs WHERE continue_of = ? AND `+notWithdrawnSQL+` LIMIT 1`,
			cur.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return cur, true, nil
		}
		if err != nil {
			return Record{}, false, err
		}
		cur = next
	}
	return cur, true, nil
}

// lineageKey is a strict total-order key (created_at, root_id) identifying when
// a run's lineage began (§C.2.3).
type lineageKey struct {
	createdAt string
	rootID    string
}

func (a lineageKey) before(b lineageKey) bool {
	if a.createdAt != b.createdAt {
		return a.createdAt < b.createdAt
	}
	return a.rootID < b.rootID
}

// lineageKeyOf resolves the origin timestamp and root id of r's lineage:
// its pipeline's created_at, its work_root's created_at, the root of its
// retry_of chain, or r itself (§C.2.3).
func (s *Store) lineageKeyOf(ctx context.Context, q querier, r Record) lineageKey {
	if r.PipelineID != "" {
		var createdAt string
		if err := q.QueryRowContext(ctx, `SELECT created_at FROM run_pipelines WHERE id = ?`, r.PipelineID).Scan(&createdAt); err == nil && createdAt != "" {
			return lineageKey{createdAt: createdAt, rootID: "pipe:" + r.PipelineID}
		}
	}
	if r.Global && r.WorkRoot != "" && r.WorkRoot != r.ID {
		var createdAt string
		if err := q.QueryRowContext(ctx, `SELECT created_at FROM runs WHERE id = ?`, r.WorkRoot).Scan(&createdAt); err == nil && createdAt != "" {
			return lineageKey{createdAt: createdAt, rootID: r.WorkRoot}
		}
	}
	if r.RetryOf != "" {
		rootID, createdAt := r.ID, r.CreatedAt
		cur := r.RetryOf
		for range 100 {
			var parentID, parentRetry, parentCreated string
			err := q.QueryRowContext(ctx, `SELECT id, retry_of, created_at FROM runs WHERE id = ?`, cur).Scan(&parentID, &parentRetry, &parentCreated)
			if err != nil {
				break
			}
			rootID, createdAt = parentID, parentCreated
			if parentRetry == "" {
				break
			}
			cur = parentRetry
		}
		return lineageKey{createdAt: createdAt, rootID: rootID}
	}
	return lineageKey{createdAt: r.CreatedAt, rootID: r.ID}
}

// runningPipelinesBefore reports whether any running pipeline on (project, nodeID)
// other than excludePipelineID has a lineage older than rKey.
func (s *Store) runningPipelinesBefore(ctx context.Context, q querier, project, nodeID, excludePipelineID string, rKey lineageKey) (bool, error) {
	if project == "" || nodeID == "" {
		return false, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id, created_at FROM run_pipelines
 WHERE project = ? AND node_id = ? AND status = ?`,
		project, nodeID, runnerwire.PipelineRunning)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, createdAt string
		if err := rows.Scan(&pid, &createdAt); err != nil {
			return false, err
		}
		if pid == excludePipelineID {
			continue
		}
		if (lineageKey{createdAt: createdAt, rootID: "pipe:" + pid}).before(rKey) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// activeOtherLineageOnNode reports whether any offered or running writing run
// of another lineage currently holds (project, nodeID).
func (s *Store) activeOtherLineageOnNode(ctx context.Context, r Record) (bool, error) {
	if r.Global || r.Project == "" || r.NodeID == "" {
		return false, nil
	}
	rows, err := s.queryWith(ctx, s.db, `SELECT `+runColumns+` FROM runs
 WHERE NOT is_global AND project = ? AND node_id = ?
   AND status IN (?, ?)
   AND kind IN ('', 'test', 'revise', 'plan')`,
		r.Project, r.NodeID, runnerwire.RunOffered, runnerwire.RunRunning)
	if err != nil {
		return false, err
	}
	rKey := s.lineageKeyOf(ctx, s.db, r)
	ex := Lineage{PipelineID: r.PipelineID, ContestID: r.ContestID}
	for _, h := range rows {
		if h.ID == r.ID || excludedByLineage(ex, h) {
			continue
		}
		if hKey := s.lineageKeyOf(ctx, s.db, h); hKey.rootID == rKey.rootID {
			continue
		}
		return true, nil
	}
	return false, nil
}

// takenOver reports whether node run r, now ending, was stopped by a
// take-over (§C.2.5), from the stored runs alone so a hub restarted between
// the take-over and r's end still knows. A take-over stops every visible
// unfinished holder on its node (a hidden one refuses it), so r was one of
// them exactly when a take-over run on r's node was created after r and
// before r ended; r is ending now, so "after r" is enough. after_run names
// only the newest holder, which is why it is not the test. The take-over run
// counts whatever became of it: one withdrawn before r finished still
// stopped r.
func (s *Store) takenOver(ctx context.Context, r Record) (bool, error) {
	if r.Global || r.NodeID == "" {
		return false, nil
	}
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM runs
 WHERE NOT is_global AND project = ? AND node_id = ? AND busy_choice = ? AND created_at > ? AND id <> ?)`,
		r.Project, r.NodeID, runnerwire.OnBusyTakeOver, r.CreatedAt, r.ID).Scan(&found)
	return found, err
}

// waitingRunnersFor finds distinct runner IDs that have queued busy runs or
// queued writing pipeline/handoff runs waiting on r's node or repository, and
// reports whether any queued busy run waits specifically on r's node.
func (s *Store) waitingRunnersFor(ctx context.Context, r Record) (runnerIDs []string, waitingOnNode bool, err error) {
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			runnerIDs = append(runnerIDs, id)
		}
	}

	if !r.Global && r.Project != "" && r.NodeID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT runner_id, busy_choice FROM runs
 WHERE status = ? AND NOT is_global AND project = ? AND node_id = ?
   AND (busy_choice IN (?, ?) OR (origin IN (?, ?) AND kind IN ('', 'test', 'revise', 'plan')))`,
			runnerwire.RunQueued, r.Project, r.NodeID,
			runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver,
			runnerwire.OriginPipeline, runnerwire.OriginHandoff)
		if err != nil {
			return nil, false, err
		}
		for rows.Next() {
			var runnerID, choice string
			if err := rows.Scan(&runnerID, &choice); err != nil {
				rows.Close()
				return nil, false, err
			}
			add(runnerID)
			if choice == runnerwire.OnBusyQueue || choice == runnerwire.OnBusyTakeOver {
				waitingOnNode = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
	}

	if r.RepoKey != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT runner_id FROM runs
 WHERE status = ? AND repo_key = ? AND busy_choice IN (?, ?)`,
			runnerwire.RunQueued, r.RepoKey,
			runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver)
		if err != nil {
			return nil, waitingOnNode, err
		}
		for rows.Next() {
			var runnerID string
			if err := rows.Scan(&runnerID); err != nil {
				rows.Close()
				return nil, waitingOnNode, err
			}
			add(runnerID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, waitingOnNode, err
		}
	}

	return runnerIDs, waitingOnNode, nil
}

// queuedCoordinatedRuns returns all queued busy runs (busy_choice IN queue/takeOver)
// and queued writing pipeline/handoff runs without a pending retry_at, so the
// RunRetries tick can recover any release missed across a hub restart (§C.2.3).
func (s *Store) queuedCoordinatedRuns(ctx context.Context) ([]Record, error) {
	return s.query(ctx, `SELECT `+runColumns+` FROM runs
 WHERE status = ? AND retry_at = ''
   AND (busy_choice IN (?, ?) OR (NOT is_global AND origin IN (?, ?) AND kind IN ('', 'test', 'revise', 'plan')))
 ORDER BY created_at ASC, id ASC`,
		runnerwire.RunQueued,
		runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver,
		runnerwire.OriginPipeline, runnerwire.OriginHandoff)
}
