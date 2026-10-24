package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"nodevas/internal/agenthook"
	"nodevas/internal/mcp"
)

// hookCmd runs `nodevas hook <agent> <event> [--server URL] [--project NAME]
// [--actor NAME]`, the command `nodevas setup-agent` wires into an agent's
// hook system. It reports the session to the running server and prints any
// waiting mail in the form the agent puts in front of its model.
//
// It always exits 0 and never writes errors to stdout: whatever the agent
// reads there goes to the model, and a hook that fails makes the agent show
// an error on every prompt. A Nodevas server that is not running is a normal
// state, not a fault worth interrupting anyone over.
func hookCmd(args []string) {
	out := ""
	defer func() {
		_ = recover()
		if out == "" && len(args) > 0 {
			out = agenthook.Fallback(args[0])
		}
		if out != "" {
			fmt.Fprintln(os.Stdout, out)
		}
	}()
	if len(args) < 2 {
		return
	}
	agent, event := args[0], args[1]

	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server := fs.String("server", mcp.DefaultServer, "the running Nodevas server")
	projectName := fs.String("project", "", "which project in that server's workspace")
	actor := fs.String("actor", mcp.DefaultActor, "the actor the agent's `nodevas mcp` runs as")
	_ = fs.Parse(args[2:])

	in := agenthook.ParseInput(agenthook.ReadStdin(os.Stdin, agenthook.StdinLimit))
	ctx, cancel := context.WithTimeout(context.Background(), agenthook.Timeout(event))
	defer cancel()
	out = agenthook.Run(ctx, agent, event, in, agenthook.Options{
		Server:  *server,
		Project: *projectName,
		Actor:   *actor,
	})
}
