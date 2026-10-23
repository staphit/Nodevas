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
	"text/tabwriter"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/agentsetup"
	"nodevas/internal/mcp"
)

// networkConfigPath is where `nodevas agents exclude` keeps its list; tests
// swap it.
var networkConfigPath = agentsetup.NetworkConfigPath

// listenWait is how long one `agents listen` long poll waits before asking
// again; tests shorten it.
var listenWait = agents.MaxInboxWait

// agentsCmd is `nodevas agents`: the agent network from a terminal. It talks
// to the server the way `nodevas mcp` does, loopback only, but as a person:
// it declares no agent role, so what it sends is a human's message.
func agentsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		agentsUsage(stderr)
		return 2
	}
	sub, rest := args[0], args[1:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch sub {
	case "who":
		err = agentsWho(ctx, rest, stdout)
	case "send":
		err = agentsSend(ctx, rest, stdout)
	case "listen":
		err = agentsListen(ctx, rest, stdout, stderr)
	case "exclude":
		err = agentsExclude(rest, stdout)
	case "help", "-h", "--help":
		agentsUsage(stdout)
		return 0
	default:
		agentsUsage(stderr)
		return 2
	}
	if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
		agentsUsage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "nodevas agents %s: %v\n", sub, err)
		return 1
	}
	return 0
}

func agentsUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: nodevas agents who [--server URL] [--project P] [--all]")
	fmt.Fprintln(w, "       nodevas agents send --to <name|*> [--node ID] [--notify] [--file path]... <message…>")
	fmt.Fprintln(w, "       nodevas agents listen --as <session-name> [--server URL] [--project P]   (Ctrl-C to stop)")
	fmt.Fprintln(w, "       nodevas agents exclude add|remove|list [path]   (folders whose agent sessions stay off the network)")
}

// agentFlags are the flags every network subcommand shares.
func agentFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("agents "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server := fs.String("server", mcp.DefaultServer, "the running Nodevas server; must be on this machine")
	project := fs.String("project", "", "which project in that server's workspace; empty means the one it has open")
	return fs, server, project
}

func agentsClient(server, project string) (*mcp.Client, error) {
	// No actor and no agent role: on a loopback server that is the local
	// user, as a person.
	return mcp.NewClient(mcp.ClientOptions{
		Server:    server,
		Project:   strings.TrimSpace(project),
		SessionID: "cli",
		AgentKind: "human",
	})
}

