package runnerwire

// Workspace actions (agent workspace, phase 1). The runner's owner merges a
// run's branch into its base on the runner's machine, or discards the run's
// worktree and branch. The hub records the request and relays it; git runs
// on the runner, which decides from its own checks, never from the request.
//
//	hub    → runner  workspace_action {workspaceAction}
//	runner → hub     workspace_result {runId, workspaceResult}
//	runner → hub     pr               {runId, pr}   (state closed, after a discard closed it)
//
// A runner offline when asked gets it on reconnect while the request is
// younger than WorkspaceActionTTLSeconds; it answers a re-sent id from memory.

const (
	TypeWorkspaceAction = "workspace_action"
	TypeWorkspaceResult = "workspace_result"

	// CapRunWorkspace: the runner understands run-scoped repo ops and
	// workspace_action. CapWorkspaceActions: its owner lets the hub ask for
	// them (runner setting workspaceActions, on unless turned off).
	CapRunWorkspace     = "run-workspace"
	CapWorkspaceActions = "workspace-actions"

	WorkspaceMergeLocal = "merge_local"
	WorkspaceDiscard    = "discard"

	// Action statuses (RunWorkspaceAction.Status).
	WorkspacePending = "pending"
	WorkspaceDone    = "done"
	WorkspaceFailed  = "failed"
	WorkspaceExpired = "expired"

	// Run.Workspace values.
	RunWorkspaceLive        = ""
	RunWorkspaceMergedLocal = "merged_local"
	RunWorkspaceDiscarded   = "discarded"

	// WorkspaceActionTTLSeconds bounds how long a request waits for its
	// runner: a discard asked for yesterday must not run on whatever the
	// worktree has become since.
	WorkspaceActionTTLSeconds = 15 * 60
	// MaxWorkspaceReason caps WorkspaceOutcome.Error and Note.
	MaxWorkspaceReason = 1024
)

// Runner refusal codes (WorkspaceOutcome.Code, RunWorkspaceAction.Code).
const (
	WorkspaceErrRefused       = "refused"          // not the runner's owner
	WorkspaceErrOff           = "actions_off"      // owner turned workspaceActions off
	WorkspaceErrRunning       = "running"          // the run is still in hand on the runner
	WorkspaceErrNotOwnBranch  = "not_own_branch"   // branch is not agent/<node>-<run8> of this run
	WorkspaceErrHasPR         = "has_pr"           // an open PR: merge it there
	WorkspaceErrNoBranch      = "no_branch"        // the run's branch is gone
	WorkspaceErrNoBase        = "no_base"          // no local refs/heads/<base>
	WorkspaceErrHeadMoved     = "head_moved"       // branch tip != headSha
	WorkspaceErrUnsaved       = "unsaved_commits"  // discard: the branch holds commits after the run's, found nowhere else
	WorkspaceErrDirtyWorktree = "dirty_worktree"   // uncommitted files in the run's worktree
	WorkspaceErrDirtyBase     = "dirty_base"       // base checked out in the repo with changes / an op in progress
	WorkspaceErrBaseElsewhere = "base_elsewhere"   // base checked out in another worktree
	WorkspaceErrMergeDriver   = "merge_driver"     // repo config names a merge driver
	WorkspaceErrSigning       = "signing_program"  // temp-worktree merge: repo config names the signing program
	WorkspaceErrConflict      = "conflict"         // merge aborted, nothing changed
	WorkspaceErrForeign       = "foreign_worktree" // worktree dir is a link, or registered to another branch
	WorkspaceErrPRClose       = "pr_close_failed"  // gh could not close the PR; nothing removed
	WorkspaceErrFilesBusy     = "files_busy"       // Windows: a process holds files in the worktree
	WorkspaceErrGit           = "git"
)

// Hub gate codes (RunWorkspaceView, and the "blocked" of a 409): why the
// panel cannot offer an action now. head_moved is the POST's own.
const (
	WorkspaceGateNotOwner    = "not_owner"
	WorkspaceGateRunning     = "running"
	WorkspaceGateDiscarded   = "discarded"
	WorkspaceGateMergedLocal = "merged_local"
	WorkspaceGatePRMerged    = "pr_merged"
	WorkspaceGatePending     = "pending"
	WorkspaceGateKind        = "kind"
	WorkspaceGateNoCommit    = "no_commit"
	WorkspaceGateOutdated    = "outdated"
	WorkspaceGateActionsOff  = "actions_off"
	WorkspaceGateOffline     = "offline"
	WorkspaceGateHeadMoved   = "head_moved"
	// WorkspaceGateInUse: another unfinished run works on the same PR or
	// branch (a revise or review run, a later pipeline stage).
	WorkspaceGateInUse = "in_use"
)

