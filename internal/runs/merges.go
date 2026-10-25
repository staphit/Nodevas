package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Remote merge approval (W4). Someone approves merging a run's PR; the hub
// records the approval and hands it to the run's runner, which merges with
// its owner's own gh login. The hub never merges anything.

// ErrPrecondition is a request the run's current state does not allow, such
// as approving a merge while CI is red. Its message is meant for people.
var ErrPrecondition = errors.New("precondition failed")

type preconditionError struct{ msg string }

func (e *preconditionError) Error() string        { return e.msg }
func (e *preconditionError) Is(target error) bool { return target == ErrPrecondition }

// Precondition is an ErrPrecondition whose message is msg alone.
func Precondition(msg string) error { return &preconditionError{msg: msg} }

// Refusals of a merge approval, as the API returns them.
const (
	MsgNoOpenPR      = "no open PR"
	MsgCINotGreen    = "CI is not green"
	MsgHeadMoved     = "the PR changed since you looked; reload"
	MsgNoHead        = "the runner has not reported the PR's head commit; update nodevas on it"
	MsgMergePending  = "a merge for this run is already waiting for its runner"
	ReasonMergeStale = "the runner did not answer in time"
)

// MergeTTL is runnerwire.MergeRequestTTLSeconds as a duration.
const MergeTTL = time.Duration(runnerwire.MergeRequestTTLSeconds) * time.Second

const mergeColumns = `id, run_id, head_sha, status, reason, requested_by, requested_by_name, created_at, finished_at`

func scanMerge(row interface{ Scan(...any) error }) (runnerwire.MergeRequest, error) {
	var m runnerwire.MergeRequest
	var byID, byName string
	if err := row.Scan(&m.ID, &m.RunID, &m.HeadSHA, &m.Status, &m.Reason, &byID, &byName,
		&m.CreatedAt, &m.FinishedAt); err != nil {
		return runnerwire.MergeRequest{}, err
	}
	// The API shows who approved by name; the ID stays in the table.
	m.RequestedBy = byName
	if m.RequestedBy == "" {
		m.RequestedBy = byID
	}
	return m, nil
}

// SetClock replaces the store's clock. For tests: merge expiry and budget
// days are measured against it.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now()
}

// formatStamp writes t the way stamp does, so the two compare as text.
func formatStamp(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format(stampLayout)
}

// stale reports whether a pending merge has outlived MergeTTL.
func (s *Store) stale(m runnerwire.MergeRequest) bool {
	return m.Status == runnerwire.MergePending && m.CreatedAt < formatStamp(s.clock().Add(-MergeTTL))
}

