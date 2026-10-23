package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"nodevas/internal/runnerwire"
)

// PipelineRecord is a stored pipeline: the API's view plus what only the hub
// needs to run its next step.
type PipelineRecord struct {
	runnerwire.PipelineState
	OwnerID, CreatedBy, Project string
	// Fallbacks is the implement step's chain, given to every implement and
	// revise run.
	Fallbacks []runnerwire.AgentModel
	// JobPrompt and Prompt are the task as first offered and the dispatcher's
	// extra instructions.
	JobPrompt, Prompt string
	// Branch and PRURL are the pull request the implement step opened.
	Branch, PRURL string
	// Reason says why the pipeline ended as it did.
	Reason    string
	UpdatedAt string
}

// NewPipeline is what CreatePipeline needs.
type NewPipeline struct {
	OwnerID, CreatedBy, Project, NodeID string
	Pipeline                            runnerwire.Pipeline
	Fallbacks                           []runnerwire.AgentModel
	JobPrompt, Prompt                   string
}

// CreatePipeline records a running pipeline at round 1.
func (s *Store) CreatePipeline(ctx context.Context, in NewPipeline) (PipelineRecord, error) {
	if in.OwnerID == "" || in.CreatedBy == "" || in.Project == "" || in.NodeID == "" {
		return PipelineRecord{}, invalidf("missing pipeline field")
	}
	if err := ValidatePipeline(in.Pipeline, false); err != nil {
		return PipelineRecord{}, err
	}
	raw, err := json.Marshal(in.Pipeline)
	if err != nil {
		return PipelineRecord{}, err
	}
	fallbacks, err := encodeChain(in.Fallbacks)
	if err != nil {
		return PipelineRecord{}, err
	}
	id, err := newID()
	if err != nil {
		return PipelineRecord{}, err
	}
	now := s.stamp()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO run_pipelines (id, owner_id, created_by, project, node_id, pipeline, fallbacks,
                           job_prompt, prompt, round, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
		id, in.OwnerID, in.CreatedBy, in.Project, in.NodeID, string(raw), fallbacks,
		in.JobPrompt, in.Prompt, runnerwire.PipelineRunning, now, now); err != nil {
		return PipelineRecord{}, err
	}
	return s.Pipeline(ctx, id)
}

// Pipeline returns one pipeline with its runs' IDs, oldest first.
func (s *Store) Pipeline(ctx context.Context, id string) (PipelineRecord, error) {
	var p PipelineRecord
	var raw, fallbacks string
	var round int64
	err := s.db.QueryRowContext(ctx, `
SELECT id, owner_id, created_by, project, node_id, pipeline, fallbacks, job_prompt, prompt,
       branch, pr_url, round, status, reason, created_at, updated_at
FROM run_pipelines WHERE id = ?`, id).Scan(
		&p.ID, &p.OwnerID, &p.CreatedBy, &p.Project, &p.NodeID, &raw, &fallbacks, &p.JobPrompt, &p.Prompt,
		&p.Branch, &p.PRURL, &round, &p.Status, &p.Reason, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineRecord{}, ErrNotFound
	}
	if err != nil {
		return PipelineRecord{}, err
	}
	if err := json.Unmarshal([]byte(raw), &p.Pipeline); err != nil {
		return PipelineRecord{}, err
	}
	p.Round = int(round)
	p.Fallbacks = decodeChain(fallbacks)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM runs WHERE pipeline_id = ? ORDER BY created_at, id`, id)
	if err != nil {
		return PipelineRecord{}, err
	}
	defer rows.Close()
	p.RunIDs = []string{}
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return PipelineRecord{}, err
		}
		p.RunIDs = append(p.RunIDs, runID)
	}
	return p, rows.Err()
}

// EndPipeline moves a running pipeline to an end status. It reports whether
// this call ended it: one that already ended is left as it is.
func (s *Store) EndPipeline(ctx context.Context, id, status, reason string) (bool, error) {
	switch status {
	case runnerwire.PipelineApproved, runnerwire.PipelineChangesLeft,
		runnerwire.PipelineFailed, runnerwire.PipelineCancelled:
	default:
		return false, invalidf("pipeline status %q", status)
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE run_pipelines SET status = ?, reason = ?, updated_at = ? WHERE id = ? AND status = ?`,
		status, Truncate(reason, MaxErrorBytes), s.stamp(), id, runnerwire.PipelineRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ClaimPR records the implement step's pull request on a running pipeline
// that has none yet. It reports whether this call recorded it, so a PR
// reported twice starts one review, not two.
func (s *Store) ClaimPR(ctx context.Context, id, branch, prURL string) (bool, error) {
	if prURL == "" {
		return false, invalidf("missing pull request")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE run_pipelines SET branch = ?, pr_url = ?, updated_at = ?
WHERE id = ? AND status = ? AND pr_url = ''`,
		Truncate(branch, maxShortField), Truncate(prURL, MaxPRURLBytes), s.stamp(), id, runnerwire.PipelineRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// NextRound moves a running pipeline from round to round+1. It reports
// whether this call moved it.
func (s *Store) NextRound(ctx context.Context, id string, round int) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE run_pipelines SET round = ?, updated_at = ? WHERE id = ? AND status = ? AND round = ?`,
		int64(round+1), s.stamp(), id, runnerwire.PipelineRunning, int64(round))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// PipelineRuns returns a pipeline's runs, oldest first.
func (s *Store) PipelineRuns(ctx context.Context, id string) ([]Record, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: missing pipeline", ErrInvalid)
	}
	return s.query(ctx, `SELECT `+runColumns+` FROM runs WHERE pipeline_id = ? ORDER BY created_at, id`, id)
}