// Merge modes (RunWorkspaceView.Merge.Mode).
const (
	WorkspaceMergeModePR    = "pr"
	WorkspaceMergeModeLocal = "local"
)

// WorkspaceActionJob is Envelope.WorkspaceAction. AccountID is who asked;
// the runner refuses anyone but its owner. Branch, Base, NodeID (or, for a
// global run, Slug) and Kind are what the hub recorded; the runner checks
// them against its own disk.
type WorkspaceActionJob struct {
	ID        string `json:"id"`
	RunID     string `json:"runId"`
	AccountID string `json:"accountId"`
	Action    string `json:"action"`
	RepoID    string `json:"repoId"`
	NodeID    string `json:"nodeId"`
	// Slug is a global run's branch slug (global.go), "" for a node's run.
	Slug   string `json:"slug,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	// HeadSHA is the run's commit as the hub has it: for merge_local the
	// commit the owner saw and merges; for discard the commit that may go
	// with the branch (commits past it that are on no other ref stop it).
	HeadSHA string `json:"headSha,omitempty"`
}

// WorkspaceOutcome is Envelope.WorkspaceResult. Code "" means done.
type WorkspaceOutcome struct {
	ID     string `json:"id"`
	RunID  string `json:"runId"`
	Action string `json:"action"`
	Code   string `json:"code,omitempty"`
	// Error is English, at most MaxWorkspaceReason.
	Error           string `json:"error,omitempty"`
	MergeCommit     string `json:"mergeCommit,omitempty"`
	WorktreeRemoved bool   `json:"worktreeRemoved,omitempty"`
	BranchDeleted   bool   `json:"branchDeleted,omitempty"`
	PRClosed        bool   `json:"prClosed,omitempty"`
	// Note is done, with a caveat ("worktree kept: ...").
	Note string `json:"note,omitempty"`
}

// RunWorkspaceAction is one request as the API returns it (Run.WorkspaceAction).
type RunWorkspaceAction struct {
	ID          string `json:"id"`
	RunID       string `json:"runId"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	Code        string `json:"code,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Note        string `json:"note,omitempty"`
	HeadSHA     string `json:"headSha,omitempty"`
	Base        string `json:"base,omitempty"`
	MergeCommit string `json:"mergeCommit,omitempty"`
	// RequestedBy is the display name of who asked.
	RequestedBy string `json:"requestedBy"`
	CreatedAt   string `json:"createdAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
}

// WorkspaceMergeBody is POST /api/runs/:id/workspace/merge.
type WorkspaceMergeBody struct {
	HeadSHA string `json:"headSha"`
}

// Files tab refusals (RunWorkspaceFiles.Blocked).
const (
	WorkspaceFilesNotOwner   = "not_owner"
	WorkspaceFilesOffline    = "offline"
	WorkspaceFilesBrowseOff  = "browse_off"
	WorkspaceFilesOutdated   = "outdated"
	WorkspaceFilesNoWorktree = "no_worktree"
)

// Why the live info is missing (RunWorkspaceView.InfoError).
const (
	WorkspaceInfoOffline  = "offline"
	WorkspaceInfoOutdated = "outdated"
	WorkspaceInfoTimeout  = "timeout"
	WorkspaceInfoNotOwner = "not_owner"
)

// RunWorkspaceFiles says whether the Files tab may browse the worktree.
type RunWorkspaceFiles struct {
	Allowed bool   `json:"allowed"`
	Blocked string `json:"blocked"`
}

// RunWorkspaceMergeGate is how the run would merge: through its PR ("pr"),
// on the runner ("local"), or not now ("" with Blocked).
type RunWorkspaceMergeGate struct {
	Mode    string `json:"mode"`
	Blocked string `json:"blocked"`
}

// RunWorkspaceDiscardGate says whether Discard is offered.
type RunWorkspaceDiscardGate struct {
	Allowed bool   `json:"allowed"`
	Blocked string `json:"blocked"`
}

// RunWorkspaceView is GET /api/runs/:id/workspace: the panel's header and
// what it may offer. Info is fetched live from the runner, for the owner
// only; a runner that is away leaves it nil with InfoError.
type RunWorkspaceView struct {
	RunID        string                  `json:"runId"`
	Owner        bool                    `json:"owner"`
	RunnerOnline bool                    `json:"runnerOnline"`
	Files        RunWorkspaceFiles       `json:"files"`
	Merge        RunWorkspaceMergeGate   `json:"merge"`
	Discard      RunWorkspaceDiscardGate `json:"discard"`
	Info         *RunWorkspaceInfo       `json:"info"`
	InfoError    string                  `json:"infoError"`
}

// Realtime: none new; runs-changed and run-notify carry it.
