package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// Structured reviewer and QA verdicts (agent analytics B): what a review or
// judge run says through the submit_verdict MCP tool, one row per run,
// replaced when the run calls again. Finish reconciles the run's textual
// verdict with it, and writes one (source "finish") when the run gave none.

// Verdict outcomes, severities, sources and reason codes.
const (
	VerdictOutcomePass = "pass"
	VerdictOutcomeFail = "fail"

	SeverityNone     = "none"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"

	VerdictSourceMCP    = "mcp"
	VerdictSourceFinish = "finish"

	// MaxVerdictReasons and MaxVerdictNote bound one verdict.
	MaxVerdictReasons = 5
	MaxVerdictNote    = 1000
)

// VerdictReasons is the fixed list of reason codes.
var VerdictReasons = []string{
	"tests_missing", "tests_weak", "criteria_unmet", "regression", "style",
	"security", "scope_creep", "flaky", "other",
}

// Verdict is one run's structured verdict.
type Verdict struct {
	RunID        string   `json:"runId"`
	Project      string   `json:"-"`
	NodeID       string   `json:"-"`
	PipelineID   string   `json:"-"`
	Role         string   `json:"role"`
	Round        int      `json:"round"`
	SubjectRunID string   `json:"subjectRunId"`
	Agent        string   `json:"agent"`
	Model        string   `json:"model"`
	Outcome      string   `json:"outcome"`
	Severity     string   `json:"severity"`
	Reasons      []string `json:"reasons"`
	Note         string   `json:"note"`
	Source       string   `json:"source"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"-"`
	// Contradicted is computed when read (internal/analytics).
	Contradicted bool `json:"contradicted"`
}

// VerdictInput is what a run submits.
type VerdictInput struct {
	Outcome  string   `json:"outcome"`
	Severity string   `json:"severity"`
	Reasons  []string `json:"reasons"`
	Note     string   `json:"note,omitempty"`
}

// ErrVerdictRole: the run's role may not give a verdict.
var ErrVerdictRole = errors.New("only a reviewer or QA run may submit a verdict")

// ValidateVerdict checks a submitted verdict against the fixed reasons and
// the outcome's severity rules, and cleans its note.
func ValidateVerdict(in *VerdictInput) error {
	if in == nil {
		return fmt.Errorf("%w: no verdict", ErrInvalid)
	}
	in.Outcome = strings.TrimSpace(in.Outcome)
	in.Severity = strings.TrimSpace(in.Severity)
	switch in.Outcome {
	case VerdictOutcomePass, VerdictOutcomeFail:
	default:
		return fmt.Errorf("%w: outcome must be pass or fail", ErrInvalid)
	}
	rank, ok := severityRank[in.Severity]
	if !ok {
		return fmt.Errorf("%w: severity must be one of none, low, medium, high, critical", ErrInvalid)
	}
	if len(in.Reasons) > MaxVerdictReasons {
		return fmt.Errorf("%w: at most %d reasons", ErrInvalid, MaxVerdictReasons)
	}
	seen := map[string]bool{}
	for _, reason := range in.Reasons {
		if !slices.Contains(VerdictReasons, reason) {
			return fmt.Errorf("%w: unknown reason %q", ErrInvalid, reason)
		}
		if seen[reason] {
			return fmt.Errorf("%w: reason %q given twice", ErrInvalid, reason)
		}
		seen[reason] = true
	}
	switch in.Outcome {
	case VerdictOutcomePass:
		if rank > severityRank[SeverityLow] {
			return fmt.Errorf("%w: a pass has severity none or low", ErrInvalid)
		}
		for _, reason := range in.Reasons {
			if !passReasons[reason] {
				return fmt.Errorf("%w: a pass may only note style, flaky or other", ErrInvalid)
			}
		}
	case VerdictOutcomeFail:
		if len(in.Reasons) == 0 {
			return fmt.Errorf("%w: a fail needs at least one reason", ErrInvalid)
		}
		if rank < severityRank[SeverityLow] {
			return fmt.Errorf("%w: a fail has severity low or higher", ErrInvalid)
		}
	}
	if len(in.Note) > MaxVerdictNote {
		return fmt.Errorf("%w: note over %d bytes", ErrInvalid, MaxVerdictNote)
	}
	if !utf8.ValidString(in.Note) {
		return fmt.Errorf("%w: note is not valid text", ErrInvalid)
	}
	in.Note = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, in.Note))
	if in.Reasons == nil {
		in.Reasons = []string{}
	}
	return nil
}

var severityRank = map[string]int{
	SeverityNone: 0, SeverityLow: 1, SeverityMedium: 2, SeverityHigh: 3, SeverityCritical: 4,
}

