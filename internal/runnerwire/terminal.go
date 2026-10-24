package runnerwire

// Interactive runs (W2.5). The agent CLI runs as its normal TUI inside a
// pseudo-terminal on the runner (ConPTY on Windows), and the bytes travel over
// the runner's connect socket to the hub, which relays them to browsers
// attached to the run's terminal socket. Same worktree, branch, PR pipeline
// and run record as a headless run; only how the agent starts and where its
// status comes from differ (hooks, plus OSC 7501 parsed from the output).
//
//	runner → hub      term_out    {runId, term:{seq, data}}
//	hub    → runner   term_in     {runId, term:{data}}           keystrokes
//	hub    → runner   term_resize {runId, term:{cols, rows}}
//	runner → hub      events      as headless, from hooks / OSC 7501
//
// The browser side is GET /api/runs/:id/terminal (WebSocket): binary frames
// are output (server → browser) and input (browser → server); text frames are
// JSON TermControl messages.

// Run modes.
const (
	ModeHeadless    = ""
	ModeInteractive = "interactive"
)

// CapInteractive: the runner can run agents interactively in a PTY.
const CapInteractive = "interactive"

// Message types.
const (
	TypeTermOut    = "term_out"
	TypeTermIn     = "term_in"
	TypeTermResize = "term_resize"
)

// TermChunk is Envelope.Term. Data is raw terminal bytes (base64 in JSON).
type TermChunk struct {
	// Seq numbers output chunks per run, from 1; the hub drops repeats.
	Seq  int64  `json:"seq,omitempty"`
	Data []byte `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// Limits.
const (
	// MaxTermChunk caps one output or input chunk.
	MaxTermChunk = 64 << 10
	// TermScrollbackBytes is how much recent output the hub keeps per run, so
	// a browser that attaches late (or reconnects) sees the screen.
	TermScrollbackBytes = 512 << 10
	// MaxTermCols / MaxTermRows bound a resize.
	MaxTermCols = 500
	MaxTermRows = 200
)

// TermControl is a text frame on the browser terminal socket.
//
//	browser → hub  {"type":"resize","cols":120,"rows":40}
//	hub → browser  {"type":"status","status":"running"}   run status changes
//	hub → browser  {"type":"readonly"}                     this viewer may watch, not type
type TermControl struct {
	Type   string `json:"type"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Status string `json:"status,omitempty"`
}

// OSC 7501 (Program Status Protocol, as used by Orca): the runner scans PTY
// output for ESC ] 7501 ; <payload> BEL (or ESC \) and turns a recognisable
// payload into a run event, as a fallback where hooks report nothing. The
// payload format is best effort: a JSON object with a "state" field, or a
// bare state word.
const OSC7501 = 7501
