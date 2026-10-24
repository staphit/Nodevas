package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"nodevas/internal/mcp"
)

// mcpServe runs the MCP server that lets an agent work the board.
//
// Note what it does not take: a --project *directory*. This process never opens
// a workspace. The workspace lock is exclusive, so a second process holding the
// files directly would lock the person out of their own editor — which is the
// opposite of the point. --project names a project inside the workspace the
// running server already has open.
func mcpServe(args []string) {
	fs, f := newMCPFlags(flag.ExitOnError)
	_ = fs.Parse(mcpArgsFor(args, os.Getenv(runMCPEnv)))
	server, projectName, actor, agentRole := f.server, f.projectName, f.actor, f.agentRole
	sessionName, model, chromaURL, ragPython := f.sessionName, f.model, f.chromaURL, f.ragPython

	// Every diagnostic goes to stderr, without exception. stdout carries the
	// JSON-RPC stream, and one stray line on it ends the session with an error
	// that names neither the line nor this program.
	name := strings.TrimSpace(*actor)
	if name == "" {
		name = mcp.DefaultActor
	}

	// Refused here, before the transport starts: a bad role sent to the server
	// would fail every single call with a 401, which reads as a broken server
	// rather than a mistyped flag.
	role := strings.TrimSpace(*agentRole)
	switch role {
	case "worker", "orchestrator":
	default:
		fmt.Fprintf(os.Stderr,
			"nodevas mcp: --agent-role %q is not a role this server knows: use \"worker\" or \"orchestrator\"\n",
			*agentRole)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := mcp.Serve(ctx, mcp.Options{
		Server:      *server,
		Project:     strings.TrimSpace(*projectName),
		Actor:       name,
		SessionName: strings.TrimSpace(*sessionName),
		AgentRole:   role,
		Model:       strings.TrimSpace(*model),
		// A runner names its runs' sidecars run:<agent> (internal/runner
		// mcpArgs). A run was dispatched to its repo on purpose, so the
		// folder exclusions, which are about agents a person starts by
		// hand, do not apply to it.
		IgnoreExclusion: strings.HasPrefix(name, "run:"),
		ChromaURL:       *chromaURL,
		RAGPython:       *ragPython,
		Stderr:          os.Stderr,
	})
	if err != nil && ctx.Err() == nil && !clientWentAway(err) {
		fmt.Fprintf(os.Stderr, "nodevas mcp: %v\n", err)
		os.Exit(1)
	}
}

// runMCPEnv carries a runner's `nodevas mcp` command line for one run, as a
// JSON array of strings: "mcp", then its flags.
//
// Inside a run, an agent may start a `nodevas mcp` other than the run's own:
// one its global config names, which the Antigravity CLI prefers to the run's
// entry of the same name. With this set, that one serves the run instead of
// whatever its own command line says.
const runMCPEnv = "NODEVAS_RUN_MCP"

// mcpArgsFor is the command line to serve: args, or the run's from runMCP
// when that holds one whose flags all parse.
func mcpArgsFor(args []string, runMCP string) []string {
	override, ok := runMCPArgs(runMCP)
	if !ok {
		return args
	}
	probe, _ := newMCPFlags(flag.ContinueOnError)
	probe.SetOutput(io.Discard)
	if probe.Parse(override) != nil || probe.NArg() > 0 {
		return args
	}
	return override
}

// runMCPArgs reads a runMCPEnv value: the flags after "mcp", or false when it
// is not a JSON array of strings that starts with "mcp".
func runMCPArgs(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	var words []string
	if err := json.Unmarshal([]byte(value), &words); err != nil || len(words) == 0 || words[0] != "mcp" {
		return nil, false
	}
	return words[1:], true
}

// mcpFlags are `nodevas mcp`'s flags.
type mcpFlags struct {
	server, projectName, actor, agentRole, sessionName, model, chromaURL, ragPython *string
}

func newMCPFlags(handling flag.ErrorHandling) (*flag.FlagSet, mcpFlags) {
	fs := flag.NewFlagSet("mcp", handling)
	return fs, mcpFlags{
		server: fs.String("server", mcp.DefaultServer,
			"the running Nodevas server to work through; must be on this machine"),
		projectName: fs.String("project", "",
			"which project in that server's workspace to act on; empty means whichever one it has open"),
		actor: fs.String("actor", mcp.DefaultActor,
			"the name recorded against everything this agent changes; attribution, not authentication"),
		agentRole: fs.String("agent-role", "worker",
			"the write-permission class this agent acts as: worker or orchestrator"),
		sessionName: fs.String("session-name", "",
			"the name other agents address this session by (send_message to); empty derives one from the agent and repo"),
		model: fs.String("model", "",
			"the model this agent runs on, shown on the roster; empty reads NODEVAS_MODEL"),
		chromaURL: fs.String("chroma-url", "", "enable document RAG using this local ChromaDB v2 server; requires --project"),
		ragPython: fs.String("rag-python", "python", "Python executable with chromadb installed for local MiniLM embeddings"),
	}
}

// clientWentAway reports whether the session ended because the client closed
// the pipe, which is how every session ends.
//
// The transport reports it as an error, and treating it as one meant exiting
// non-zero every single time a client detached — so an operator reading logs
// would see a crash on every normal shutdown and eventually stop reading them.
func clientWentAway(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "EOF") ||
		strings.Contains(message, "file already closed")
}
