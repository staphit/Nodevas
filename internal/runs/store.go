// Package runs is the hub's record of runs: one agent CLI invocation on a
// runner, for one board node. It stores them, moves them through their
// lifecycle and hands offers to connected runners (dispatcher.go).
//
// Like internal/runners it must never start a process: this package must not
// import os/exec (internal/runners/noexec_test.go holds it to that). The hub
// is reachable from the internet and a run is code execution; only the runner
// on its owner's machine executes anything.
package runs

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
	"unicode/utf8"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

var (
	// ErrNotFound covers a run that does not exist or is not the caller's to
	// see or touch.
	ErrNotFound = errors.New("run not found")
	// ErrTransition is a status change the lifecycle does not allow from the
	// run's current status, e.g. accepting a run that was already cancelled.
	ErrTransition = errors.New("run status change not allowed")
	// ErrInvalid is a request that breaks the limits.
	ErrInvalid = errors.New("invalid run request")
)

// MaxErrorBytes caps runs.error: a reason, not a log.
const MaxErrorBytes = 4 << 10

// maxShortField caps the event fields that are names rather than text.
const maxShortField = 256

// legal is the lifecycle, from → allowed next statuses. Anything else is
// refused, so a late or replayed message cannot drag a finished run back.
var legal = map[string]map[string]bool{
	runnerwire.RunQueued: {runnerwire.RunOffered: true, runnerwire.RunCancelled: true},
	runnerwire.RunOffered: {
		runnerwire.RunRunning: true, runnerwire.RunDeclined: true, runnerwire.RunFailed: true,
		runnerwire.RunCancelled: true,
		// Back to the queue: the offer never reached anyone (the send failed,
		// or the runner restarted before anyone answered it).
		runnerwire.RunQueued: true,
	},
	runnerwire.RunRunning: {runnerwire.RunSucceeded: true, runnerwire.RunFailed: true, runnerwire.RunCancelled: true},
}

// Legal reports whether a run may move from one status to another.
func Legal(from, to string) bool { return legal[from][to] }

// Record is a stored run: the API's view plus what only the hub needs.
type Record struct {
	runnerwire.Run
	// OwnerID is the runner's owner when the run was made.
	OwnerID string
	// NodeTitle and JobPrompt are what the runner is offered.
	NodeTitle string
	JobPrompt string
	// W5: the rest of the fallback chain, and what a review or revise job is
	// offered (the PR branch, its pull request, the review to address).
	Fallbacks []runnerwire.AgentModel
	JobBranch string
	JobPRURL  string
	Review    string
	// SteppedUpAt is when the dispatcher's session stepped up, if it was
	// stepped up when the run was made ("" not). W6: offers vouch for the
	// dispatcher only within the step-up window from it (Dispatcher.SteppedUp).
	SteppedUpAt string
	// Handoff (handoff.go): the handoff branch and note this run is offered
	// to continue from (the API shows the branch as Run.ContinuesFrom), and
	// how many usage-limit waits its lineage has had. ResetAt is what the
	// runner's finish said about the limit's reset; it is not stored.
	JobHandoffBranch string
	JobHandoffNote   string
	LimitWaits       int
	ResetAt          string
}

// Job is the offer for this run.
func (r Record) Job() runnerwire.Job {
	return runnerwire.Job{
		RunID: r.ID, Project: r.Project, NodeID: r.NodeID, NodeTitle: r.NodeTitle,
		Prompt: r.JobPrompt, Agent: r.Agent, Model: r.Model, RepoID: r.RepoID,
		Base: r.Base, RequestedBy: r.CreatedByName,
		Kind: r.Kind, Branch: r.JobBranch, PRURL: r.JobPRURL, Review: r.Review,
		Mode:          r.Mode,
		HandoffBranch: r.JobHandoffBranch, HandoffNote: r.JobHandoffNote,
	}
}

// Store keeps runs and their events in the workspace database.
type Store struct {
	db  *db.DB
	now func() time.Time

	// last is the latest stamp handed out. Windows' clock can return the same
	// instant twice, and two runs with one created_at would have no order.
	mu   sync.Mutex
	last time.Time
}

// NewStore builds a store over the workspace database.
func NewStore(database *db.DB) *Store {
	return &Store{db: database, now: time.Now}
}

