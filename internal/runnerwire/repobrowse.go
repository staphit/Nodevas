package runnerwire

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
}

// RepoEntry is one directory entry. Type is file, dir or symlink; Escape
// marks a symlink that points outside the repo (never traversed or read).
type RepoEntry struct {
	Name   string `json:"name"`
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
	ID      string         `json:"id"`
	Error   string         `json:"error,omitempty"`
	Code    string         `json:"code,omitempty"`
	Listing *RepoListing   `json:"listing,omitempty"`
	File    *RepoFile      `json:"file,omitempty"`
	Status  *RepoGitStatus `json:"status,omitempty"`
	Log     []RepoCommit   `json:"log,omitempty"`
	Diff    *RepoDiff      `json:"diff,omitempty"`
}

// ValidRepoRef reports whether ref may be a git_diff ref: "" (the worktree
// against HEAD) or a full 40- or 64-hex commit id.
func ValidRepoRef(ref string) bool {
	// TODO(green): implement.
	return false
}
