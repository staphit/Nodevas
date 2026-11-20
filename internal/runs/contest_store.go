package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"nodevas/internal/runnerwire"
)

// ContestRecord is a stored deliberate competition (run_contests, §C.5).
type ContestRecord struct {
	runnerwire.Contest
	OwnerID       string
	CreatedByName string
	DecidedBy     string
	Scores        *runnerwire.ContestScores
}

const contestColumns = `id, owner_id, created_by, created_by_name, project, node_id, is_global,
       title, entry_count, reviewer, state, judge_run_id, scores, winner_run_id,
       decided_by, decided_at, created_at`

func scanContest(row interface{ Scan(...any) error }) (ContestRecord, error) {
	var c ContestRecord
	var entryCount int64
	var rawReviewer, rawScores string
	if err := row.Scan(
		&c.ID, &c.OwnerID, &c.CreatedBy, &c.CreatedByName, &c.Project, &c.NodeID, &c.Global,
		&c.Title, &entryCount, &rawReviewer, &c.State, &c.JudgeRunID, &rawScores, &c.WinnerRunID,
		&c.DecidedBy, &c.DecidedAt, &c.CreatedAt,
	); err != nil {
		return ContestRecord{}, err
	}
	c.EntryCount = int(entryCount)
	if rawReviewer != "" {
		var rev runnerwire.ContestEntrySpec
		if err := json.Unmarshal([]byte(rawReviewer), &rev); err == nil {
			c.Reviewer = &rev
		}
	}
	if rawScores != "" {
		var scores runnerwire.ContestScores
		if err := json.Unmarshal([]byte(rawScores), &scores); err == nil {
			c.Scores = &scores
		}
	}
	return c, nil
}

func encodeReviewer(rev *runnerwire.ContestEntrySpec) (string, error) {
	if rev == nil {
		return "", nil
	}
	raw, err := json.Marshal(rev)
	return string(raw), err
}

func encodeContestScores(scores *runnerwire.ContestScores) (string, error) {
	if scores == nil {
		return "", nil
	}
	raw, err := json.Marshal(scores)
	return string(raw), err
}

func (s *Store) createContestWith(ctx context.Context, q querier, in ContestRecord) (ContestRecord, error) {
	id, err := newID()
	if err != nil {
		return ContestRecord{}, err
	}
	now := s.stamp()
	if in.State == "" {
		in.State = runnerwire.ContestRunning
	}
	rawReviewer, err := encodeReviewer(in.Reviewer)
	if err != nil {
		return ContestRecord{}, err
	}
	rawScores, err := encodeContestScores(in.Scores)
	if err != nil {
		return ContestRecord{}, err
	}
	_, err = q.ExecContext(ctx, `
INSERT INTO run_contests (id, owner_id, created_by, created_by_name, project, node_id, is_global,
                          title, entry_count, reviewer, state, judge_run_id, scores, winner_run_id,
                          decided_by, decided_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.OwnerID, in.CreatedBy, in.CreatedByName, in.Project, in.NodeID, in.Global,
		in.Title, int64(in.EntryCount), rawReviewer, in.State, in.JudgeRunID, rawScores, in.WinnerRunID,
		in.DecidedBy, in.DecidedAt, now)
	if err != nil {
		return ContestRecord{}, err
	}
	in.ID = id
	in.CreatedAt = now
	return in, nil
}

// GetContest returns one contest by ID.
func (s *Store) GetContest(ctx context.Context, id string) (ContestRecord, error) {
	return s.getContestWith(ctx, s.db, id)
}

func (s *Store) getContestWith(ctx context.Context, q querier, id string) (ContestRecord, error) {
	if id == "" {
		return ContestRecord{}, ErrNotFound
	}
	c, err := scanContest(q.QueryRowContext(ctx, `SELECT `+contestColumns+` FROM run_contests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ContestRecord{}, ErrNotFound
	}
	return c, err
}

