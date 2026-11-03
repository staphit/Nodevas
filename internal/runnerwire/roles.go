package runnerwire

import "unicode/utf8"

// Agent roles and workflow stages on the wire (spec section 4): a job's role
// and toolset, the red/green verify job, judge checklists, and the evidence a
// staged pipeline records. The test command itself never crosses the wire:
// a runner only says whether a repo has one (Repo.Verify).

// Job kinds added by the staged pipeline.
const (
	// JobTest: a Tester run. Like an implement run, but it opens a draft PR
	// and may change only test files.
	JobTest = "test"
	// JobVerify: no agent and no model. The runner runs the repo's own test
	// command at Job.Verify.Head and reports a VerifyResult.
	JobVerify = "verify"
	// JobJudge: a read-only agent run that judges acceptance items
	// (CHECK lines) and gives a pass/fail verdict.
	JobJudge = "judge"
)

// Runner capabilities for staged pipelines.
const (
	CapVerify = "verify"
	CapRoles  = "roles"
	CapJudge  = "judge"
)

// Verify phases.
const (
	VerifyRed   = "red"
	VerifyGreen = "green"
	VerifyGoal  = "goal"
)

// Judge verdicts (RunResult.Verdict on a judge job).
const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

// Limits on role traffic.
const (
	MaxCheckResults = 30
	MaxVerifyOutput = 8 << 10
	MaxTestFiles    = 500
	MaxCheckNote    = 300
)

// AcceptanceItem is one acceptance criterion, as data for a prompt.
type AcceptanceItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// VerifySpec is what a verify (or goal judge) job checks: the phase and the
// commit the tests must run at.
type VerifySpec struct {
	Phase string `json:"phase"`
	Head  string `json:"head,omitempty"`
}

// VerifyResult is one run of the repo's test command. Passed is relative to
// the phase: red passes when the tests fail.
type VerifyResult struct {
	Phase      string `json:"phase"`
	Head       string `json:"head,omitempty"`
	ExitCode   int    `json:"exitCode"`
	Passed     bool   `json:"passed"`
	DurationMs int64  `json:"durationMs"`
	Output     string `json:"output,omitempty"`
	NoCommand  bool   `json:"noCommand,omitempty"`
}

// CheckResult is a judge's verdict on one acceptance item.
type CheckResult struct {
	ID   string `json:"id"`
	Pass bool   `json:"pass"`
	Note string `json:"note,omitempty"`
}

// GuardReport is the executor diff check: test files the Tester wrote that
// the Executor touched, and sensitive paths (test infrastructure).
type GuardReport struct {
	TestFiles  []string `json:"testFiles,omitempty"`
	Touched    []string `json:"touched,omitempty"`
	Sensitive  []string `json:"sensitive,omitempty"`
	Approved   bool     `json:"approved,omitempty"`
	ApprovedBy string   `json:"approvedBy,omitempty"`
}

// Violation reports whether the executor touched protected files.
func (g GuardReport) Violation() bool { return len(g.Touched) > 0 || len(g.Sensitive) > 0 }

// Staged pipeline phases (PipelineState.Phase).
const (
	PhaseTest        = "test"
	PhaseVerifyRed   = "verify_red"
	PhaseImplement   = "implement"
	PhaseVerifyGreen = "verify_green"
	PhaseGuard       = "guard"
	PhaseCI          = "ci"
	PhaseReview      = "review"
	PhaseHuman       = "human"
	PhaseJudge       = "judge"
)

// RouteRole matches a job's role (RouteRule.Value is a role id).
const RouteRole = "role"

// Autopilot stage names for the role pipeline.
const (
	StageTest        = "test"
	StageVerifyRed   = "verify_red"
	StageVerifyGreen = "verify_green"
	StageGuard       = "guard"
	StageJudge       = "judge"
	StageQA          = "qa"
	StageGoalReview  = "goal_review"
)

// Goal phases (autopilot).
const (
	GoalPhaseAcceptance = "acceptance"
	GoalPhasePlan       = "plan"
	GoalPhaseTasks      = "tasks"
	GoalPhaseQA         = "qa"
	GoalPhaseHuman      = "human"
	GoalPhaseDone       = "done"
)

// RoleSettings is one owner's concurrency and budget for a role
// (GET/PUT /api/runs/roles). Project "" applies to every project.
type RoleSettings struct {
	Role          string  `json:"role"`
	Project       string  `json:"project"`
	MaxConcurrent int     `json:"maxConcurrent"`
	DailyCostUSD  float64 `json:"dailyCostUsd"`
	DailyTokens   int64   `json:"dailyTokens"`
	DailyRuns     int     `json:"dailyRuns"`
}

// RoleSettingsResponse is the body of GET/PUT /api/runs/roles.
type RoleSettingsResponse struct {
	Roles []RoleSettings `json:"roles"`
}

// ClampRoleFields cuts a result's role fields to the wire limits: at most
// MaxCheckResults checks, MaxCheckNote bytes per note, MaxVerifyOutput bytes
// of verify output (the tail is kept). The runner sends clamped; the hub
// clamps again.
func (r *RunResult) ClampRoleFields() {
	if r == nil {
		return
	}
	if len(r.Checks) > MaxCheckResults {
		r.Checks = r.Checks[:MaxCheckResults]
	}
	for i := range r.Checks {
		r.Checks[i].ID = clampBytes(r.Checks[i].ID, 16)
		r.Checks[i].Note = clampBytes(r.Checks[i].Note, MaxCheckNote)
	}
	if r.Verify != nil {
		r.Verify.Output = tailBytes(r.Verify.Output, MaxVerifyOutput)
		r.Verify.Head = clampBytes(r.Verify.Head, 64)
		r.Verify.Phase = clampBytes(r.Verify.Phase, 16)
	}
}

// clampBytes cuts s to at most n bytes on a rune boundary, keeping the head.
func clampBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// tailBytes keeps the last n bytes of s on a rune boundary (a test run's
// failure summary is at its end).
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