// attachMerges sets each run's latest merge request. A pending one past its
// TTL is expired on the way: whoever reads it first retires it.
func (s *Store) attachMerges(ctx context.Context, records []*Record) error {
	if len(records) == 0 {
		return nil
	}
	byRun := make(map[string]*Record, len(records))
	args := make([]any, 0, len(records))
	for _, r := range records {
		byRun[r.ID] = r
		args = append(args, r.ID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+mergeColumns+` FROM run_merges WHERE run_id IN (?`+
		strings.Repeat(", ?", len(args)-1)+`) ORDER BY created_at, id`, args...)
	if err != nil {
		return err
	}
	latest := map[string]runnerwire.MergeRequest{}
	for rows.Next() {
		m, err := scanMerge(rows)
		if err != nil {
			rows.Close()
			return err
		}
		latest[m.RunID] = m
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for runID, m := range latest {
		if s.stale(m) {
			if expired, err := s.expireMerge(ctx, m); err == nil {
				m = expired
			}
		}
		merge := m
		byRun[runID].Merge = &merge
	}
	return nil
}

// expireMerge marks one pending merge expired. If it was answered meanwhile,
// the stored row is returned as it now stands.
func (s *Store) expireMerge(ctx context.Context, m runnerwire.MergeRequest) (runnerwire.MergeRequest, error) {
	now := s.stamp()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE run_merges SET status = ?, reason = ?, finished_at = ? WHERE id = ? AND status = ?`,
		runnerwire.MergeExpired, ReasonMergeStale, now, m.ID, runnerwire.MergePending); err != nil {
		return m, err
	}
	return s.merge(ctx, m.ID)
}

func (s *Store) merge(ctx context.Context, id string) (runnerwire.MergeRequest, error) {
	m, err := scanMerge(s.db.QueryRowContext(ctx, `SELECT `+mergeColumns+` FROM run_merges WHERE id = ?`, id))
	if err != nil {
		return runnerwire.MergeRequest{}, ErrNotFound
	}
	return m, nil
}

// ExpireMerges retires every pending merge older than MergeTTL and returns
// the IDs of the runs they belonged to.
func (s *Store) ExpireMerges(ctx context.Context) ([]string, error) {
	cutoff := formatStamp(s.clock().Add(-MergeTTL))
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mergeColumns+` FROM run_merges WHERE status = ? AND created_at < ? ORDER BY created_at`,
		runnerwire.MergePending, cutoff)
	if err != nil {
		return nil, err
	}
	var stale []runnerwire.MergeRequest
	for rows.Next() {
		m, err := scanMerge(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		stale = append(stale, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var runIDs []string
	for _, m := range stale {
		expired, err := s.expireMerge(ctx, m)
		if err != nil {
			return runIDs, err
		}
		if expired.Status == runnerwire.MergeExpired {
			runIDs = append(runIDs, m.RunID)
		}
	}
	return runIDs, nil
}

// CreateMerge records an approval to merge a run's PR at headSHA. At most one
// approval waits per run; a second is refused with MsgMergePending.
func (s *Store) CreateMerge(ctx context.Context, runID, headSHA, byID, byName string) (runnerwire.MergeRequest, error) {
	if runID == "" || headSHA == "" || byID == "" {
		return runnerwire.MergeRequest{}, fmt.Errorf("%w: missing field", ErrInvalid)
	}
	// A stale approval must not block a fresh one.
	if _, err := s.ExpireMerges(ctx); err != nil {
		return runnerwire.MergeRequest{}, err
	}
	id, err := newID()
	if err != nil {
		return runnerwire.MergeRequest{}, err
	}
	// The partial unique index on pending rows makes the check and the
	// insert one step, on both engines.
	result, err := s.db.ExecContext(ctx, `
INSERT INTO run_merges (id, run_id, head_sha, status, requested_by, requested_by_name, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`,
		id, runID, headSHA, runnerwire.MergePending, byID, Truncate(byName, maxShortField), s.stamp())
	if err != nil {
		return runnerwire.MergeRequest{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return runnerwire.MergeRequest{}, err
	} else if n == 0 {
		return runnerwire.MergeRequest{}, Precondition(MsgMergePending)
	}
	return s.merge(ctx, id)
}

// PendingMerges returns the jobs for a runner's pending merges, oldest first.
func (s *Store) PendingMerges(ctx context.Context, runnerID string) ([]runnerwire.MergeJob, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT m.id, m.run_id, m.head_sha FROM run_merges m JOIN runs r ON r.id = m.run_id
WHERE r.runner_id = ? AND m.status = ? ORDER BY m.created_at, m.id`, runnerID, runnerwire.MergePending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runnerwire.MergeJob
	for rows.Next() {
		var job runnerwire.MergeJob
		if err := rows.Scan(&job.ID, &job.RunID, &job.HeadSHA); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// FinishMerge records a runner's answer to a merge job: merged when
// outcome.Error is "", failed otherwise. The merge must be for one of the
// runner's runs and still waiting (or expired: a runner that got the job
// before it expired and merged anyway has merged, and the record says so).
func (s *Store) FinishMerge(ctx context.Context, runnerID string, outcome runnerwire.MergeOutcome) (Record, runnerwire.MergeRequest, error) {
	m, err := s.merge(ctx, outcome.ID)
	if err != nil || m.RunID != outcome.RunID {
		return Record{}, runnerwire.MergeRequest{}, ErrNotFound
	}
	r, err := s.Get(ctx, m.RunID)
	if err != nil {
		return Record{}, runnerwire.MergeRequest{}, err
	}
	if r.RunnerID != runnerID {
		return Record{}, runnerwire.MergeRequest{}, ErrNotFound
	}
	status, reason := runnerwire.MergeDone, ""
	if outcome.Error != "" {
		status = runnerwire.MergeFailed
		reason = strings.ReplaceAll(Truncate(outcome.Error, MaxErrorBytes), "\x00", "")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE run_merges SET status = ?, reason = ?, finished_at = ?
WHERE id = ? AND status IN (?, ?)`,
		status, reason, s.stamp(), m.ID, runnerwire.MergePending, runnerwire.MergeExpired)
	if err != nil {
		return Record{}, runnerwire.MergeRequest{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return Record{}, runnerwire.MergeRequest{}, err
	} else if n == 0 {
		return r, m, fmt.Errorf("%w: merge %s is already %s", ErrTransition, m.ID, m.Status)
	}
	r, err = s.Get(ctx, m.RunID)
	if err != nil {
		return Record{}, runnerwire.MergeRequest{}, err
	}
	m, err = s.merge(ctx, m.ID)
	return r, m, err
}

// CheckMergeable says why a run's PR cannot be approved for merging at
// headSHA, or returns nil.
func CheckMergeable(r Record, headSHA string) error {
	switch {
	case r.PRURL == "" || r.PRState != runnerwire.PROpen:
		return Precondition(MsgNoOpenPR)
	case r.CIStatus != runnerwire.CISuccess:
		return Precondition(MsgCINotGreen)
	case r.PRHeadSHA == "":
		return Precondition(MsgNoHead)
	case !strings.EqualFold(headSHA, r.PRHeadSHA):
		return Precondition(MsgHeadMoved)
	}
	return nil
}
