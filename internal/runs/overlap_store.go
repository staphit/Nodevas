package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"nodevas/internal/runnerwire"
)

// touchedEntry is one validated touched path to upsert for a run.
type touchedEntry struct {
	path    string
	addOnly bool
}

// overlapRow is a stored row from run_overlaps.
type overlapRow struct {
	RunA        string
	RunB        string
	Paths       []string
	PathCount   int
	StrongCount int
	CrossMember bool
	MailedA     bool
	MailedB     bool
	FirstAt     string
	UpdatedAt   string
}

// sharedPath is one path touched by both runs of a pair.
type sharedPath struct {
	path   string
	strong bool
}

// UpsertTouchedFiles records validated paths into run_touched_files for runID,
// keeping the total per run within runnerwire.MaxTouchedFiles while still
// allowing existing paths to downgrade add_only from true to false (§C.3.2).
func (s *Store) UpsertTouchedFiles(ctx context.Context, runID string, entries []touchedEntry) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	existing := make(map[string]bool)
	rows, err := s.db.QueryContext(ctx, `SELECT path, add_only FROM run_touched_files WHERE run_id = ?`, runID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		var ao bool
		if err := rows.Scan(&p, &ao); err != nil {
			rows.Close()
			return nil, err
		}
		existing[p] = ao
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := s.stamp()
	count := len(existing)
	var touched []string
	for _, e := range entries {
		prevAO, seen := existing[e.path]
		if !seen {
			if count >= runnerwire.MaxTouchedFiles {
				continue
			}
			count++
		} else if prevAO == (prevAO && e.addOnly) {
			// Already stored with same or stronger add_only state; nothing to update.
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO run_touched_files (run_id, path, add_only, at)
VALUES (?, ?, ?, ?)
ON CONFLICT (run_id, path) DO UPDATE SET
  add_only = run_touched_files.add_only AND EXCLUDED.add_only,
  at = EXCLUDED.at`,
			runID, e.path, e.addOnly, now); err != nil {
			return nil, err
		}
		existing[e.path] = seen && prevAO && e.addOnly || (!seen && e.addOnly)
		touched = append(touched, e.path)
	}
	return touched, nil
}

// overlapCandidates returns every other run sharing r.RepoKey that is in scope
// for Layer 2 overlap detection (§C.3.2):
//   - own runs (same created_by or owner_id): active, or succeeded within 24h
//     with no workspace action applied (workspace = ”) and no PR (pr_url = ”);
//   - another member's runs: active node runs in the same project (only when r
//     itself is a node run).
func (s *Store) overlapCandidates(ctx context.Context, r Record, now time.Time) ([]Record, error) {
	if r.RepoKey == "" {
		return nil, nil
	}
	cutoff24h := formatStamp(now.Add(-24 * time.Hour))
	if r.Global {
		// Global runs pair only with the actor's own runs sharing repo_key.
		return s.query(ctx, `SELECT `+runColumns+` FROM runs
 WHERE repo_key = ? AND id <> ?
   AND (created_by = ? OR owner_id = ?)
   AND (
     status IN (?, ?, ?)
     OR (status = ? AND finished_at <> '' AND finished_at >= ? AND workspace = '' AND pr_url = '')
   )
 ORDER BY created_at ASC, id ASC`,
			r.RepoKey, r.ID,
			r.CreatedBy, r.OwnerID,
			runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning,
			runnerwire.RunSucceeded, cutoff24h)
	}

	// Node runs pair with own runs (node or global, active or recent kept
	// unapplied result) and with other members' active node runs in the same project.
	return s.query(ctx, `SELECT `+runColumns+` FROM runs
 WHERE repo_key = ? AND id <> ?
   AND (
     (
       (created_by = ? OR owner_id = ?)
       AND (
         status IN (?, ?, ?)
         OR (status = ? AND finished_at <> '' AND finished_at >= ? AND workspace = '' AND pr_url = '')
       )
     )
     OR (
       NOT is_global AND project = ? AND status IN (?, ?, ?)
     )
   )
 ORDER BY created_at ASC, id ASC`,
		r.RepoKey, r.ID,
		r.CreatedBy, r.OwnerID,
		runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning,
		runnerwire.RunSucceeded, cutoff24h,
		r.Project, runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning)
}

// fallbackRoot walks retry_of links up to the root run ID of a fallback chain.
func (s *Store) fallbackRoot(ctx context.Context, r Record) string {
	curID, next := r.ID, r.RetryOf
	for range 32 {
		if next == "" {
			return curID
		}
		curID = next
		var parent string
		err := s.db.QueryRowContext(ctx, `SELECT retry_of FROM runs WHERE id = ?`, curID).Scan(&parent)
		if err != nil {
			return curID
		}
		next = parent
	}
	return curID
}

// sharedTouchedPaths returns all paths present in run_touched_files for both
// runA and runB, ordered strong-first and then alphabetically (§C.3.2).
func (s *Store) sharedTouchedPaths(ctx context.Context, runA, runB string) ([]sharedPath, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.path, NOT (a.add_only AND b.add_only) AS strong
  FROM run_touched_files a
  JOIN run_touched_files b ON b.path = a.path
 WHERE a.run_id = ? AND b.run_id = ?`, runA, runB)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sharedPath
	for rows.Next() {
		var sp sharedPath
		if err := rows.Scan(&sp.path, &sp.strong); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b sharedPath) int {
		if a.strong != b.strong {
			if a.strong {
				return -1
			}
			return 1
		}
		if a.path < b.path {
			return -1
		}
		if a.path > b.path {
			return 1
		}
		return 0
	})
	return out, nil
}

