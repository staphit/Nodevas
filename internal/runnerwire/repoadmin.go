package runnerwire

// Repo admin (single-user): the owner adds, removes and sets up repos of the
// built-in runner from the app, instead of `nodevas runner repo add`. The hub
// relays one op at a time and waits at most RepoAdminTimeoutSeconds.
//
//	hub    → runner  repo_admin      {repoAdmin}
//	runner → hub     hello (the new repos, first, when they changed)
//	runner → hub     repo_admin_done {repoAdminDone}
//
// The runner decides: its owner only, the built-in runner only (a classic
// runner answers RepoAdminErrOff: a team member adds repos on their own PC),
// and every add re-inspects the folder and re-checks the sensitive-folder
// guard on both the folder and its git top level.
const (
	TypeRepoAdmin     = "repo_admin"      // hub → runner
	TypeRepoAdminDone = "repo_admin_done" // runner → hub (after a new hello when repos changed)
	// CapRepoAdmin: the runner takes repo_admin (built-in runner only).
	CapRepoAdmin = "repo-admin"

	// RepoAdminRequest.Op values.
	RepoAdminInspect = "inspect"
	RepoAdminAdd     = "add"
	RepoAdminInitAdd = "init_add"
	RepoAdminRemove  = "remove"
	RepoAdminSetTest = "set_test"

	// RepoAdminTimeoutSeconds is how long the hub waits for repo_admin_done.
	RepoAdminTimeoutSeconds = 30
	// RepoAdminInitTimeoutSeconds is how long it waits for an init_add: git
	// saves up to MaxInitFiles files as the first version, which takes
	// minutes on a slow disk. The runner gives up on the init before this.
	RepoAdminInitTimeoutSeconds = 5 * 60

	// RepoAdminDone.Code values; "" means done.
	RepoAdminErrRefused     = "refused" // not the owner
	RepoAdminErrOff         = "off"     // not the built-in runner
	RepoAdminErrSensitive   = "sensitive"
	RepoAdminErrNotFound    = "not_found"
	RepoAdminErrNotDir      = "not_dir"
	RepoAdminErrNotRepo     = "not_repo"
	RepoAdminErrAlready     = "already"
	RepoAdminErrTooMany     = "too_many"
	RepoAdminErrTooBig      = "too_big"
	RepoAdminErrGitMissing  = "git_missing"
	RepoAdminErrGit         = "git_failed"
	RepoAdminErrPlanChanged = "plan_changed"
	RepoAdminErrBusy        = "busy"
	// RepoAdminErrBadRequest: an unknown op, a missing path or repo, or a
	// test command or timeout out of range.
	RepoAdminErrBadRequest = "bad_request"

	// RepoInspect.State values.
	RepoStateRepo       = "repo"
	RepoStateInsideRepo = "inside_repo"
	RepoStateNotRepo    = "not_repo"
	RepoStateRefused    = "refused"

	// RepoInspect.Refused values.
	RepoRefusedHome       = "home"
	RepoRefusedRoot       = "root"
	RepoRefusedSecret     = "secret"
	RepoRefusedSystem     = "system"
	RepoRefusedNodevas    = "nodevas"
	RepoRefusedUnreadable = "unreadable"

	// Limits on making a folder a git project (init_add).
	MaxInitFiles = 20000
	MaxInitBytes = 1 << 30

	// MaxTestCommandBytes caps RepoAdminRequest.TestCommand;
	// MaxTestTimeoutMinutes caps its TestTimeoutMinutes.
	MaxTestCommandBytes   = 2048
	MaxTestTimeoutMinutes = 180
	// MaxRepoAdminError caps RepoAdminDone.Error.
	MaxRepoAdminError = 1024
)

// RepoAdminRequest is Envelope.RepoAdmin. AccountID is who asks; the runner
// refuses anyone but its owner.
type RepoAdminRequest struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Op        string `json:"op"`
	// Path is the folder (inspect, add, init_add), on the runner's machine.
	Path string `json:"path,omitempty"`
	// RepoID names the repo (remove, set_test).
	RepoID string `json:"repoId,omitempty"`
	// TestCommand is the repo's test command (set_test); "" clears it.
	TestCommand        string `json:"testCommand,omitempty"`
	TestTimeoutMinutes int    `json:"testTimeoutMinutes,omitempty"`
	// InitPlan is the InitPlan.Hash the user confirmed (init_add).
	InitPlan string `json:"initPlan,omitempty"`
}

// InitPlan is what making a folder a git project would do: how many files
// the first version saves and what it leaves out.
type InitPlan struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Ignored lists what the first version leaves out (gitignore patterns
	// that match something in the folder).
	Ignored []string `json:"ignored"`
	// WritesGitignore: the folder has no .gitignore; a default one is added.
	WritesGitignore bool `json:"writesGitignore"`
	// TooBig: past MaxInitFiles or MaxInitBytes, or too slow to count; the
	// runner refuses to init it.
	TooBig bool `json:"tooBig"`
	// Hash identifies this plan; init_add must name it.
	Hash string `json:"hash"`
}

// RepoInspect is what the runner found at a folder.
type RepoInspect struct {
	// Path is the folder as resolved; Top its git top level, when it has one.
	Path string `json:"path"`
	Top  string `json:"top,omitempty"`
	// State is RepoStateRepo, RepoStateInsideRepo, RepoStateNotRepo or
	// RepoStateRefused.
	State string `json:"state"`
	// Refused says why (RepoRefused*), with State RepoStateRefused.
	Refused string `json:"refused,omitempty"`
	// Already: the repo is already added (Repo is it).
	Already bool  `json:"already,omitempty"`
	Repo    *Repo `json:"repo,omitempty"`
	// Init is the plan for a folder that is not a git project yet.
	Init *InitPlan `json:"init,omitempty"`
}

// RepoAdminDone is Envelope.RepoAdminDone.
type RepoAdminDone struct {
	ID   string `json:"id"`
	Code string `json:"code,omitempty"`
	// Error is English, at most MaxRepoAdminError.
	Error   string       `json:"error,omitempty"`
	Inspect *RepoInspect `json:"inspect,omitempty"`
	// Repo is the repo added or changed.
	Repo *Repo `json:"repo,omitempty"`
}

// HTTP bodies (hub, single-user only).

// RepoInspectBody is the body of POST /api/runners/:id/repos/inspect.
type RepoInspectBody struct {
	Path string `json:"path"`
}

// RepoAddBody is the body of POST /api/runners/:id/repos.
type RepoAddBody struct {
	Path     string `json:"path"`
	InitPlan string `json:"initPlan,omitempty"`
}

// RepoAddResponse answers POST /api/runners/:id/repos.
type RepoAddResponse struct {
	Repo   Repo   `json:"repo"`
	Runner Runner `json:"runner"`
}

// RepoTestBody is the body of PUT /api/runners/:id/repos/:repoId/test.
type RepoTestBody struct {
	Command        string `json:"command"`
	TimeoutMinutes int    `json:"timeoutMinutes,omitempty"`
}

// RepoRunnerResponse answers DELETE /api/runners/:id/repos/:repoId and PUT
// …/test: the runner after its new hello.
type RepoRunnerResponse struct {
	Runner Runner `json:"runner"`
}
