package runnerwire

// Remote merge approval (W4). Someone looking at a run's diff — on a phone,
// say — approves merging its pull request. The hub never merges anything: it
// asks the run's runner, which merges with its owner's own gh login, and only
// a PR it opened itself for that run, only while it is open, not a draft and
// its CI is green. How to merge (squash, merge, rebase) is the runner's
// setting, never the request's.
//
//	hub    → runner  merge         {runId, mergeId}
//	runner → hub     merge_result  {runId, mergeId, reason}  reason "" = merged
//	runner → hub     pr            {runId, pr}               state merged, as usual
//
// A runner that is offline when the merge is approved gets it on reconnect,
// while the request is younger than MergeRequestTTLSeconds.

// Message types.
const (
	TypeMerge       = "merge"
	TypeMergeResult = "merge_result"
)

// MergeRequestTTLSeconds bounds how long an approved merge waits for its
// runner. An approval from yesterday must not merge whatever the PR has
// become since.
const MergeRequestTTLSeconds = 15 * 60

// Merge request statuses.
const (
	MergePending = "pending" // approved, not yet answered by the runner
	MergeDone    = "merged"
	MergeFailed  = "failed"
	MergeExpired = "expired"
)

// MergeRequest is one approval to merge a run's PR.
type MergeRequest struct {
	ID    string `json:"id"`
	RunID string `json:"runId"`
	// HeadSHA is the PR head the approver saw. The runner refuses when the PR
	// has moved on since: approving one diff must not merge another.
	HeadSHA     string `json:"headSha"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	RequestedBy string `json:"requestedBy"`
	CreatedAt   string `json:"createdAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
}

// MergeJob is what the hub sends with TypeMerge (Envelope.Merge).
type MergeJob struct {
	ID      string `json:"id"`
	RunID   string `json:"runId"`
	HeadSHA string `json:"headSha"`
}

// MergeOutcome is what the runner answers with TypeMergeResult
// (Envelope.MergeResult).
type MergeOutcome struct {
	ID    string `json:"id"`
	RunID string `json:"runId"`
	// Error is "" when the PR was merged.
	Error string `json:"error,omitempty"`
}

// MergeRequestBody is POST /api/runs/:id/merge. HeadSHA must match the run's
// last reported PR head.
type MergeRequestBody struct {
	HeadSHA string `json:"headSha"`
}

// Budgets (W4): per owner, enforced by the hub when a run is created. Zero
// means no limit. Cost is the CLIs' own estimate and only a reference (runs
// use the owner's subscription), so a budget is a brake, not an invoice.
type Budget struct {
	DailyRuns    int     `json:"dailyRuns"`
	DailyCostUSD float64 `json:"dailyCostUsd"`
	// DailyTokens counts input + output tokens.
	DailyTokens int64 `json:"dailyTokens"`
}

// BudgetStatus is GET /api/runs/budget: the caller's budget and today's use
// (UTC day), so the dispatch dialog can say why it would refuse.
type BudgetStatus struct {
	Budget Budget `json:"budget"`
	Used   struct {
		Runs    int     `json:"runs"`
		CostUSD float64 `json:"costUsd"`
		Tokens  int64   `json:"tokens"`
	} `json:"used"`
	// Exceeded names the limit that blocks a new run: "runs", "cost",
	// "tokens", or "".
	Exceeded string `json:"exceeded"`
}

// Realtime: run notifications for the web's toasts and system notifications
// (project room; ID is the run ID). Sent when a run finishes, its CI turns
// red, or its merge succeeds or fails.
const RunNotifyEvent = "run-notify"
