package autopilot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

var (
	// ErrNotFound is an autopilot run that does not exist.
	ErrNotFound = errors.New("autopilot not found")
	// ErrInvalid wraps a request the caller got wrong.
	ErrInvalid = errors.New("invalid autopilot request")
	// ErrConflict is a goal that already has a live autopilot, or an action
	// its state does not allow.
	ErrConflict = errors.New("autopilot conflict")
	// ErrNodeType is a goal whose node type agents may not work (a Review,
	// a Decision, a Note…).
	ErrNodeType = errors.New("node type forbids agents")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func conflictf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
}

// Record is a stored autopilot run: the API's view plus what only the hub
// needs.
type Record struct {
	runnerwire.AutopilotRun
	SteppedUpAt string
	// Workflow is the goal's workflow stages, frozen when the autopilot
	// started (nil: legacy, the policy's review mode decides); PlanRunID the
	// planner run that breaks the goal down (goal phase plan).
	Workflow  *engine.Workflow
	PlanRunID string
}

// stagesColumn is autopilot_runs.stages: the frozen workflow and the
// planner run.
type stagesColumn struct {
	Workflow  *engine.Workflow `json:"workflow"`
	PlanRunID string           `json:"planRunId,omitempty"`
}

func encodeStages(r Record) (string, error) {
	if r.Workflow == nil {
		return "", nil
	}
	raw, err := json.Marshal(stagesColumn{Workflow: r.Workflow, PlanRunID: r.PlanRunID})
	return string(raw), err
}

// Store keeps autopilot runs and their task instances.
type Store struct {
	db  *db.DB
	now func() time.Time

	mu   sync.Mutex
	last time.Time
}

// NewStore wraps the workspace database.
func NewStore(database *db.DB) *Store { return &Store{db: database, now: time.Now} }

// stampLayout matches the run store's fixed-width times, so stage times from
// runs and from here sort as text.
const stampLayout = "2006-01-02T15:04:05.000000Z07:00"

// stamp is a strictly increasing UTC time, as the run store's.
func (s *Store) stamp() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now().UTC()
	if !t.After(s.last) {
		t = s.last.Add(time.Microsecond)
	}
	s.last = t
	return t.Format(stampLayout)
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

const runColumns = `id, project, goal_node_id, status, policy, reason, created_by, created_by_name,
	stepped_up_at, created_at, updated_at, finished_at, stages, goal_phase, qa_round, qa_run_id`

func scanRun(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var policy, stages string
	var qaRound int64
	err := row.Scan(&r.ID, &r.Project, &r.GoalNodeID, &r.Status, &policy, &r.Reason, &r.CreatedBy,
		&r.CreatedByName, &r.SteppedUpAt, &r.CreatedAt, &r.UpdatedAt, &r.FinishedAt,
		&stages, &r.GoalPhase, &qaRound, &r.QARunID)
	if err != nil {
		return r, err
	}
	r.QARound = int(qaRound)
	if policy != "" {
		_ = json.Unmarshal([]byte(policy), &r.Policy)
	}
	if stages != "" {
		var column stagesColumn
		if json.Unmarshal([]byte(stages), &column) == nil && column.Workflow != nil {
			r.Workflow, r.PlanRunID = column.Workflow, column.PlanRunID
			r.Stages = runs.StagesSnapshot(column.Workflow)
		}
	}
	return r, nil
}

// Create records a running autopilot for a goal with its first task
// instances. A goal that already has a running or paused one is a conflict.
func (s *Store) Create(ctx context.Context, r Record, tasks []runnerwire.TaskInstance) (Record, error) {
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	policy, err := json.Marshal(r.Policy)
	if err != nil {
		return Record{}, err
	}
	stages, err := encodeStages(r)
	if err != nil {
		return Record{}, err
	}
	r.Stages = runs.StagesSnapshot(r.Workflow)
	now := s.stamp()
	r.ID, r.Status, r.CreatedAt, r.UpdatedAt, r.FinishedAt = id, runnerwire.AutopilotRunning, now, now, ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM autopilot_runs
		WHERE project = ? AND goal_node_id = ? AND status IN ('running', 'paused')`, r.Project, r.GoalNodeID).Scan(&live); err != nil {
		return Record{}, err
	}
	if live > 0 {
		return Record{}, conflictf("this goal already has an autopilot running; pause or cancel it first")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO autopilot_runs (`+runColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Project, r.GoalNodeID, r.Status, string(policy), r.Reason, r.CreatedBy, r.CreatedByName,
		r.SteppedUpAt, r.CreatedAt, r.UpdatedAt, r.FinishedAt,
		stages, r.GoalPhase, int64(r.QARound), r.QARunID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Record{}, conflictf("this goal already has an autopilot running; pause or cancel it first")
		}
		return Record{}, err
	}
	for _, t := range tasks {
		if err := putTask(ctx, tx, r.ID, t, now); err != nil {
			return Record{}, err
		}
	}
	return r, tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func putTask(ctx context.Context, x execer, autopilotID string, t runnerwire.TaskInstance, now string) error {
	if t.UpdatedAt == "" {
		t.UpdatedAt = now
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `INSERT INTO autopilot_tasks (autopilot_id, node_id, state, run_id, pipeline_id, data, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (autopilot_id, node_id) DO UPDATE SET state = excluded.state, run_id = excluded.run_id,
			pipeline_id = excluded.pipeline_id, data = excluded.data, updated_at = excluded.updated_at`,
		autopilotID, t.NodeID, t.State, t.RunID, t.PipelineID, string(data), t.UpdatedAt)
	return err
}