// stamp is RFC3339 with microseconds, fixed width so the text sorts in time
// order: the queue is served oldest first, and two runs made in the same
// second still have an order.
func (s *Store) stamp() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now().UTC().Truncate(time.Microsecond)
	if !t.After(s.last) {
		t = s.last.Add(time.Microsecond)
	}
	s.last = t
	return t.Format(stampLayout)
}

// stampLayout is the fixed-width form of every stored time.
const stampLayout = "2006-01-02T15:04:05.000000Z07:00"

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Truncate cuts s to at most max bytes without splitting a UTF-8 sequence,
// and replaces invalid UTF-8 so what is stored is always valid text.
func Truncate(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// New is what Create needs to record a run.
type New struct {
	Project, NodeID, NodeTitle    string
	RunnerID, RunnerName, OwnerID string
	Agent, Model                  string
	RepoID, RepoName, Base        string
	// Prompt is the dispatcher's extra instructions; JobPrompt the assembled
	// task the runner is offered.
	Prompt, JobPrompt        string
	CreatedBy, CreatedByName string
	// W5: job kind and what a review or revise works on; the run this one
	// falls back from, its pipeline, and the fallback chain still left.
	Kind                string
	JobBranch, JobPRURL string
	Review              string
	RetryOf, PipelineID string
	Fallbacks           []runnerwire.AgentModel
	// SteppedUpAt is the dispatcher's step-up (W6), "" for none. Follow-up
	// runs (fallbacks, pipeline steps) carry their parent's, so they are bound
	// by the same window.
	SteppedUpAt string
	// Mode is runnerwire.ModeHeadless or ModeInteractive (W2.5).
	Mode string
	// Handoff (handoff.go): the handoff to continue from; RetryAt, for a run
	// that waits for a usage limit to reset, when it may be offered; and the
	// lineage's waits so far.
	JobHandoffBranch, JobHandoffNote string
	RetryAt                          string
	LimitWaits                       int
	// Agent roles: the role the run acts in ("" legacy) and a person's stage
	// override reason. RED PHASE STUB: not stored yet.
	Role, Override string
}

// Create records a queued run.
func (s *Store) Create(ctx context.Context, in New) (Record, error) {
	for _, required := range []string{in.Project, in.NodeID, in.RunnerID, in.OwnerID, in.Agent, in.RepoID, in.CreatedBy} {
		if strings.TrimSpace(required) == "" {
			return Record{}, fmt.Errorf("%w: missing field", ErrInvalid)
		}
	}
	if in.Mode != runnerwire.ModeHeadless && in.Mode != runnerwire.ModeInteractive {
		return Record{}, fmt.Errorf("%w: unknown mode", ErrInvalid)
	}
	if len(in.JobPrompt) > runnerwire.MaxPromptBytes {
		return Record{}, fmt.Errorf("%w: prompt over %d bytes", ErrInvalid, runnerwire.MaxPromptBytes)
	}
	if !jobKinds[in.Kind] {
		return Record{}, fmt.Errorf("%w: job kind %q", ErrInvalid, in.Kind)
	}
	fallbacks, err := encodeChain(in.Fallbacks)
	if err != nil {
		return Record{}, err
	}
	// Only a handoff branch name, and only for an implement run: the runner
	// checks it is its own, the hub that it is one at all.
	handoffBranch := ""
	if in.JobHandoffBranch != "" {
		if !runnerwire.ValidHandoffBranch(in.JobHandoffBranch) || in.Kind != runnerwire.JobImplement {
			return Record{}, fmt.Errorf("%w: handoff branch %q", ErrInvalid, in.JobHandoffBranch)
		}
		handoffBranch = in.JobHandoffBranch
	}
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	now := s.stamp()
	// A revise job's review is a run's summary; a plan job's is the revision
	// it reconciles, which may be longer.
	reviewCap := runnerwire.MaxSummaryBytes
	if in.Kind == runnerwire.JobPlan {
		reviewCap = runnerwire.MaxRevisionBytes
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO runs (id, project, node_id, runner_id, runner_name, owner_id, agent, model,
                  repo_id, repo_name, base, prompt, node_title, job_prompt, status,
                  created_by, created_by_name, created_at,
                  kind, job_branch, job_pr_url, review, retry_of, pipeline_id, fallbacks,
                  stepped_up_at, mode, job_handoff_branch, job_handoff_note, retry_at, limit_waits)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Project, in.NodeID, in.RunnerID, in.RunnerName, in.OwnerID, in.Agent, in.Model,
		in.RepoID, in.RepoName, in.Base, in.Prompt, in.NodeTitle, in.JobPrompt, runnerwire.RunQueued,
		in.CreatedBy, in.CreatedByName, now,
		in.Kind, Truncate(in.JobBranch, maxShortField), Truncate(in.JobPRURL, MaxPRURLBytes),
		Truncate(in.Review, reviewCap), in.RetryOf, in.PipelineID, fallbacks,
		Truncate(in.SteppedUpAt, 64), in.Mode, handoffBranch, Truncate(in.JobHandoffNote, runnerwire.MaxHandoffNoteBytes),
		Truncate(in.RetryAt, 64), int64(in.LimitWaits))
	if err != nil {
		return Record{}, err
	}
	return s.Get(ctx, id)
}

const runColumns = `id, project, node_id, runner_id, runner_name, owner_id, agent, model,
       repo_id, repo_name, base, branch, prompt, node_title, job_prompt, status, error, summary,
       exit_code, usage, pr_url, ci_status, created_by, created_by_name, created_at,
       started_at, finished_at, commit_sha, files_changed, additions, deletions,
       pr_number, pr_state, pr_error, pr_head_sha,
       kind, failure_kind, verdict, retry_of, pipeline_id, fallbacks, job_branch, job_pr_url, review,
       stepped_up_at, mode, handoff_branch, handoff_note, job_handoff_branch, job_handoff_note,
       retry_at, limit_waits`

func scanRun(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var usage, fallbacks string
	var exit, files, additions, deletions, prNumber, limitWaits int64
	if err := row.Scan(&r.ID, &r.Project, &r.NodeID, &r.RunnerID, &r.RunnerName, &r.OwnerID,
		&r.Agent, &r.Model, &r.RepoID, &r.RepoName, &r.Base, &r.Branch, &r.Prompt, &r.NodeTitle,
		&r.JobPrompt, &r.Status, &r.Error, &r.Summary, &exit, &usage, &r.PRURL, &r.CIStatus,
		&r.CreatedBy, &r.CreatedByName, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		&r.Commit, &files, &additions, &deletions, &prNumber, &r.PRState, &r.PRError, &r.PRHeadSHA,
		&r.Kind, &r.FailureKind, &r.Verdict, &r.RetryOf, &r.PipelineID, &fallbacks,
		&r.JobBranch, &r.JobPRURL, &r.Review, &r.SteppedUpAt, &r.Mode,
		&r.HandoffBranch, &r.HandoffNote, &r.JobHandoffBranch, &r.JobHandoffNote, &r.RetryAt, &limitWaits); err != nil {
		return Record{}, err
	}
	r.ExitCode = int(exit)
	r.LimitWaits = int(limitWaits)
	r.ContinuesFrom = r.JobHandoffBranch
	r.FilesChanged, r.Additions, r.Deletions, r.PRNumber = int(files), int(additions), int(deletions), int(prNumber)
	if usage != "" {
		var parsed runnerwire.Usage
		if json.Unmarshal([]byte(usage), &parsed) == nil {
			r.Usage = &parsed
		}
	}
	r.Fallbacks = decodeChain(fallbacks)
	// A plan run carrying a revision reconciles it (runnerwire.Run.Replan).
	r.Replan = r.Kind == runnerwire.JobPlan && r.Review != ""
	return r, nil
}

// Get returns one run, with its latest merge request.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if err := s.attachMerges(ctx, []*Record{&r}); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Filter narrows List.
type Filter struct {
	Project string
	// NodeID, when set, keeps that node's runs only.
	NodeID string
	// Active keeps the runs that have not ended (queued, offered, running),
	// every one of them: Limit is ignored, since they are bounded by what the
	// runners can hold and queue, and a capped list would hide some of a
	// node's parallel reviewers.
	Active bool
	// Viewer, when set, keeps the runs that person made or that ran on their
	// runners. Empty means everyone's (an administrator's view).
	Viewer string
	Limit  int
}

// List returns runs newest first.
func (s *Store) List(ctx context.Context, f Filter) ([]Record, error) {
	query := `SELECT ` + runColumns + ` FROM runs WHERE project = ?`
	args := []any{f.Project}
	if f.NodeID != "" {
		query += ` AND node_id = ?`
		args = append(args, f.NodeID)
	}
	if f.Active {
		query += ` AND status IN (?, ?, ?)`
		args = append(args, runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning)
	}
	if f.Viewer != "" {
		query += ` AND (created_by = ? OR owner_id = ?)`
		args = append(args, f.Viewer, f.Viewer)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	if !f.Active {
		limit := f.Limit
		if limit <= 0 || limit > 500 {
			limit = 50
		}
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	out, err := s.query(ctx, query, args...)
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
	return out, nil
}

// ForRunner returns the runner's runs in the given statuses, oldest first.
func (s *Store) ForRunner(ctx context.Context, runnerID string, statuses ...string) ([]Record, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query := `SELECT ` + runColumns + ` FROM runs WHERE runner_id = ? AND status IN (?` +
		strings.Repeat(", ?", len(statuses)-1) + `) ORDER BY created_at, id`
	args := []any{runnerID}
	for _, status := range statuses {
		args = append(args, status)
	}
	return s.query(ctx, query, args...)
}

// Busy counts the runs a runner holds: offered (waiting on someone there) and
// running. It is what MaxConcurrent is measured against.
func (s *Store) Busy(ctx context.Context, runnerID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE runner_id = ? AND status IN (?, ?)`,
		runnerID, runnerwire.RunOffered, runnerwire.RunRunning).Scan(&n)
	return n, err
}

func (s *Store) query(ctx context.Context, query string, args ...any) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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

// Change is what a transition writes besides the status. Zero fields are left
// as they are.
type Change struct {
	// Runner, when set, must be the run's runner: a runner may only move its
	// own runs, whatever ID it sends.
	Runner   string
	Base     string
	Branch   string
	Error    string
	Summary  string
	ExitCode *int
	Usage    *runnerwire.Usage
	// FailureKind and Verdict are what a finish reports (W5).
	FailureKind string
	Verdict     string
	// HandoffBranch and HandoffNote are a handoff a finish reports.
	HandoffBranch string
	HandoffNote   string
}

// Transition moves a run to status to, if the lifecycle allows it from the
// status the run is in. The update is conditional on that status, so two
// racing transitions (an accept and a cancel) cannot both win.
// started_at is stamped on entering running, finished_at on entering an end
// state.
func (s *Store) Transition(ctx context.Context, id, to string, change Change) (Record, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if change.Runner != "" && current.RunnerID != change.Runner {
		return Record{}, ErrNotFound
	}
	if !Legal(current.Status, to) {
		return current, fmt.Errorf("%w: %s → %s", ErrTransition, current.Status, to)
	}
	sets := []string{"status = ?"}
	args := []any{to}
	set := func(column string, value any) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if change.Base != "" {
		set("base", Truncate(change.Base, maxShortField))
	}
	if change.Branch != "" {
		set("branch", Truncate(change.Branch, maxShortField))
	}
	if change.Error != "" {
		set("error", Truncate(change.Error, MaxErrorBytes))
	}
	if change.Summary != "" {
		set("summary", Truncate(change.Summary, runnerwire.MaxSummaryBytes))
	}
	if change.FailureKind != "" {
		set("failure_kind", change.FailureKind)
	}
	if change.Verdict != "" {
		set("verdict", change.Verdict)
	}
	if change.HandoffBranch != "" {
		set("handoff_branch", change.HandoffBranch)
		set("handoff_note", Truncate(change.HandoffNote, runnerwire.MaxHandoffNoteBytes))
	}
	if change.ExitCode != nil {
		set("exit_code", int64(*change.ExitCode))
	}
	if change.Usage != nil {
		raw, err := json.Marshal(change.Usage)
		if err != nil {
			return Record{}, err
		}
		set("usage", string(raw))
	}
	now := s.stamp()
	switch {
	case to == runnerwire.RunRunning:
		set("started_at", now)
	case to == runnerwire.RunQueued && change.Error == "":
		// Back in the queue: whatever the last offer said no longer applies.
		set("error", "")
	case runnerwire.RunFinal(to):
		set("finished_at", now)
	}
	args = append(args, id, current.Status)
	result, err := s.db.ExecContext(ctx,
		`UPDATE runs SET `+strings.Join(sets, ", ")+` WHERE id = ? AND status = ?`, args...)
	if err != nil {
		return Record{}, err
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		// Someone else moved it between the read and the write.
		latest, getErr := s.Get(ctx, id)
		if getErr != nil {
			return Record{}, getErr
		}
		return latest, fmt.Errorf("%w: %s changed under us", ErrTransition, id)
	}
	return s.Get(ctx, id)
}

// Finish records how a run ended, as its runner reports it.
func (s *Store) Finish(ctx context.Context, id, runnerID string, result runnerwire.RunResult) (Record, error) {
	switch result.Status {
	case runnerwire.RunSucceeded, runnerwire.RunFailed, runnerwire.RunCancelled:
	default:
		return Record{}, fmt.Errorf("%w: finish status %q", ErrInvalid, result.Status)
	}
	exit := result.ExitCode
	failureKind := ""
	if result.Status == runnerwire.RunFailed {
		failureKind = CleanFailureKind(result.FailureKind)
	}
	// A handoff is a failed run's, and only ever a handoff branch's name: a
	// runner cannot have the hub hand on any other ref.
	handoffBranch, handoffNote := "", ""
	if result.Status == runnerwire.RunFailed && runnerwire.HandoffWorthy(failureKind) &&
		runnerwire.ValidHandoffBranch(result.HandoffBranch) {
		handoffBranch, handoffNote = result.HandoffBranch, result.HandoffNote
	}
	r, err := s.Transition(ctx, id, result.Status, Change{
		Runner: runnerID, Error: result.Error, Summary: result.Summary,
		ExitCode: &exit, Usage: result.Usage,
		FailureKind: failureKind, Verdict: CleanVerdict(result.Verdict),
		HandoffBranch: handoffBranch, HandoffNote: handoffNote,
	})
	if err == nil && result.Status == runnerwire.RunFailed {
		r.ResetAt = Truncate(result.ResetAt, 64)
	}
	return r, err
}

// cleanEvent clamps an event to the limits before it is stored.
func cleanEvent(e runnerwire.RunEvent) runnerwire.RunEvent {
	e.Type = Truncate(e.Type, maxShortField)
	e.At = Truncate(e.At, 64)
	e.Tool = Truncate(e.Tool, maxShortField)
	e.ToolID = Truncate(e.ToolID, maxShortField)
	e.Text = Truncate(e.Text, runnerwire.MaxEventText)
	e.Input = Truncate(e.Input, runnerwire.MaxEventText)
	return e
}

// AppendEvents stores a batch of a run's events and reports how many were
// new. A seq at or below the last one stored is a resend and is dropped, as
// is anything past MaxRunEvents. A usage event also updates the run's usage,
// even past the cap: usage is cumulative and the latest figure is the one
// that matters.
func (s *Store) AppendEvents(ctx context.Context, runID string, events []runnerwire.RunEvent) (int, error) {
	added := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		added = 0
		var last sql.NullInt64
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq), COUNT(*) FROM run_events WHERE run_id = ?`, runID).Scan(&last, &count); err != nil {
			return err
		}
		var usage *runnerwire.Usage
		for _, event := range events {
			if event.Seq <= 0 || (last.Valid && event.Seq <= last.Int64) {
				continue
			}
			last = sql.NullInt64{Int64: event.Seq, Valid: true}
			if event.Type == runnerwire.EventUsage && event.Usage != nil {
				usage = event.Usage
			}
			if count >= runnerwire.MaxRunEvents {
				continue
			}
			event = cleanEvent(event)
			payload, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO run_events (run_id, seq, type, at, payload) VALUES (?, ?, ?, ?, ?)`,
				runID, event.Seq, event.Type, event.At, string(payload)); err != nil {
				return err
			}
			count++
			added++
		}
		if usage == nil {
			return nil
		}
		raw, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE runs SET usage = ? WHERE id = ?`, string(raw), runID)
		return err
	})
	return added, err
}

// Event page sizes for Events.
const (
	DefaultEventPage = 500
	MaxEventPage     = 1000
)

// Events returns a run's events after seq after, up to limit, and whether
// more follow.
func (s *Store) Events(ctx context.Context, runID string, after int64, limit int) ([]runnerwire.RunEvent, bool, error) {
	if limit <= 0 {
		limit = DefaultEventPage
	}
	if limit > MaxEventPage {
		limit = MaxEventPage
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM run_events WHERE run_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		runID, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []runnerwire.RunEvent{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, false, err
		}
		var event runnerwire.RunEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, false, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
