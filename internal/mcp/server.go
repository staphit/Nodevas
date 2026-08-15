package mcp

import (
	"context"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is what this server reports to a client. It is the MCP surface's own
// version, not the application's: a client cares which tools it is talking to.
const Version = "v1"

// Options configure a `nodevas mcp` process.
type Options struct {
	Server  string
	Project string
	Actor   string
	// AgentRole is the write-permission class this process declares on every
	// request: "worker" or "orchestrator". Empty presents as a human session.
	// Nodes whose write_access outranks it refuse this agent's writes.
	AgentRole string
	// Stderr receives every diagnostic. Nothing may write to stdout but the
	// JSON-RPC transport: one stray line there and the client's parser gives up
	// on the session, usually with an error that names neither the line nor
	// this program.
	Stderr io.Writer
}

// DefaultActor names an agent when the operator did not.
//
// The prefix is not decoration. Everything this process writes lands in the
// `by` field of the timeline and the audit trail, next to entries made by
// people, and a reader deciding whether to trust a change needs to see which
// kind of thing made it. An agent must not be able to pass as a person by
// accident, so the fallback says what it is.
const DefaultActor = "mcp:agent"

// Serve runs the MCP server on stdin/stdout until the context ends or the
// client disconnects.
func Serve(ctx context.Context, opts Options) error {
	server, err := NewServer(ctx, opts)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// NewServer builds the MCP server without binding it to a transport, which is
// what lets the tools be exercised against a real Nodevas server in a test
// rather than only through a subprocess.
func NewServer(ctx context.Context, opts Options) (*mcp.Server, error) {
	client, err := NewClient(ClientOptions{
		Server:    opts.Server,
		Project:   opts.Project,
		Actor:     opts.Actor,
		AgentRole: opts.AgentRole,
	})
	if err != nil {
		return nil, err
	}
	// Fail here rather than at the first tool call. A transport error surfacing
	// mid-conversation tells the model nothing it can act on; this says what to
	// start.
	if err := client.Probe(ctx); err != nil {
		return nil, err
	}
	if opts.Stderr != nil {
		fmt.Fprintf(opts.Stderr, "nodevas mcp: %s, project %q, acting as %q, role %s\n",
			client.BaseURL(), displayProject(client.Project()), client.Actor(),
			displayRole(client.AgentRole()))
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "nodevas",
		Version: Version,
	}, &mcp.ServerOptions{
		Instructions: instructions(client.Project()),
	})
	registerReadTools(server, client)
	registerWriteTools(server, client)
	registerAuthoringTools(server, client)
	registerResources(server, client)
	registerPrompt(server, client)
	return server, nil
}

func displayProject(name string) string {
	if name == "" {
		return "(the server's active project)"
	}
	return name
}

func displayRole(role string) string {
	if role == "" {
		return "(none: presents as a human session)"
	}
	return role
}

// instructions is what the client shows the model about this server as a whole,
// so the per-tool descriptions do not each have to repeat the loop.
func instructions(project string) string {
	scope := "the project this server was started with"
	if project != "" {
		scope = fmt.Sprintf("the %q project", project)
	}
	return fmt.Sprintf(`Nodevas is a visual board of work: nodes are tasks, wires between them are dependencies.

Everything here acts on %s. A person may be looking at this board while you work; your changes appear on their screen as you make them.

The loop:
  1. get_ready_tasks — what is actionable now. It excludes anything whose prerequisites are unfinished or whose stated condition is false, so if it is empty the answer is not "make something up".
  2. claim_task — take it before doing anything. Another agent may be reading the same queue; claiming is what decides which of you does the work.
  3. get_node — read the full ticket.
  4. Do the work.
  5. set_node_status — done, failed or skipped, with a note saying what actually happened. If you could not make progress, release_task gives it back instead.
  Then repeat from 1.

Two rules worth stating plainly:

An empty queue with tasks still waiting means people, not you, are the blockers. Say so rather than picking up blocked work.

Never report a result you did not achieve. The note goes into a timeline people read to reconstruct what happened, and "failed" with an honest reason is far more useful than "done".`, scope)
}

// readOnly marks a tool that cannot change anything, which is what lets a
// client offer it without asking a person first.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true}
}
