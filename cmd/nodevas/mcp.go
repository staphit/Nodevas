package main

import (
	"context"
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
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	server := fs.String("server", mcp.DefaultServer,
		"the running Nodevas server to work through; must be on this machine")
	projectName := fs.String("project", "",
		"which project in that server's workspace to act on; empty means whichever one it has open")
	actor := fs.String("actor", mcp.DefaultActor,
		"the name recorded against everything this agent changes; attribution, not authentication")
	_ = fs.Parse(args)

	// Every diagnostic goes to stderr, without exception. stdout carries the
	// JSON-RPC stream, and one stray line on it ends the session with an error
	// that names neither the line nor this program.
	name := strings.TrimSpace(*actor)
	if name == "" {
		name = mcp.DefaultActor
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := mcp.Serve(ctx, mcp.Options{
		Server:  *server,
		Project: strings.TrimSpace(*projectName),
		Actor:   name,
		Stderr:  os.Stderr,
	})
	if err != nil && ctx.Err() == nil && !clientWentAway(err) {
		fmt.Fprintf(os.Stderr, "nodevas mcp: %v\n", err)
		os.Exit(1)
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
