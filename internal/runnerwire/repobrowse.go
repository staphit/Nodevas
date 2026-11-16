package runnerwire

import "time"

// Read-only repo browsing for the Explorer. The hub asks a person's own
// runner to list, read or show git state of one of its registered repos;
// the runner answers from disk. The hub never execs anything: git runs on
// the runner, at the registered repo's root, hardened against repo config.
//
//	hub    → runner  repo_request {repo}       (one op, one path)
//	runner → hub     repo_done    {repoDone}   (the answer, or a coded error)

// Message types and the runner capability.
const (
	TypeRepoRequest = "repo_request"
	TypeRepoDone    = "repo_done"
	// CapRepoBrowse: the runner answers repo_request (on unless the owner
	// turned it off with `nodevas runner set repoBrowse off`).
	CapRepoBrowse = "repo-browse"
)

// Repo browse operations.
const (
	RepoOpList   = "list"
	RepoOpRead   = "read"
	RepoOpStatus = "git_status"
	RepoOpLog    = "git_log"
	RepoOpDiff   = "git_diff"
)

// Run-scoped ops (agent workspace): the same answers as list, read and
// git_status, rooted at one run's worktree (<dataDir>/worktrees/<runId>)
// instead of the repo, and run_info, the worktree and branch as the runner
// sees them. Their own names, so a runner from before them refuses ("unknown
// repo operation") instead of browsing the repo root.
const (
	RepoOpRunList   = "run_list"   // -> Listing
	RepoOpRunRead   = "run_read"   // -> File
	RepoOpRunStatus = "run_status" // -> Status (uncommitted, worktree vs HEAD)
	RepoOpRunInfo   = "run_info"   // -> RunInfo
	// RepoOpRunPreview (easy start, result preview): a token for the
	// built-in runner's own loopback preview of the run's folder, so its
	// web page opens in a browser without reaching the hub's origin.
	// Built-in runner only (CapRunPreview, runner setting runPreview).
	RepoOpRunPreview = "run_preview" // -> Preview
)

// RunRepoOp reports whether op is run-scoped (RunID required).
func RunRepoOp(op string) bool {
	switch op {
	case RepoOpRunList, RepoOpRunRead, RepoOpRunStatus, RepoOpRunInfo, RepoOpRunPreview:
		return true
	}
	return false
}

// Limits on repo browsing.
const (
	MaxRepoEntries        = 1000
	MaxRepoFileBytes      = 512 << 10
	MaxRepoPatchBytes     = 256 << 10
	MaxRepoStatusEntries  = 2000
	MaxRepoLogEntries     = 100
	DefaultRepoLogEntries = 30
	MaxRepoPathBytes      = 1024
	MaxRepoDoneBytes      = 2 << 20
)

// RepoDone error codes.
const (
	RepoErrNotFound = "not_found"
	RepoErrBadPath  = "bad_path"
	RepoErrHidden   = "hidden"
	RepoErrOff      = "off"
	RepoErrRefused  = "refused"
	RepoErrGit      = "git"
	RepoErrBusy     = "busy"
	// RepoErrNoWorktree: the run's worktree is gone (or never existed).
	RepoErrNoWorktree = "no_worktree"
	// RepoErrPreviewOff: run_preview on a runner that is not built in, or
	// whose owner turned runPreview off.
	RepoErrPreviewOff = "preview_off"
)

// RepoRequest is one browse call. AccountID is who asks; the runner refuses
// anyone but its owner.
type RepoRequest struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	RepoID    string `json:"repoId"`
	Op        string `json:"op"`
	Path      string `json:"path,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	// Run-scoped ops only: the run, its node or, for a global run, its
	// branch slug (to name the run's own branch), the branch the hub
	// recorded and where a local merge would go.
	RunID  string `json:"runId,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	Slug   string `json:"slug,omitempty"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
}

// RepoEntry is one directory entry. Type is file, dir or symlink; Escape
// marks a symlink that points outside the repo (never traversed or read).
type RepoEntry struct {
	Name string `json:"name"`
	// Type is file, dir, symlink, or other (a FIFO, socket or device: never
	// read).
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Escape bool   `json:"escape,omitempty"`
}

// RepoListing is a directory's entries, directories first.
type RepoListing struct {
	Path      string      `json:"path"`
	Entries   []RepoEntry `json:"entries"`
	Hidden    int         `json:"hidden"`
	Truncated bool        `json:"truncated"`
}

