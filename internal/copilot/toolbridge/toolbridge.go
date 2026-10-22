// Package toolbridge hands the copilot the same tools an MCP agent gets,
// without a second process and without a second implementation.
//
// It builds the `nodevas mcp` server in-process (internal/mcp), connects to it
// over an in-memory MCP transport, and routes the MCP client's HTTP calls
// straight into the server's own http.Handler. Every tool call therefore
// passes the whole middleware chain — authentication as the signed-in person,
// write_access, visitor read-only, abuse limits, the audit trail — and its
// writes reach the realtime hub exactly like an agent's or a browser's do.
package toolbridge

import (
	"context"
	"encoding/json"
	"net/http"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/identity"
)

// Kind is how much a tool can change, which is what the permission modes
// decide on.
type Kind string

const (
	// KindRead cannot change anything (MCP ReadOnlyHint).
	KindRead Kind = "read"
	// KindWrite changes project content in a way history can undo:
	// create_node, link_nodes, update_node_meta, set_node_status, claim/release.
	KindWrite Kind = "write"
	// KindRisky replaces content wholesale (update_node_body) or is otherwise
	// singled out; acceptEdits mode still asks before running it.
	KindRisky Kind = "risky"
)

// Tool is a provider tool plus its permission class.
type Tool struct {
	provider.Tool
	Kind  Kind   `json:"kind"`
	Title string `json:"title,omitempty"`
}

// Result is what a tool call produced, as text for the model.
type Result struct {
	Output  string
	IsError bool
}

// Bridge is one open in-process MCP session, scoped to one person and one
// project for the length of one copilot turn.
type Bridge interface {
	Tools() []Tool
	// Call runs a tool. A tool-level failure (bad argument, conflict,
	// permission denied) is a Result with IsError, which the model reads and
	// can recover from; err is reserved for the bridge itself breaking.
	Call(ctx context.Context, name string, input json.RawMessage) (Result, error)
	Close() error
}

// Options say whom the bridge acts as and against what.
type Options struct {
	// Handler is the server's own router. Requests are served in-process.
	Handler http.Handler
	// Host and Cookie are copied from the person's request to the copilot
	// endpoint: Host passes the server's Host allow-list, Cookie carries the
	// session the request authenticates as. Empty Cookie on a loopback-only
	// server is fine; that server authenticates nobody.
	Host   string
	Cookie string
	// Project pins every tool to one project; empty is the active project.
	Project string
	// Actor is written into `by`/`owner` fields, e.g. "copilot:alice".
	Actor string
	// AgentRole is the agent class the in-process requests carry, enforced
	// against node write_access. Defaults to orchestrator.
	AgentRole identity.AgentClass
	// ChromaURL and RAGPython enable the document retrieval tools, as for
	// `nodevas mcp --chroma-url`.
	ChromaURL string
	RAGPython string
}

// Open is implemented in bridge.go:
//
//	func Open(ctx context.Context, opts Options) (Bridge, error)
