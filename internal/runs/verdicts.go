package runs

import (
	"context"
	"errors"
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
	// TODO(green): implement.
	return nil
}

// VerdictRole is the verdict role ("reviewer" or "qa") a run gives
// verdicts in, from its recorded role (a legacy run's kind), or "".
func VerdictRole(r Record) string {
	// TODO(green): implement.
	return ""
}

// SubmitVerdict validates and stores run's verdict, replacing an earlier one
// of the same run; replaced reports that it did.
func (s *Store) SubmitVerdict(ctx context.Context, r Record, in VerdictInput) (v Verdict, replaced bool, err error) {
	// TODO(green): implement.
	return Verdict{}, false, errors.New("not implemented")
}

// VerdictFor is a run's verdict; found false when it has none.
func (s *Store) VerdictFor(ctx context.Context, runID string) (Verdict, bool, error) {
	// TODO(green): implement.
	return Verdict{}, false, nil
}