// passReasons are the reasons a pass may note.
var passReasons = map[string]bool{"style": true, "flaky": true, "other": true}

// VerdictRole is the verdict role ("reviewer" or "qa") a run gives
// verdicts in, from its recorded role (a legacy run's kind), or "".
func VerdictRole(r Record) string {
	if r.Role != "" {
		switch slotOf(r.Role) {
		case engine.RoleReviewer:
			return engine.RoleReviewer
		case engine.RoleQA:
			return engine.RoleQA
		}
		return ""
	}
	switch r.Kind {
	case runnerwire.JobReview:
		return engine.RoleReviewer
	case runnerwire.JobJudge:
		return engine.RoleQA
	}
	return ""
}

// slotOf is a role id's built-in slot: a built-in id is its own; a custom
// role's base is not known from the id alone, so it is none.
func slotOf(role string) string {
	switch role {
	case engine.RolePlanner, engine.RoleQA, engine.RoleTester, engine.RoleExecutor, engine.RoleReviewer, engine.RoleResearcher:
		return role
	}
	return ""
}

// SubmitVerdict validates and stores run's verdict, replacing an earlier one
// of the same run; replaced reports that it did.
func (s *Store) SubmitVerdict(ctx context.Context, r Record, in VerdictInput) (v Verdict, replaced bool, err error) {
	return s.submitVerdict(ctx, r, VerdictRole(r), in, VerdictSourceMCP)
}

// SubmitVerdictAs is SubmitVerdict with the verdict role already resolved
// by the caller (a custom role's base slot).
func (s *Store) SubmitVerdictAs(ctx context.Context, r Record, role string, in VerdictInput) (Verdict, bool, error) {
	if role != engine.RoleReviewer && role != engine.RoleQA {
		return Verdict{}, false, ErrVerdictRole
	}
	return s.submitVerdict(ctx, r, role, in, VerdictSourceMCP)
}

func (s *Store) submitVerdict(ctx context.Context, r Record, role string, in VerdictInput, source string) (Verdict, bool, error) {
	if role == "" {
		return Verdict{}, false, ErrVerdictRole
	}
	if err := ValidateVerdict(&in); err != nil {
		return Verdict{}, false, err
	}
	round, subject, err := s.verdictRound(ctx, r, role)
	if err != nil {
		return Verdict{}, false, err
	}
	reasons, err := json.Marshal(in.Reasons)
	if err != nil {
		return Verdict{}, false, err
	}
	now := s.stamp()
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_verdicts WHERE run_id = ?`, r.ID).Scan(&existing); err != nil {
		return Verdict{}, false, err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO run_verdicts (run_id, project, node_id, pipeline_id, role, round, subject_run_id, agent, model,
                          outcome, severity, reasons, note, source, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (run_id) DO UPDATE SET
    outcome = excluded.outcome, severity = excluded.severity, reasons = excluded.reasons,
    note = excluded.note, source = excluded.source, round = excluded.round,
    subject_run_id = excluded.subject_run_id, updated_at = excluded.updated_at`,
		r.ID, r.Project, r.NodeID, r.PipelineID, role, round, subject, r.Agent, r.Model,
		in.Outcome, in.Severity, string(reasons), in.Note, source, now, now); err != nil {
		return Verdict{}, false, err
	}
	v, _, err := s.VerdictFor(ctx, r.ID)
	return v, existing > 0, err
}