// RepoFile is a file's text, or Binary with no content.
type RepoFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	Content   string `json:"content"`
}

// RepoStatusEntry is one changed path; Mark is M, A, D, R or ?.
type RepoStatusEntry struct {
	Path string `json:"path"`
	Mark string `json:"mark"`
}

// RepoGitStatus is the worktree's state against HEAD.
type RepoGitStatus struct {
	Branch    string            `json:"branch"`
	Head      string            `json:"head"`
	Entries   []RepoStatusEntry `json:"entries"`
	Hidden    int               `json:"hidden"`
	Truncated bool              `json:"truncated"`
}

// RepoCommit is one log entry.
type RepoCommit struct {
	SHA     string `json:"sha"`
	At      string `json:"at"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// RepoDiff is the worktree against HEAD (Ref "") or one commit's patch.
type RepoDiff struct {
	Ref       string `json:"ref"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
	Hidden    int    `json:"hidden"`
}

// RepoDone answers a RepoRequest: one of the results, or Code and Error.
type RepoDone struct {
	ID      string            `json:"id"`
	Error   string            `json:"error,omitempty"`
	Code    string            `json:"code,omitempty"`
	Listing *RepoListing      `json:"listing,omitempty"`
	File    *RepoFile         `json:"file,omitempty"`
	Status  *RepoGitStatus    `json:"status,omitempty"`
	Log     []RepoCommit      `json:"log,omitempty"`
	Diff    *RepoDiff         `json:"diff,omitempty"`
	RunInfo *RunWorkspaceInfo `json:"runInfo,omitempty"`
	Preview *RunPreview       `json:"preview,omitempty"`
}

// RunPreview answers run_preview. Web: the folder holds a page to open
// (Entry, relative to it), served at http://localhost:<Port>/p/<Token>/<Entry>
// until ExpiresAt; otherwise Port and Token are empty and the web opens the
// folder (Path, the owner's machine path) instead.
type RunPreview struct {
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	Entry     string    `json:"entry,omitempty"`
	Web       bool      `json:"web"`
	Path      string    `json:"path,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Where a local merge into a run's base would happen
// (RunWorkspaceInfo.BaseCheckout).
const (
	BaseCheckoutRepo      = "repo"      // checked out in the registered repo; see BaseClean
	BaseCheckoutElsewhere = "elsewhere" // checked out in another worktree: refused
	BaseCheckoutNone      = "none"      // checked out nowhere: a temporary worktree
	BaseCheckoutMissing   = "missing"   // no local branch
)

// UncommittedUnknown is RunWorkspaceInfo.Uncommitted for a worktree whose
// status the runner could not read (git failed or took too long).
const UncommittedUnknown = -1

// RunWorkspaceInfo is a run's worktree and branch as its runner sees them now.
type RunWorkspaceInfo struct {
	// Exists: the worktree directory is there and is the run's own.
	Exists bool `json:"exists"`
	// Path is the owner's machine path, for orientation only.
	Path string `json:"path,omitempty"`
	// Branch is the worktree's HEAD branch, "" when detached or gone.
	Branch string `json:"branch"`
	Head   string `json:"head,omitempty"`
	// OwnBranch: Branch is the branch this run made (agent/<node>-<run8>).
	OwnBranch bool `json:"ownBranch"`
	// BranchExists: refs/heads/<branch> is in the repo.
	BranchExists bool   `json:"branchExists"`
	BranchHead   string `json:"branchHead,omitempty"`
	// Uncommitted counts tracked and untracked changes in the worktree;
	// UncommittedUnknown when the runner could not read them.
	Uncommitted int `json:"uncommitted"`
	// Running: the runner still holds the run.
	Running bool `json:"running"`
	// Kept: listed in kept-worktrees.json.
	Kept         bool   `json:"kept"`
	BaseCheckout string `json:"baseCheckout"`
	BaseClean    bool   `json:"baseClean"`
	// WatchedPR is the PR URL the runner watches for the run.
	WatchedPR string `json:"watchedPr,omitempty"`
}

// ValidRepoRef reports whether ref may be a git_diff ref: "" (the worktree
// against HEAD) or a full 40- or 64-hex commit id.
func ValidRepoRef(ref string) bool {
	if ref == "" {
		return true
	}
	if len(ref) != 40 && len(ref) != 64 {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