func agentsWho(ctx context.Context, args []string, stdout io.Writer) error {
	fs, server, project := agentFlags("who")
	all := fs.Bool("all", false, "include sessions that went offline in the last day")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	client, err := agentsClient(*server, *project)
	if err != nil {
		return err
	}
	sessions, err := client.Roster(ctx, *all)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "no agent sessions")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tACTOR\tAGENT\tSTATE\tINBOUND\tMODEL\tREPO\tDOING")
	for _, s := range sessions {
		state := s.State
		if !s.Online {
			state = "offline"
		}
		repo := s.Repo
		if s.Branch != "" {
			repo += "@" + s.Branch
		}
		doing := s.Doing
		if s.NodeID != "" {
			doing = strings.TrimSpace("[" + s.NodeID + "] " + doing)
		}
		inbound := s.Inbound
		if inbound == "" {
			inbound = agents.InboundOpen
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Name, s.ActorName, s.Agent, state, inbound, dash(s.Model), dash(repo), dash(doing))
	}
	return tw.Flush()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func agentsSend(ctx context.Context, args []string, stdout io.Writer) error {
	fs, server, project := agentFlags("send")
	to := fs.String("to", "", "the session name to send to, or * for every online session")
	node := fs.String("node", "", "the board node the message is about")
	notify := fs.Bool("notify", false, "also pop the message up on the recipient's screen")
	var files []string
	fs.Func("file", "a text file to attach (repeatable; at most 10, 256 KB each)", func(v string) error {
		files = append(files, v)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	body := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if strings.TrimSpace(*to) == "" || (body == "" && len(files) == 0) {
		return errUsage
	}
	if len(files) > agents.MaxFiles {
		return fmt.Errorf("at most %d files per message", agents.MaxFiles)
	}
	// A person at a terminal picks the files, so any path they can read
	// will do; only the agent tool is held to its working directory.
	attached := make([]agents.FileInput, 0, len(files))
	for _, path := range files {
		file, err := mcp.ReadAttachment(path)
		if err != nil {
			return err
		}
		attached = append(attached, file)
	}
	client, err := agentsClient(*server, *project)
	if err != nil {
		return err
	}
	result, err := client.SendAsHuman(ctx, mcp.OutgoingMessage{
		To: *to, NodeID: *node, Body: body, Notify: *notify, Files: attached,
	})
	if err != nil {
		return err
	}
	for _, d := range result.Deliveries {
		fmt.Fprintf(stdout, "%s: %s (%s)\n", d.Recipient, d.Status, d.MessageID)
	}
	return nil
}

func agentsListen(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, server, project := agentFlags("listen")
	as := fs.String("as", "", "the session whose inbox to read")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	name := strings.TrimSpace(*as)
	if name == "" {
		return errUsage
	}
	client, err := agentsClient(*server, *project)
	if err != nil {
		return err
	}
	// The inbox is the session's: the server hands it only to the session's
	// own actor, so listen reads it as that actor. On a loopback server,
	// where every caller is the local user, that is attribution, not a
	// borrowed identity.
	sessions, err := client.Roster(ctx, true)
	if err != nil {
		return err
	}
	var session *agents.Session
	for i := range sessions {
		s := &sessions[i]
		if strings.EqualFold(s.Name, name) && (session == nil || (s.Online && !session.Online)) {
			session = s
		}
	}
	if session == nil {
		return fmt.Errorf("no session named %q (nodevas agents who --all lists them)", name)
	}
	fmt.Fprintf(stderr, "listening as %s (Ctrl-C to stop)\n", session.Address())
	for {
		msgs, err := client.WaitInbox(ctx, session.Name, session.ActorID, listenWait)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			fmt.Fprintf(stderr, "nodevas agents listen: %v; retrying\n", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, m := range msgs {
			printMessage(stdout, m)
		}
	}
}

func printMessage(w io.Writer, m agents.Message) {
	at := time.UnixMilli(m.CreatedAt).Local().Format("15:04:05")
	flags := ""
	if m.Notify {
		flags = " [notify]"
	}
	fmt.Fprintf(w, "[%s] %s -> %s (%s)%s\n%s\n", at, m.From, m.Recipient, m.ID, flags, m.Body)
	for _, f := range m.Files {
		fmt.Fprintf(w, "  file: %s (%d bytes)\n", f.Name, f.Size)
	}
}

func agentsExclude(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	path, err := networkConfigPath()
	if err != nil {
		return err
	}
	cfg, err := agentsetup.LoadNetworkConfig(path)
	if err != nil {
		return err
	}
	action, rest := args[0], args[1:]
	if action == "list" {
		if len(rest) != 0 {
			return errUsage
		}
		if len(cfg.ExcludeFolders) == 0 {
			fmt.Fprintln(stdout, "no folders excluded")
		}
		for _, f := range cfg.ExcludeFolders {
			fmt.Fprintln(stdout, f)
		}
		return nil
	}
	if len(rest) > 1 {
		return errUsage
	}
	// No path means the folder this is run from: `cd private && nodevas
	// agents exclude add`.
	target := "."
	if len(rest) == 1 {
		target = rest[0]
	}
	var folder string
	var changed bool
	switch action {
	case "add":
		folder, changed, err = cfg.AddExclude(target)
	case "remove":
		folder, changed, err = cfg.RemoveExclude(target)
	default:
		return errUsage
	}
	if err != nil {
		return err
	}
	if !changed {
		if action == "add" {
			fmt.Fprintf(stdout, "%s is already excluded\n", folder)
		} else {
			fmt.Fprintf(stdout, "%s was not excluded\n", folder)
		}
		return nil
	}
	if err := agentsetup.SaveNetworkConfig(path, cfg); err != nil {
		return err
	}
	if action == "add" {
		fmt.Fprintf(stdout, "excluded %s: agent sessions there stay off the network\n", folder)
	} else {
		fmt.Fprintf(stdout, "%s is back on the network\n", folder)
	}
	return nil
}
