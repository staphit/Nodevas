package runnerwire

// Runner settings from the hub (agent workspace, phase 2): the runner's
// owner changes how many runs it takes at once from the web. Synchronous:
// the hub waits at most SettingsTimeoutSeconds for the answer.
//
//	hub    → runner  settings_request {settings}
//	runner → hub     hello (the new info, first)  then  settings_done {settingsDone}
//
// The runner decides: only its owner may ask, its owner may turn remote
// changes off (runner setting remoteSettings), and it never goes past its
// own maxConcurrentCeiling. It sends settings_done only in answer, so an
// older hub never sees one.
const (
	TypeSettingsRequest = "settings_request"
	TypeSettingsDone    = "settings_done"
	// CapRemoteSettings: the runner's owner lets the hub change its settings
	// (runner setting remoteSettings, on unless turned off).
	CapRemoteSettings = "remote-settings"

	// SettingsDone.Code values; "" means applied.
	SettingsErrRefused = "refused"      // AccountID is not the runner's owner
	SettingsErrOff     = "settings_off" // owner turned remoteSettings off
	SettingsErrRange   = "out_of_range" // past 1..maxConcurrentCeiling
	// SettingsErrLocalOnly: a classic runner refuses any field but
	// MaxConcurrent; a team member changes their PC's settings on that PC.
	SettingsErrLocalOnly = "local_only"
	// SettingsErrInvalid: an unknown agent kind or permission level, or an
	// agent path the runner refused (SettingsDone.Refused says why).
	SettingsErrInvalid = "invalid"

	// SettingsDone.Refused values: why an agent path (AgentPath) was not
	// taken. The web shows a plain sentence for each.
	AgentPathRefusedName       = "name"        // the file is not named like the agent's program
	AgentPathRefusedPlace      = "place"       // inside a registered folder, the runner's data or the temp dir
	AgentPathRefusedNotProgram = "not_program" // not a regular executable file
	AgentPathRefusedMissing    = "missing"     // no such file
	AgentPathRefusedNoAnswer   = "no_answer"   // `--version` did not answer
	AgentPathRefusedInvalid    = "invalid"     // not an absolute path, too long, or a NUL
	// MaxAgentPathBytes caps one agent path.
	MaxAgentPathBytes = 1024

	// Permission levels (SettingsRequest.Permission, LocalSettings.Permission):
	// what an agent may do without asking, mapped to each CLI's own mode on
	// the runner. PermCustom is a mode that maps to none (shown, never set).
	PermRead   = "read"
	PermEdit   = "edit"
	PermFull   = "full"
	PermCustom = "custom"

	// DefaultMaxConcurrentCeiling is how high the hub may set maxConcurrent
	// unless the owner set maxConcurrentCeiling on the runner.
	DefaultMaxConcurrentCeiling = 8
	// MaxConcurrentLimit is the hard bound on maxConcurrent and its ceiling.
	MaxConcurrentLimit = 16
	// SettingsTimeoutSeconds is how long the hub waits for settings_done.
	SettingsTimeoutSeconds = 10
	// SettingsRedetectTimeoutSeconds is how long it waits for one that also
	// looks for the agent CLIs again (Redetect): asking each CLI for its
	// version and models takes seconds apiece.
	SettingsRedetectTimeoutSeconds = 60
	// MaxSettingsError caps SettingsDone.Error.
	MaxSettingsError = 1024
)