// verdictRound is a verdict's round and the run it judges: a reviewer's is
// its pipeline's round (1 without one) and the latest implement or revise
// run in it before the review; a QA verdict's round counts the node's
// earlier QA verdicts, and judges no single run.
func (s *Store) verdictRound(ctx context.Context, r Record, role string) (int, string, error) {
	if role == engine.RoleQA {
		var n int
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM run_verdicts WHERE project = ? AND node_id = ? AND role = ? AND run_id <> ?`,
			r.Project, r.NodeID, engine.RoleQA, r.ID).Scan(&n); err != nil {
			return 0, "", err
		}
		return n + 1, "", nil
	}
	if r.PipelineID == "" {
		return 1, "", nil
	}
	var round int64 = 1
	if err := s.db.QueryRowContext(ctx, `SELECT round FROM run_pipelines WHERE id = ?`, r.PipelineID).Scan(&round); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	var subject string
	err := s.db.QueryRowContext(ctx, `
SELECT id FROM runs WHERE pipeline_id = ? AND kind IN (?, ?, 'implement') AND created_at < ? AND id <> ?
 ORDER BY created_at DESC LIMIT 1`, r.PipelineID, runnerwire.JobImplement, runnerwire.JobRevise, r.CreatedAt, r.ID).Scan(&subject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	return int(max(round, 1)), subject, nil
}

const verdictColumns = `run_id, project, node_id, pipeline_id, role, round, subject_run_id, agent, model,
       outcome, severity, reasons, note, source, created_at, updated_at`

func scanVerdict(row interface{ Scan(...any) error }) (Verdict, error) {
	var v Verdict
	var round int64
	var reasons string
	if err := row.Scan(&v.RunID, &v.Project, &v.NodeID, &v.PipelineID, &v.Role, &round, &v.SubjectRunID,
		&v.Agent, &v.Model, &v.Outcome, &v.Severity, &reasons, &v.Note, &v.Source, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return Verdict{}, err
	}
	v.Round = int(round)
	if json.Unmarshal([]byte(reasons), &v.Reasons) != nil || v.Reasons == nil {
		v.Reasons = []string{}
	}
	return v, nil
}

// VerdictFor is a run's verdict; found false when it has none.
func (s *Store) VerdictFor(ctx context.Context, runID string) (Verdict, bool, error) {
	v, err := scanVerdict(s.db.QueryRowContext(ctx, `SELECT `+verdictColumns+` FROM run_verdicts WHERE run_id = ?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return Verdict{}, false, nil
	}
	if err != nil {
		return Verdict{}, false, err
	}
	return v, true, nil
}

// VerdictsForRuns lists the verdicts of runs, keyed by run id.
func (s *Store) VerdictsForRuns(ctx context.Context, runIDs []string) (map[string]Verdict, error) {
	out := map[string]Verdict{}
	if len(runIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+verdictColumns+` FROM run_verdicts WHERE run_id IN (`+placeholders(len(runIDs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanVerdict(rows)
		if err != nil {
			return nil, err
		}
		out[v.RunID] = v
	}
	return out, rows.Err()
}

// reconcileVerdict is the runs.verdict a finishing run records: its
// structured verdict, when it gave one, decides over the textual line.
func (s *Store) reconcileVerdict(ctx context.Context, r Record, textual string) string {
	if VerdictRole(r) == "" {
		return textual
	}
	v, found, err := s.VerdictFor(ctx, r.ID)
	if err != nil || !found || v.Source != VerdictSourceMCP {
		return textual
	}
	switch r.Kind {
	case runnerwire.JobReview:
		if v.Outcome == VerdictOutcomePass {
			return runnerwire.VerdictApprove
		}
		return runnerwire.VerdictChanges
	case runnerwire.JobJudge:
		if v.Outcome == VerdictOutcomePass {
			return runnerwire.VerdictPass
		}
		return runnerwire.VerdictFail
	}
	return textual
}

// synthesizeVerdict writes a finished review or judge run's verdict from its
// textual one when the run gave no structured verdict.
func (s *Store) synthesizeVerdict(ctx context.Context, r Record) {
	if r.Kind != runnerwire.JobReview && r.Kind != runnerwire.JobJudge {
		return
	}
	role := VerdictRole(r)
	if role == "" {
		role = engine.RoleReviewer
		if r.Kind == runnerwire.JobJudge {
			role = engine.RoleQA
		}
	}
	var in VerdictInput
	switch r.Verdict {
	case runnerwire.VerdictApprove, runnerwire.VerdictPass:
		in = VerdictInput{Outcome: VerdictOutcomePass, Severity: SeverityNone, Reasons: []string{}}
	case runnerwire.VerdictChanges, runnerwire.VerdictFail:
		reason := "other"
		if r.Kind == runnerwire.JobJudge {
			for _, c := range r.Checks {
				if !c.Pass {
					reason = "criteria_unmet"
					break
				}
			}
		}
		in = VerdictInput{Outcome: VerdictOutcomeFail, Severity: SeverityMedium, Reasons: []string{reason}}
	default:
		return
	}
	if _, found, err := s.VerdictFor(ctx, r.ID); err != nil || found {
		return
	}
	if _, _, err := s.submitVerdict(ctx, r, role, in, VerdictSourceFinish); err != nil {
		slog.Warn("runs: record finish verdict", slog.String("run", r.ID), slog.Any("error", err))
	}
}

// LinkRoutePick ties an auto-routing pick to the run that followed it: only
// owner's pick for nodeID that no run has used yet.
func (s *Store) LinkRoutePick(ctx context.Context, pickID, ownerID, nodeID, runID string) {
	if pickID == "" || runID == "" {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE route_picks SET run_id = ? WHERE id = ? AND owner_id = ? AND node_id = ? AND run_id = ''`,
		runID, pickID, ownerID, nodeID); err != nil {
		slog.Warn("runs: link route pick", slog.String("pick", pickID), slog.Any("error", err))
	}
}
