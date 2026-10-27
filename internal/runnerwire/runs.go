package runnerwire

// Runs: one agent CLI invocation on a runner, for one board node, in its own
// git worktree. The hub records runs and relays them; only the runner starts
// the agent, and only after someone at the runner accepted the offer.
//
// Lifecycle on the connect socket:
//
//	hub    → runner  offer    {job}                  status queued → offered
//	runner → hub     decline  {runId, reason}        offered → declined
//	runner → hub     accept   {accept}               offered → running
//	runner → hub     events   {runId, events}        (running)
//	hub    → runner  cancel   {runId, reason}        (asks the runner to stop)
//	runner → hub     finish   {runId, result}        running|offered → succeeded|failed|cancelled
//
// A run the runner never finishes (it crashed, its machine slept) is failed by
// the hub when the runner reconnects without listing it in hello's active runs.

// Message types for runs, alongside the W0 types in wire.go.
const (
	// Hub → runner: a job the runner may take. Envelope.Job is set.
	TypeOffer = "offer"
	// Hub → runner: stop this run. Envelope.RunID and Reason are set.
	TypeCancel = "cancel"
	// Runner → hub: someone at the runner accepted; the worktree exists and the
	// agent is starting. Envelope.Accept is set.
	TypeAccept = "accept"
	// Runner → hub: the runner will not take this job. Envelope.RunID and
	// Reason are set.
	TypeDecline = "decline"
	// Runner → hub: progress. Envelope.RunID and Events are set.
	TypeEvents = "events"
	// Runner → hub: the run ended. Envelope.RunID and Result are set.
	TypeFinish = "finish"
)