// SettingsRequest is Envelope.Settings. AccountID is who asks; the runner
// refuses anyone but its owner. A nil field is left as it is. Every field
// but MaxConcurrent is for the built-in runner only (SettingsErrLocalOnly
// from any other).
type SettingsRequest struct {
	ID            string `json:"id"`
	AccountID     string `json:"accountId"`
	MaxConcurrent *int   `json:"maxConcurrent,omitempty"`
	// Permission sets agent kind → PermRead|PermEdit|PermFull.
	Permission  map[string]string `json:"permission,omitempty"`
	AskEveryRun *bool             `json:"askEveryRun,omitempty"`
	// ShellWithoutAsking sets agent kind → whether an automatic run of a
	// full-access agent starts without asking.
	ShellWithoutAsking map[string]bool `json:"shellWithoutAsking,omitempty"`
	AutoPR             *bool           `json:"autoPR,omitempty"`
	PRDraft            *bool           `json:"prDraft,omitempty"`
	// AgentPath sets agent kind → the absolute path of the program to run
	// for it, at most one entry; "" goes back to finding it on PATH. The
	// runner checks the file and asks it for --version before it keeps it.
	AgentPath map[string]string `json:"agentPath,omitempty"`
	// PermissionRelay turns "ask me each time an agent wants to run a
	// command" on or off (runner setting permissionRelay).
	PermissionRelay *bool `json:"permissionRelay,omitempty"`
	// Redetect looks for the agent CLIs again (a new hello follows).
	Redetect bool `json:"redetect,omitempty"`
}

// LocalSettings is Info.Local: the built-in runner's settings as the app
// shows them ("this computer").
type LocalSettings struct {
	// Permission is agent kind → PermRead|PermEdit|PermFull|PermCustom;
	// PermissionDetail the exact CLI setting, e.g. "codexSandbox=workspace-write".
	Permission       map[string]string `json:"permission"`
	PermissionDetail map[string]string `json:"permissionDetail"`
	AskEveryRun      bool              `json:"askEveryRun"`
	// ShellWithoutAsking lists the agent kinds whose automatic runs start
	// without asking although they may run any command.
	ShellWithoutAsking []string `json:"shellWithoutAsking"`
	AutoPR             bool     `json:"autoPR"`
	PRDraft            bool     `json:"prDraft"`
	// ConfigPath is the runner's settings file, for 進階.
	ConfigPath string `json:"configPath"`
	// AgentPaths is agent kind → the program the owner added for it, the
	// path as picked (cleaned; a link is checked again where it points at
	// every use). Only here, for the owner; never in Info.Agents.
	AgentPaths map[string]string `json:"agentPaths,omitempty"`
}

// SettingsDone is Envelope.SettingsDone.
type SettingsDone struct {
	ID   string `json:"id"`
	Code string `json:"code,omitempty"`
	// Error is English, at most MaxSettingsError.
	Error string `json:"error,omitempty"`
	// Refused is why an agent path was not taken (AgentPathRefused*), with
	// Code SettingsErrInvalid.
	Refused string `json:"refused,omitempty"`
	// MaxConcurrent is the limit in effect now.
	MaxConcurrent int `json:"maxConcurrent"`
	// Persisted: written to the runner's config file, so it outlives a
	// restart.
	Persisted bool `json:"persisted"`
}

// RunnerSettingsBody is the body of PUT /api/runners/:id/settings. Every
// field but MaxConcurrent is for the built-in runner only; they mean what
// SettingsRequest's do.
type RunnerSettingsBody struct {
	MaxConcurrent      *int              `json:"maxConcurrent"`
	Permission         map[string]string `json:"permission,omitempty"`
	AskEveryRun        *bool             `json:"askEveryRun,omitempty"`
	ShellWithoutAsking map[string]bool   `json:"shellWithoutAsking,omitempty"`
	AutoPR             *bool             `json:"autoPR,omitempty"`
	PRDraft            *bool             `json:"prDraft,omitempty"`
	AgentPath          map[string]string `json:"agentPath,omitempty"`
	PermissionRelay    *bool             `json:"permissionRelay,omitempty"`
	Redetect           bool              `json:"redetect,omitempty"`
}

// RunnerSettingsResponse answers PUT /api/runners/:id/settings: the runner
// after its new hello.
type RunnerSettingsResponse struct {
	Runner    Runner `json:"runner"`
	Persisted bool   `json:"persisted"`
}