// ContestRuns returns every run belonging to contestID (entries A, B, C then
// optional reviewer J), with merge and workspace-action attachments loaded.
func (s *Store) ContestRuns(ctx context.Context, contestID string) ([]Record, error) {
	if contestID == "" {
		return []Record{}, nil
	}
	out, err := s.query(ctx, `SELECT `+runColumns+` FROM runs WHERE contest_id = ?
 ORDER BY CASE contest_label WHEN 'A' THEN 1 WHEN 'B' THEN 2 WHEN 'C' THEN 3 WHEN 'J' THEN 4 ELSE 5 END,
          created_at ASC, id ASC`, contestID)
	if err != nil {
		return nil, err
	}
	refs := make([]*Record, len(out))
	for i := range out {
		refs[i] = &out[i]
	}
	if err := s.attachMerges(ctx, refs); err != nil {
		return nil, err
	}
	if err := s.attachWorkspaceActions(ctx, refs); err != nil {
		return nil, err
	}
	return out, nil
}

// markContestReady transitions contestID from running to ready if still running.
func (s *Store) markContestReady(ctx context.Context, id string) (ContestRecord, bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run_contests SET state = ? WHERE id = ? AND state = ?`,
		runnerwire.ContestReady, id, runnerwire.ContestRunning)
	if err != nil {
		return ContestRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		cur, gerr := s.GetContest(ctx, id)
		return cur, false, gerr
	}
	updated, err := s.GetContest(ctx, id)
	return updated, true, err
}

// startContestJudgingWith updates contestID to judging inside tx.
func (s *Store) startContestJudgingWith(ctx context.Context, q querier, id, fromState, judgeRunID string, reviewer *runnerwire.ContestEntrySpec) (ContestRecord, bool, error) {
	rawReviewer, err := encodeReviewer(reviewer)
	if err != nil {
		return ContestRecord{}, false, err
	}
	res, err := q.ExecContext(ctx,
		`UPDATE run_contests SET state = ?, judge_run_id = ?, reviewer = ? WHERE id = ? AND state = ? AND judge_run_id = ''`,
		runnerwire.ContestJudging, judgeRunID, rawReviewer, id, fromState)
	if err != nil {
		return ContestRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		cur, gerr := s.getContestWith(ctx, q, id)
		return cur, false, gerr
	}
	updated, err := s.getContestWith(ctx, q, id)
	return updated, true, err
}

// finishContestJudging transitions a judging contest to ready and stores parsed
// reviewer scores (or empty string when scores is nil).
func (s *Store) finishContestJudging(ctx context.Context, id, judgeRunID string, scores *runnerwire.ContestScores) (ContestRecord, bool, error) {
	rawScores, err := encodeContestScores(scores)
	if err != nil {
		return ContestRecord{}, false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE run_contests SET state = ?, scores = ? WHERE id = ? AND state = ? AND judge_run_id = ?`,
		runnerwire.ContestReady, rawScores, id, runnerwire.ContestJudging, judgeRunID)
	if err != nil {
		return ContestRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		cur, gerr := s.GetContest(ctx, id)
		return cur, false, gerr
	}
	updated, err := s.GetContest(ctx, id)
	return updated, true, err
}

// decideContest transitions a ready or judging contest to decided.
func (s *Store) decideContest(ctx context.Context, id, winnerRunID, decidedBy string) (ContestRecord, bool, error) {
	now := s.stamp()
	res, err := s.db.ExecContext(ctx,
		`UPDATE run_contests SET state = ?, winner_run_id = ?, decided_by = ?, decided_at = ? WHERE id = ? AND state IN (?, ?)`,
		runnerwire.ContestDecided, winnerRunID, decidedBy, now, id,
		runnerwire.ContestReady, runnerwire.ContestJudging)
	if err != nil {
		return ContestRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		cur, gerr := s.GetContest(ctx, id)
		return cur, false, gerr
	}
	updated, err := s.GetContest(ctx, id)
	return updated, true, err
}

// cancelContest transitions any undecided contest to cancelled.
func (s *Store) cancelContest(ctx context.Context, id string) (ContestRecord, bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE run_contests SET state = ? WHERE id = ? AND state IN (?, ?, ?)`,
		runnerwire.ContestCancelled, id,
		runnerwire.ContestRunning, runnerwire.ContestJudging, runnerwire.ContestReady)
	if err != nil {
		return ContestRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		cur, gerr := s.GetContest(ctx, id)
		return cur, false, gerr
	}
	updated, err := s.GetContest(ctx, id)
	return updated, true, err
}
