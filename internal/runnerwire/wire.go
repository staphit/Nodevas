// Package runnerwire is the contract between the hub (nodevas serve) and a
// runner (nodevas runner): the JSON both ends exchange, and nothing else.
//
// It holds types only, so the hub can import it without importing anything
// that starts processes. Starting processes is the runner's job alone; see
// docs/nodevas-workstation-plan.md, "Hub 永遠不執行程式".
package runnerwire

// ConnectPath is the runner's WebSocket endpoint on the hub. The runner
// authenticates with "Authorization: Bearer <runner token>"; behind
// Cloudflare Access it also sends the service token headers below.
const ConnectPath = "/api/runner/connect"

// ProxyPathPrefix is where a runner's loopback MCP proxy forwards an agent's
// board calls: /api/runner/proxy/<path> on the hub serves /api/<path> as the
// runner's owner, marked as an agent, for an allowlist of agent routes only.
const ProxyPathPrefix = "/api/runner/proxy/"

// TokenPrefix marks a runner token, so one pasted in the wrong place is
// recognisable and secret scanners can find it.
const TokenPrefix = "nvr_"

// Cloudflare Access service-token headers. The edge checks them and forwards
// the request with its own JWT; Nodevas never sees or checks the secret.
const (
	AccessClientIDHeader     = "CF-Access-Client-Id"
	AccessClientSecretHeader = "CF-Access-Client-Secret"
)

// Message types on the connect socket.
const (
	// Runner → hub. The first message on every connection; sent again
	// whenever the runner's agents, models or repos change.
	TypeHello = "hello"
	// Runner → hub, every PingInterval. The hub answers with TypePong.
	TypePing = "ping"
	// Hub → runner, in answer to the first hello.
	TypeWelcome = "welcome"
	TypePong    = "pong"
	// Hub → runner, before the hub closes the socket on purpose (token
	// revoked, replaced by a newer connection). Reason says which; a runner
	// whose token was revoked stops instead of reconnecting.
	TypeBye = "bye"
)

// PingIntervalSeconds is how often a runner pings; the hub treats a runner silent
// for three intervals as gone.
const PingIntervalSeconds = 20

// Bye reasons.
const (
	ByeRevoked  = "revoked"
	ByeReplaced = "replaced"
)

// Envelope is every message on the connect socket.
type Envelope struct {
	Type string `json:"type"`
	// Hello carries the runner's description (TypeHello).
	Hello *Info `json:"hello,omitempty"`
	// Welcome answers the first hello (TypeWelcome).
	Welcome *Welcome `json:"welcome,omitempty"`
	// Reason explains a TypeBye, TypeDecline or TypeCancel.
	Reason string `json:"reason,omitempty"`
	// Active lists, with a hello, the run IDs this runner is still executing.
	// The hub fails any run it thinks is running there that is not listed.
	Active []string `json:"active,omitempty"`

	// Runs; see runs.go.
	Job    *Job       `json:"job,omitempty"`
	RunID  string     `json:"runId,omitempty"`
	Accept *Accept    `json:"accept,omitempty"`
	Events []RunEvent `json:"events,omitempty"`
	Result *RunResult `json:"result,omitempty"`

	// Changes and pull requests; see changes.go.
	Changes *RunChanges `json:"changes,omitempty"`
	PR      *PRInfo     `json:"pr,omitempty"`

	// Remote merge; see merge.go.
	Merge       *MergeJob     `json:"merge,omitempty"`
	MergeResult *MergeOutcome `json:"mergeResult,omitempty"`
}

// Welcome tells a runner who the hub thinks it is.
type Welcome struct {
	RunnerID string `json:"runnerId"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
}

// Info is what a runner reports about itself. The hub stores it as given and
// shows it; it never acts on a path or command in it.
type Info struct {
	Version  string  `json:"version"`
	Hostname string  `json:"hostname"`
	OS       string  `json:"os"`
	Arch     string  `json:"arch"`
	Agents   []Agent `json:"agents"`
	Repos    []Repo  `json:"repos"`
	// Capabilities the runner supports: CapHeadless from W1 on.
	Capabilities []string `json:"capabilities,omitempty"`
	// MaxConcurrent is how many runs the runner takes at once; the hub keeps
	// further jobs queued rather than offering them.
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
}

// CapHeadless: the runner takes offers and runs agents non-interactively.
const CapHeadless = "headless"

// Agent is one agent CLI on the runner's machine.
type Agent struct {
	// Kind is "claude", "codex" or "gemini".
	Kind      string `json:"kind"`
	Installed bool   `json:"installed"`
	// Version is the CLI's own --version output, first line, trimmed.
	Version string  `json:"version,omitempty"`
	Models  []Model `json:"models"`
	// Error says why detection failed, when Installed is false for a reason
	// other than "not on PATH".
	Error string `json:"error,omitempty"`
}

// Model is one model a run can ask an agent for.
type Model struct {
	// ID is what the CLI's model flag takes. "default" means "pass no model
	// flag", i.e. whatever the CLI itself defaults to.
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// Repo is a local repository the runner's owner allowed runs in. Jobs name a
// repo by ID; only the runner maps the ID to a path.
type Repo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Path is shown to the owner for orientation only.
	Path string `json:"path,omitempty"`
	// Remote is the origin URL, when there is one.
	Remote string `json:"remote,omitempty"`
}

// Limits on what a hello may carry, enforced by the hub.
const (
	MaxHelloBytes = 64 << 10
	MaxAgents     = 16
	MaxModels     = 64
	MaxRepos      = 256
)

// Runner is a runner as the HTTP API (GET /api/runners) returns it.
type Runner struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"ownerId"`
	OwnerName string `json:"ownerName"`
	CreatedAt string `json:"createdAt"`
	// LastSeenAt is RFC3339, or "" for a runner that never connected.
	LastSeenAt string `json:"lastSeenAt"`
	Online     bool   `json:"online"`
	// Info is the last hello, or nil for a runner that never connected.
	Info *Info `json:"info,omitempty"`
}

// CreateRequest is the body of POST /api/runners.
type CreateRequest struct {
	Name string `json:"name"`
}

// CreateResponse is returned once, on creation: Token is never shown again.
type CreateResponse struct {
	Runner Runner `json:"runner"`
	Token  string `json:"token"`
}

// ListResponse is the body of GET /api/runners.
type ListResponse struct {
	Runners []Runner `json:"runners"`
}

// ChangedEvent is the realtime event the hub broadcasts when any runner is
// created, revoked, connects, disconnects or sends a new hello.
const ChangedEvent = "runners-changed"
