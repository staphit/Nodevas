package runnerwire

// Changes and pull requests (W3). After the agent exits, the runner commits
// whatever it changed in the run's worktree, pushes the branch and opens a pull
// request with the owner's own gh login, then keeps watching that PR (state and
// CI) until it is merged or closed, when it removes the worktree. The hub only
// records what the runner reports.
//
//	runner → hub  changes {runId, changes}   before finish; at most once per run
//	runner → hub  pr      {runId, pr}        when the PR opens and whenever its
//	                                         state or CI changes, also after finish

// Message types.
const (
	TypeChanges = "changes"
	TypePR      = "pr"
)

// File change statuses.
const (
	FileAdded    = "added"
	FileModified = "modified"
	FileDeleted  = "deleted"
	FileRenamed  = "renamed"
)

// PR states.
const (
	PROpen   = "open"
	PRMerged = "merged"
	PRClosed = "closed"
)

// CI statuses, rolled up over the PR's checks.
const (
	CINone    = ""        // no checks reported (yet)
	CIPending = "pending" // at least one check still running, none failed
	CISuccess = "success"
	CIFailure = "failure"
)

// Limits.
const (
	// MaxPatchBytes caps RunChanges.Patch; a bigger diff keeps the file list
	// and counts and drops the patch (PatchTruncated).
	MaxPatchBytes = 1 << 20
	// MaxChangedFiles caps RunChanges.Files.
	MaxChangedFiles = 2000
)

// FileChange is one file in a run's commit.
type FileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// OldPath is set for a rename.
	OldPath   string `json:"oldPath,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Binary files have no line counts.
	Binary bool `json:"binary,omitempty"`
}

// RunChanges is what a run changed: `git diff <base>...<commit>`.
type RunChanges struct {
	// Commit is the run's commit SHA; "" when the agent changed nothing.
	Commit    string       `json:"commit"`
	Base      string       `json:"base"`
	Files     []FileChange `json:"files"`
	Additions int          `json:"additions"`
	Deletions int          `json:"deletions"`
	// Patch is the unified diff, at most MaxPatchBytes.
	Patch          string `json:"patch,omitempty"`
	PatchTruncated bool   `json:"patchTruncated,omitempty"`
}

// PRInfo is a run's pull request as the runner last saw it.
type PRInfo struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	State  string `json:"state"`
	Draft  bool   `json:"draft,omitempty"`
	CI     string `json:"ci"`
	// HeadSHA is the commit the CI status belongs to.
	HeadSHA string `json:"headSha,omitempty"`
	// Error says why no PR could be opened (no origin remote, gh missing or
	// not signed in, push refused). URL is then "".
	Error string `json:"error,omitempty"`
}

// Run fields filled from W3 (added to Run in runs.go): Commit, FilesChanged,
// Additions, Deletions, PRNumber, PRState, PRError — alongside the existing
// PRURL and CIStatus.

// RunChangesResponse is the body of GET /api/runs/:id/changes. Changes is nil
// for a run that reported none (yet).
type RunChangesResponse struct {
	Changes *RunChanges `json:"changes"`
}