// Get returns one autopilot run.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM autopilot_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return r, err
}

// Latest returns a goal's newest autopilot run.
func (s *Store) Latest(ctx context.Context, project, goalNodeID string) (Record, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM autopilot_runs
		WHERE project = ? AND goal_node_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, project, goalNodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return r, err
}

// Live returns the ids of every running or paused autopilot, for a restart.
func (s *Store) Live(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM autopilot_runs WHERE status IN ('running', 'paused') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// InProject returns the ids of a project's autopilot runs in the given
// statuses, oldest first.
func (s *Store) InProject(ctx context.Context, project string, statuses ...string) ([]string, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query := `SELECT id FROM autopilot_runs WHERE project = ? AND status IN (?` +
		strings.Repeat(", ?", len(statuses)-1) + `) ORDER BY created_at, id`
	args := []any{project}
	for _, status := range statuses {
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// WaitingHumanRow is one task of a live autopilot whose review waits for a
// person: what Station's building counts and links to.
type WaitingHumanRow struct {
	Project     string
	AutopilotID string
	GoalNodeID  string
	NodeID      string
	PipelineID  string
}

// WaitingHuman returns, in one query, the tasks of the given projects' live
// autopilots (running or paused) that wait for a person's review.
func (s *Store) WaitingHuman(ctx context.Context, projects []string) ([]WaitingHumanRow, error) {
	out := []WaitingHumanRow{}
	if len(projects) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(projects)+1)
	for _, project := range projects {
		args = append(args, project)
	}
	args = append(args, runnerwire.TaskWaitingHuman)
	rows, err := s.db.QueryContext(ctx, `SELECT r.project, r.id, r.goal_node_id, t.node_id, t.pipeline_id
		FROM autopilot_tasks t JOIN autopilot_runs r ON r.id = t.autopilot_id
		WHERE r.project IN (?`+strings.Repeat(", ?", len(projects)-1)+`) AND r.status IN ('running', 'paused')
		AND t.state = ? AND t.pipeline_id <> ''
		ORDER BY r.project, r.created_at, t.node_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row WaitingHumanRow
		if err := rows.Scan(&row.Project, &row.AutopilotID, &row.GoalNodeID, &row.NodeID, &row.PipelineID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Tasks returns an autopilot's task instances, unordered.
func (s *Store) Tasks(ctx context.Context, autopilotID string) ([]runnerwire.TaskInstance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM autopilot_tasks WHERE autopilot_id = ?`, autopilotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runnerwire.TaskInstance
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var t runnerwire.TaskInstance
		if json.Unmarshal([]byte(raw), &t) == nil && t.NodeID != "" {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// ByRun returns the live autopilots a run or a pipeline belongs to.
func (s *Store) ByRun(ctx context.Context, runID, pipelineID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT t.autopilot_id FROM autopilot_tasks t
		JOIN autopilot_runs r ON r.id = t.autopilot_id
		WHERE r.status IN ('running', 'paused') AND ((t.run_id <> '' AND t.run_id = ?) OR (t.pipeline_id <> '' AND t.pipeline_id = ?))`,
		runID, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Save writes an autopilot's state and its changed tasks in one transaction.
func (s *Store) Save(ctx context.Context, r Record, tasks []runnerwire.TaskInstance) (Record, error) {
	now := s.stamp()
	r.UpdatedAt = now
	if final(r.Status) && r.FinishedAt == "" {
		r.FinishedAt = now
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback() }()
	stages, err := encodeStages(r)
	if err != nil {
		return r, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE autopilot_runs SET status = ?, reason = ?, updated_at = ?, finished_at = ?,
		stages = ?, goal_phase = ?, qa_round = ?, qa_run_id = ?
		WHERE id = ?`, r.Status, r.Reason, r.UpdatedAt, r.FinishedAt,
		stages, r.GoalPhase, int64(r.QARound), r.QARunID, r.ID); err != nil {
		return r, err
	}
	for _, t := range tasks {
		t.UpdatedAt = now
		if err := putTask(ctx, tx, r.ID, t, now); err != nil {
			return r, err
		}
	}
	return r, tx.Commit()
}

// final reports whether an autopilot status is an end state.
func final(status string) bool {
	switch status {
	case runnerwire.AutopilotSucceeded, runnerwire.AutopilotFailed, runnerwire.AutopilotCancelled:
		return true
	}
	return false
}