// Run statuses.
const (
	// Waiting for its runner to come online (or to have room).
	RunQueued = "queued"
	// Sent to the runner, waiting for someone there to accept it.
	RunOffered   = "offered"
	RunDeclined  = "declined"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// RunFinal reports whether a status is an end state.
func RunFinal(status string) bool {
	switch status {
	case RunDeclined, RunSucceeded, RunFailed, RunCancelled:
		return true
	}
	return false
}

// Event types: the normalised stream every agent adapter produces, so the UI
// never needs to know which CLI ran.
const (
	EventText       = "text"        // assistant prose; Text
	EventThinking   = "thinking"    // reasoning summary; Text
	EventToolCall   = "tool_call"   // Tool, ToolID, Input
	EventToolResult = "tool_result" // ToolID, Text, IsError
	EventUsage      = "usage"       // Usage, cumulative for the run so far
	EventError      = "error"       // Text
	EventLog        = "log"         // runner-side note (worktree made, process exited); Text
)

// Limits on run traffic. The runner truncates to them; the hub enforces them.
const (
	// MaxMessageBytes caps one socket message once hello is done.
	MaxMessageBytes = 1 << 20
	// MaxPromptBytes caps Job.Prompt.
	MaxPromptBytes = 64 << 10
	// MaxEventText caps RunEvent.Text and RunEvent.Input.
	MaxEventText = 16 << 10
	// MaxEventsPerMessage caps Envelope.Events.
	MaxEventsPerMessage = 128
	// MaxSummaryBytes caps RunResult.Summary.
	MaxSummaryBytes = 4 << 10
	// MaxRunEvents is how many events the hub keeps per run; later events are
	// dropped (the final result is still recorded).
	MaxRunEvents = 5000
)

// Job is what the hub offers a runner. It names a repository by ID only: the
// runner alone maps that to a path, from its own allowlist. It carries no
// permission settings: how freely the agent may act is the runner's call.
type Job struct {
	RunID   string `json:"runId"`
	Project string `json:"project"`
	NodeID  string `json:"nodeId"`
	// NodeTitle is for the acceptance prompt and the branch name.
	NodeTitle string `json:"nodeTitle"`
	// Prompt is the task as the hub assembled it: node title, body, and the
	// dispatcher's extra instructions. The runner adds its own preamble.
	Prompt string `json:"prompt"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	RepoID string `json:"repoId"`
	// Base is the branch to start from; "" means the repository's default.
	Base string `json:"base,omitempty"`
	// RequestedBy is who dispatched it, for the acceptance prompt.
	RequestedBy string `json:"requestedBy"`

	// W5: what kind of job (JobImplement, JobReview, JobRevise; see
	// routing.go). For review and revise, Branch is the existing PR branch and
	// PRURL its pull request; Review is the review text a revise addresses.
	// For a plan job (W7), Review is the revision a replan reconciles
	// (CreateRunRequest.Revision); empty for a first breakdown.
	Kind   string `json:"kind,omitempty"`
	Branch string `json:"branch,omitempty"`
	PRURL  string `json:"prUrl,omitempty"`
	Review string `json:"review,omitempty"`

	// W2.5: ModeHeadless ("") or ModeInteractive; see terminal.go.
	Mode string `json:"mode,omitempty"`

	// W6: SteppedUp is the hub vouching that the dispatcher re-proved their
	// identity within the step-up window when this job was offered. A runner
	// may auto-accept only such jobs (AutoAcceptPolicy.RequireStepUp).
	SteppedUp bool `json:"steppedUp,omitempty"`

	// Handoff (handoff.go): an implement job that continues another run's
	// partial work. HandoffBranch names a local branch the runner itself made
	// when that run hit a usage limit; the runner starts the worktree from it
	// instead of Base, after checking it is one of its own. HandoffNote is the
	// note that run left, as the hub recorded it: data for the prompt, never
	// instructions. Older runners ignore both and start from Base.
	HandoffBranch string `json:"handoffBranch,omitempty"`
	HandoffNote   string `json:"handoffNote,omitempty"`

	// Agent roles (roles.go): the role the run acts in ("" legacy), the
	// hub-resolved tool allowlist for `nodevas mcp --tools`, the Tester's
	// files an Executor must not change, the acceptance items (data for the
	// prompt) and, for a verify or goal judge job, what to verify.
	Role       string           `json:"role,omitempty"`
	Tools      []string         `json:"tools,omitempty"`
	TestFiles  []string         `json:"testFiles,omitempty"`
	Acceptance []AcceptanceItem `json:"acceptance,omitempty"`
	Verify     *VerifySpec      `json:"verify,omitempty"`
}

// Accept reports an accepted job: the worktree and branch it runs in.
type Accept struct {
	RunID  string `json:"runId"`
	Base   string `json:"base"`
	Branch string `json:"branch"`
}

// RunEvent is one normalised step of a run.
type RunEvent struct {
	// Seq is assigned by the runner: 1, 2, 3… per run. The hub drops a
	// duplicate or out-of-order seq, so a resent batch is harmless.
	Seq  int64  `json:"seq"`
	Type string `json:"type"`
	// At is RFC3339 with milliseconds, on the runner's clock.
	At      string `json:"at"`
	Text    string `json:"text,omitempty"`
	Tool    string `json:"tool,omitempty"`
	ToolID  string `json:"toolId,omitempty"`
	Input   string `json:"input,omitempty"`
	IsError bool   `json:"isError,omitempty"`
	Usage   *Usage `json:"usage,omitempty"`
}

// Usage is token use and, where the CLI reports it, cost. Cost is a reference
// figure only: runs use the owner's subscription login, not an API key.
type Usage struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64   `json:"cacheWriteTokens,omitempty"`
	CostUSD          float64 `json:"costUsd,omitempty"`
	// Model is the model the CLI said it ran on, when it said: what a run
	// dispatched with model "default" actually used, for pricing.
	Model string `json:"model,omitempty"`
}

// RunResult is how a run ended.
type RunResult struct {
	// Status is RunSucceeded, RunFailed or RunCancelled.
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Summary  string `json:"summary,omitempty"`
	ExitCode int    `json:"exitCode"`
	Usage    *Usage `json:"usage,omitempty"`
	// FailureKind classifies a failed run (W5, routing.go): whether another
	// model might succeed where this one could not.
	FailureKind string `json:"failureKind,omitempty"`
	// Verdict is a review job's verdict (VerdictApprove / VerdictChanges).
	Verdict string `json:"verdict,omitempty"`
	// Handoff (handoff.go): on a HandoffWorthy failure, the local branch the
	// runner kept the partial work on ("" when there was nothing to keep) and
	// its handoff note. ResetAt is when the agent said its limit resets,
	// RFC3339, when it said. All optional: an older runner sends none.
	HandoffBranch string `json:"handoffBranch,omitempty"`
	HandoffNote   string `json:"handoffNote,omitempty"`
	ResetAt       string `json:"resetAt,omitempty"`
	// Agent roles (roles.go): a verify job's test run, a judge's CHECK
	// lines, and a review's "TESTS: approve-changes" acknowledgement.
	Verify   *VerifyResult `json:"verify,omitempty"`
	Checks   []CheckResult `json:"checks,omitempty"`
	GuardAck bool          `json:"guardAck,omitempty"`
}

// Run is a run as the HTTP API returns it.
type Run struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	NodeID     string `json:"nodeId"`
	RunnerID   string `json:"runnerId"`
	RunnerName string `json:"runnerName"`
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	RepoID     string `json:"repoId"`
	// RepoName is copied from the runner's hello when the run was created.
	RepoName string `json:"repoName"`
	Base     string `json:"base"`
	Branch   string `json:"branch"`
	// Prompt is the dispatcher's extra instructions, as typed.
	Prompt   string `json:"prompt"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Summary  string `json:"summary,omitempty"`
	ExitCode int    `json:"exitCode"`
	Usage    *Usage `json:"usage,omitempty"`
	// Changes and pull request (W3; see changes.go).
	Commit       string `json:"commit,omitempty"`
	FilesChanged int    `json:"filesChanged"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	PRURL        string `json:"prUrl,omitempty"`
	PRNumber     int    `json:"prNumber,omitempty"`
	PRState      string `json:"prState,omitempty"`
	PRError      string `json:"prError,omitempty"`
	CIStatus     string `json:"ciStatus,omitempty"`
	// PRHeadSHA is the PR head the CI status belongs to; a merge approval
	// must name it (W4).
	PRHeadSHA string `json:"prHeadSha,omitempty"`
	// Merge is the latest merge request for the run, if any (W4).
	Merge *MergeRequest `json:"merge,omitempty"`
	// W5: job kind, failure kind, review verdict, the run this one falls back
	// from, and the pipeline it belongs to.
	Kind        string `json:"kind,omitempty"`
	FailureKind string `json:"failureKind,omitempty"`
	Verdict     string `json:"verdict,omitempty"`
	RetryOf     string `json:"retryOf,omitempty"`
	PipelineID  string `json:"pipelineId,omitempty"`
	// W2.5: ModeHeadless ("") or ModeInteractive.
	Mode string `json:"mode,omitempty"`
	// Replan: a plan run that reconciles a goal's tasks with a revision of
	// its requirement (CreateRunRequest.Revision), not a first breakdown.
	Replan bool `json:"replan,omitempty"`
	// Handoff (handoff.go): the branch and note this run left when it hit a
	// usage limit; ContinuesFrom, the handoff branch this run started from;
	// RetryAt, while the run waits queued for its agent's usage limit to
	// reset, when it is offered again (RFC3339, "" once released).
	HandoffBranch string `json:"handoffBranch,omitempty"`
	HandoffNote   string `json:"handoffNote,omitempty"`
	ContinuesFrom string `json:"continuesFrom,omitempty"`
	RetryAt       string `json:"retryAt,omitempty"`
	// Agent roles (roles.go): the run's role ("" legacy), its verify result
	// and judge checks, and a person's stage override reason.
	Role     string        `json:"role,omitempty"`
	Verify   *VerifyResult `json:"verify,omitempty"`
	Checks   []CheckResult `json:"checks,omitempty"`
	Override string        `json:"override,omitempty"`
	// Agent analytics: the run's cost in USD (reported by the CLI, or
	// estimated from its tokens when CostEstimated), and the node's type and
	// acceptance size when it was dispatched (difficulty).
	CostUSD         float64 `json:"costUsd"`
	CostEstimated   bool    `json:"costEstimated"`
	NodeType        string  `json:"nodeType,omitempty"`
	AcceptanceCount int     `json:"acceptanceCount"`
	CreatedBy       string  `json:"createdBy"`
	CreatedByName   string  `json:"createdByName"`
	CreatedAt       string  `json:"createdAt"`
	StartedAt       string  `json:"startedAt,omitempty"`
	FinishedAt      string  `json:"finishedAt,omitempty"`
}

// CreateRunRequest is the body of POST /api/runs (project from ?project=).
type CreateRunRequest struct {
	NodeID   string `json:"nodeId"`
	RunnerID string `json:"runnerId"`
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	RepoID   string `json:"repoId"`
	Base     string `json:"base,omitempty"`
	// Prompt is extra instructions on top of the node's own content.
	Prompt string `json:"prompt,omitempty"`
	// Fallbacks are tried in order when the run fails with a fallback-worthy
	// FailureKind (W5). At most MaxFallbacks.
	Fallbacks []AgentModel `json:"fallbacks,omitempty"`
	// Pipeline, when set, dispatches implement → review instead of one run
	// (W5); Agent and Model are then ignored in favour of Pipeline.Implement.
	Pipeline *Pipeline `json:"pipeline,omitempty"`
	// Mode is ModeHeadless ("") or ModeInteractive (W2.5).
	Mode string `json:"mode,omitempty"`
	// Kind is JobImplement ("") or JobPlan (W7): a read-only run that breaks
	// the node into task nodes and changes no files.
	Kind string `json:"kind,omitempty"`
	// Revision makes a plan run a replan: the change to the goal's
	// requirement and the goal's tasks as they stand, which the planner
	// reconciles (see replan.go). Only with Kind JobPlan; at most
	// MaxRevisionBytes. The runner is offered it as Job.Review.
	Revision string `json:"revision,omitempty"`
	// Role is the role the run acts in ("" legacy). Override is a person's
	// reason for dispatching a plain implement run past enabled stages
	// (at most 200 characters); recorded on the run and the audit trail.
	Role     string `json:"role,omitempty"`
	Override string `json:"override,omitempty"`
	// RoutePick is the auto-routing pick (RouteSuggestion.RuleID "auto:<id>"
	// without the prefix) this dispatch followed, so the pick is linked to
	// its run. Another owner's or a used pick is ignored.
	RoutePick string `json:"routePick,omitempty"`
}

// RunListResponse is the body of GET /api/runs?nodeId=.
type RunListResponse struct {
	Runs []Run `json:"runs"`
}

// RunEventsResponse is the body of GET /api/runs/:id/events?after=<seq>.
type RunEventsResponse struct {
	Events []RunEvent `json:"events"`
	// More is true when the page was cut short; ask again after the last seq.
	More bool `json:"more"`
}

// Realtime events (project room). The ID is the node ID for RunsChangedEvent,
// the run ID for RunEventsEvent.
const (
	RunsChangedEvent = "runs-changed"
	RunEventsEvent   = "run-events"
)
