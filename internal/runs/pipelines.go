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
	// Awaiting is AwaitingHuman while a running pipeline waits for a person
	// (PipelineState.Status then says waiting_human); "" otherwise.
	Awaiting string
}

// AwaitingHuman is PipelineRecord.Awaiting while a person must review.
const AwaitingHuman = "human"

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
       branch, pr_url, round, status, reason, awaiting, created_at, updated_at
FROM run_pipelines WHERE id = ?`, id).Scan(
		&p.ID, &p.OwnerID, &p.CreatedBy, &p.Project, &p.NodeID, &raw, &fallbacks, &p.JobPrompt, &p.Prompt,
		&p.Branch, &p.PRURL, &round, &p.Status, &p.Reason, &p.Awaiting, &p.CreatedAt, &p.UpdatedAt)
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
	if p.Status == runnerwire.PipelineRunning && p.Awaiting == AwaitingHuman {
		p.Status = runnerwire.PipelineWaitingHuman
	}
	votes, err := s.Votes(ctx, id)
	if err != nil {
		return PipelineRecord{}, err
	}
	for _, v := range votes {
		p.Votes = append(p.Votes, v.ReviewVote)
		if v.Reviewer == HumanReviewerKey && v.Round == p.Round {
			p.HumanVerdict = v.Verdict
		}
	}
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
		`UPDATE run_pipelines SET status = ?, reason = ?, awaiting = '', updated_at = ? WHERE id = ? AND status = ?`,
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

// NextRound moves a running pipeline from round to round+1 (and off waiting
// for a person). It reports whether this call moved it.
func (s *Store) NextRound(ctx context.Context, id string, round int) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE run_pipelines SET round = ?, awaiting = '', updated_at = ? WHERE id = ? AND status = ? AND round = ?`,
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

// AwaitHuman has a running pipeline in round wait for a person's review. It
// reports whether this call did.
func (s *Store) AwaitHuman(ctx context.Context, id string, round int) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE run_pipelines SET awaiting = ?, updated_at = ?
WHERE id = ? AND status = ? AND round = ? AND awaiting = ''`,
		AwaitingHuman, s.stamp(), id, runnerwire.PipelineRunning, int64(round))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// HumanReviewerKey is review_votes.reviewer for a person's vote.
const HumanReviewerKey = "human"

// Vote is one stored review vote.
type Vote struct {
	runnerwire.ReviewVote
	// Reviewer is the agent reviewer's position in the pipeline's list
	// ("0", "1", ...) or HumanReviewerKey.
	Reviewer string
	// Comment is a person's review text; ActorID is who they are.
	Comment, ActorID string
}

// maxVoteComment caps a person's review comment.
const maxVoteComment = 16 << 10

// StartVote records an agent reviewer's pending vote for its review run.
func (s *Store) StartVote(ctx context.Context, pipelineID string, round, index int, am runnerwire.AgentModel, runID string) error {
	now := s.stamp()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO review_votes (pipeline_id, round, reviewer, agent, model, verdict, run_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, '', ?, ?, ?)`,
		pipelineID, int64(round), fmt.Sprint(index), Truncate(am.Agent, maxShortField), Truncate(am.Model, maxShortField),
		runID, now, now)
	return err
}

// SettleVote records the verdict of the review run runID: approve, changes or
// abstain. It reports whether this call settled a pending vote.
func (s *Store) SettleVote(ctx context.Context, runID, verdict string) (bool, error) {
	switch verdict {
	case runnerwire.VerdictApprove, runnerwire.VerdictChanges, runnerwire.VerdictAbstain:
	default:
		return false, invalidf("verdict %q", verdict)
	}
	if runID == "" {
		return false, invalidf("missing run")
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE review_votes SET verdict = ?, updated_at = ? WHERE run_id = ? AND verdict = ''`,
		verdict, s.stamp(), runID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// MoveVote hands a review's pending vote from one run to the run that
// retries it (a fallback, or another try after no verdict), so the retry's
// verdict settles the reviewer's seat. A vote already settled stays.
func (s *Store) MoveVote(ctx context.Context, fromRunID, toRunID string) error {
	if fromRunID == "" || toRunID == "" {
		return invalidf("missing run")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_votes SET run_id = ?, updated_at = ? WHERE run_id = ? AND verdict = ''`,
		toRunID, s.stamp(), fromRunID)
	return err
}

// HumanVote records a person's verdict in round. A second vote in the same
// round is refused (ErrPrecondition).
func (s *Store) HumanVote(ctx context.Context, pipelineID string, round int, actorID, name, verdict, comment string) error {
	switch verdict {
	case runnerwire.VerdictApprove, runnerwire.VerdictChanges:
	default:
		return invalidf("verdict must be %q or %q", runnerwire.VerdictApprove, runnerwire.VerdictChanges)
	}
	now := s.stamp()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO review_votes (pipeline_id, round, reviewer, agent, model, verdict, comment, actor_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (pipeline_id, round, reviewer) DO NOTHING`,
		pipelineID, int64(round), HumanReviewerKey, runnerwire.HumanReviewer, Truncate(name, maxShortField),
		verdict, Truncate(comment, maxVoteComment), actorID, now, now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: this round already has a person's review", ErrPrecondition)
	}
	return nil
}

// Votes returns a pipeline's votes by round: agent reviewers in list order,
// then the person.
func (s *Store) Votes(ctx context.Context, pipelineID string) ([]Vote, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT round, reviewer, agent, model, verdict, run_id, comment, actor_id
FROM review_votes WHERE pipeline_id = ?
ORDER BY round, CASE WHEN reviewer = 'human' THEN 1 ELSE 0 END, LENGTH(reviewer), reviewer`, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vote
	for rows.Next() {
		var v Vote
		var round int64
		if err := rows.Scan(&round, &v.Reviewer, &v.Agent, &v.Model, &v.Verdict, &v.RunID, &v.Comment, &v.ActorID); err != nil {
			return nil, err
		}
		v.Round = int(round)
		out = append(out, v)
	}
	return out, rows.Err()
}

// RoundVotes is Votes for one round.
func (s *Store) RoundVotes(ctx context.Context, pipelineID string, round int) ([]Vote, error) {
	all, err := s.Votes(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	var out []Vote
	for _, v := range all {
		if v.Round == round {
			out = append(out, v)
		}
	}
	return out, nil
}
