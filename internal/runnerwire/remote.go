package runnerwire

// Remote dispatch (W6). Until now every offer waited for someone at the
// runner to say yes (or the runner auto-accepted everything). W6 lets a runner
// auto-accept only what its owner allowlisted, and only from a dispatcher who
// recently proved it is them again (step-up: a fresh email passcode), so a
// stolen browser session alone cannot run agents on the owner's machine.

// AutoAcceptPolicy is the runner's auto-accept allowlist (runner.json), as
// reported in hello (Info.AutoAccept) so the web can say what will happen.
// The runner enforces it; the hub only shows it.
type AutoAcceptPolicy struct {
	Enabled bool `json:"enabled"`
	// RequireStepUp: auto-accept only jobs whose dispatcher stepped up
	// (Job.SteppedUp). Default true; turning it off is the owner's explicit
	// choice and the runner warns about it.
	RequireStepUp bool `json:"requireStepUp"`
	// Repos and Agents limit auto-accept to these repo IDs / agent kinds;
	// empty means none (not all).
	Repos  []string `json:"repos,omitempty"`
	Agents []string `json:"agents,omitempty"`
	// Modes: "" (headless) and/or "interactive". Empty means headless only.
	Modes []string `json:"modes,omitempty"`
	// AllowShell: auto-accept runs even when the agent's permission setting
	// lets it run any shell command (Codex danger-full-access, OpenCode or
	// agy full, agy auto, Gemini yolo, Claude bypassPermissions or Bash
	// allowed). Default false: such offers are asked about at the terminal.
	AllowShell bool `json:"allowShell,omitempty"`
	// AllowRemoteTyping: the dispatcher may type into an auto-accepted
	// interactive run's terminal. Default false: view only.
	AllowRemoteTyping bool `json:"allowRemoteTyping,omitempty"`
	// AllowVerify: auto-accept verify jobs (the repo's own test command, no
	// agent) for the listed repos. Default false: asked at the terminal.
	// RED PHASE STUB: not consulted yet.
	AllowVerify bool `json:"allowVerify,omitempty"`
}

// StepUpWindowSeconds is how long a step-up lasts by default; the server's
// setting may shorten or lengthen it.
const StepUpWindowSeconds = 10 * 60

// StepUpStatus is GET /api/auth/step-up: whether the caller's session is
// stepped up and until when. Loopback servers (no accounts) report Required
// false: the OS account is the boundary there.
type StepUpStatus struct {
	Required bool   `json:"required"`
	Active   bool   `json:"active"`
	Until    string `json:"until,omitempty"`
}

// External sessions (W6): an agent the owner runs in their own terminal (not
// through a runner) reports, through `nodevas hook`, which files its edits
// touched while it held a node, and at Stop a diff snapshot of its working
// tree. The hub stores them on the node, alongside runs.

// SessionChange is one external session's recorded changes for a node.
type SessionChange struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	NodeID    string `json:"nodeId"`
	SessionID string `json:"sessionId"`
	// Session is the roster name (e.g. visual-editor-claude), Agent the CLI.
	Session string `json:"session"`
	Agent   string `json:"agent"`
	ActorID string `json:"actorId"`
	Repo    string `json:"repo,omitempty"`
	Branch  string `json:"branch,omitempty"`
	// Files the session's Edit/Write tools touched (repo-relative when known).
	Files []string `json:"files"`
	// Snapshot is the diff at Stop, when one was taken (RunChanges shape:
	// file stats + patch ≤ MaxPatchBytes).
	Snapshot  *RunChanges `json:"snapshot,omitempty"`
	CreatedAt string      `json:"createdAt"`
	UpdatedAt string      `json:"updatedAt"`
}

// SessionChangesResponse is GET /api/nodes/:id/session-changes.
type SessionChangesResponse struct {
	Changes []SessionChange `json:"changes"`
}

// MaxSessionFiles caps SessionChange.Files.
const MaxSessionFiles = 500