// getOverlap returns the stored run_overlaps row for canonical pair (runA < runB),
// or (zero, false, nil) if none exists yet.
func (s *Store) getOverlap(ctx context.Context, runA, runB string) (overlapRow, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT run_a, run_b, paths, path_count, strong_count, cross_member, mailed_a, mailed_b, first_at, updated_at
  FROM run_overlaps
 WHERE run_a = ? AND run_b = ?`, runA, runB)
	ov, err := scanOverlap(row)
	if errors.Is(err, sql.ErrNoRows) {
		return overlapRow{}, false, nil
	}
	if err != nil {
		return overlapRow{}, false, err
	}
	return ov, true, nil
}

// upsertOverlap stores or updates a pair in run_overlaps, keeping first_at and
// mailed_a/mailed_b on conflict (§C.3.2).
func (s *Store) upsertOverlap(ctx context.Context, runA, runB string, paths []string, pathCount, strongCount int, crossMember bool) (overlapRow, error) {
	if paths == nil {
		paths = []string{}
	}
	rawPaths, err := json.Marshal(paths)
	if err != nil {
		return overlapRow{}, err
	}
	now := s.stamp()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_overlaps (run_a, run_b, paths, path_count, strong_count, cross_member, first_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (run_a, run_b) DO UPDATE SET
  paths = EXCLUDED.paths,
  path_count = EXCLUDED.path_count,
  strong_count = EXCLUDED.strong_count,
  cross_member = EXCLUDED.cross_member,
  updated_at = EXCLUDED.updated_at`,
		runA, runB, string(rawPaths), pathCount, strongCount, crossMember, now, now)
	if err != nil {
		return overlapRow{}, err
	}
	ov, _, err := s.getOverlap(ctx, runA, runB)
	return ov, err
}

// markOverlapMailed sets mailed_a or mailed_b to TRUE for pair (runA, runB).
func (s *Store) markOverlapMailed(ctx context.Context, runA, runB string, isA bool) error {
	col := "mailed_b"
	if isA {
		col = "mailed_a"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE run_overlaps SET `+col+` = TRUE WHERE run_a = ? AND run_b = ?`, runA, runB)
	return err
}

// overlapsForRun returns every run_overlaps row involving runID, ordered by
// strong_count DESC, updated_at DESC.
func (s *Store) overlapsForRun(ctx context.Context, runID string) ([]overlapRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT run_a, run_b, paths, path_count, strong_count, cross_member, mailed_a, mailed_b, first_at, updated_at
  FROM run_overlaps
 WHERE run_a = ? OR run_b = ?
 ORDER BY strong_count DESC, updated_at DESC, run_a ASC, run_b ASC`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []overlapRow
	for rows.Next() {
		ov, err := scanOverlap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ov)
	}
	return out, rows.Err()
}

func scanOverlap(row interface{ Scan(...any) error }) (overlapRow, error) {
	var ov overlapRow
	var rawPaths string
	if err := row.Scan(
		&ov.RunA, &ov.RunB, &rawPaths, &ov.PathCount, &ov.StrongCount,
		&ov.CrossMember, &ov.MailedA, &ov.MailedB, &ov.FirstAt, &ov.UpdatedAt,
	); err != nil {
		return overlapRow{}, err
	}
	if rawPaths != "" {
		_ = json.Unmarshal([]byte(rawPaths), &ov.Paths)
	}
	if ov.Paths == nil {
		ov.Paths = []string{}
	}
	return ov, nil
}
