package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"nodevas/internal/agentsetup"
	"nodevas/internal/mcp"
)

// setupAgentCmd wires `nodevas mcp` and the `nodevas hook` handlers into the
// coding agents on this machine (Claude Code, Codex, Gemini CLI), or with
// --remove takes them out again. It edits the agents' own config files, so it
// backs each one up once before the first change and leaves everything that is
// not nodevas's exactly as it was.
func setupAgentCmd(args []string) {
	fs := flag.NewFlagSet("setup-agent", flag.ExitOnError)
	agentList := fs.String("agents", "",
		"comma-separated agents to wire: claude, codex, gemini; empty means every one installed here")
	remove := fs.Bool("remove", false, "take nodevas out of the agents' configs instead")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing anything")
	server := fs.String("server", mcp.DefaultServer, "the Nodevas server the agents work through")
	projectName := fs.String("project", "", "the project in that server's workspace; empty means whichever one it has open")
	actor := fs.String("actor", "",
		"the attribution every agent acts under; empty gives each its own, mcp:<agent>")
	agentRole := fs.String("agent-role", "worker", "the write-permission class: worker or orchestrator")
	_ = fs.Parse(args)

	role := strings.TrimSpace(*agentRole)
	if role != "worker" && role != "orchestrator" {
		fmt.Fprintf(os.Stderr,
			"nodevas setup-agent: --agent-role %q is not a role this server knows: use \"worker\" or \"orchestrator\"\n",
			*agentRole)
		os.Exit(2)
	}

	env, err := agentsetup.SystemEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nodevas setup-agent: %v\n", err)
		os.Exit(1)
	}
	var agents []string
	if strings.TrimSpace(*agentList) == "" {
		agents = agentsetup.Detect(env)
		if len(agents) == 0 {
			fmt.Fprintln(os.Stderr,
				"nodevas setup-agent: found no Claude Code, Codex or Gemini CLI here; name one with --agents")
			os.Exit(1)
		}
	} else if agents, err = agentsetup.ParseAgents(*agentList); err != nil {
		fmt.Fprintf(os.Stderr, "nodevas setup-agent: %v\n", err)
		os.Exit(2)
	}

	reports := agentsetup.Apply(env, agentsetup.Options{
		Agents:    agents,
		Remove:    *remove,
		DryRun:    *dryRun,
		Server:    strings.TrimSpace(*server),
		Project:   strings.TrimSpace(*projectName),
		Actor:     strings.TrimSpace(*actor),
		AgentRole: role,
	})
	failed := false
	for _, r := range reports {
		fmt.Println(agentsetup.Label(r.Agent))
		for _, l := range r.Lines {
			fmt.Println("  " + l)
		}
		for _, n := range r.Notes {
			fmt.Println("  note: " + n)
		}
		if r.Err != nil {
			failed = true
			fmt.Printf("  error: %v\n", r.Err)
		}
	}
	if *dryRun {
		fmt.Println("(dry run: nothing was written)")
	}
	if failed {
		os.Exit(1)
	}
}
