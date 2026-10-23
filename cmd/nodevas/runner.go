package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"nodevas/internal/runner"
)

// runnerCommands is how `nodevas runner` looks at this machine; tests swap it.
var runnerCommands runner.CommandRunner = runner.SystemCommands{}

// runnerCmd is `nodevas runner`: the daemon that connects this machine to a
// hub, and the commands that set it up. Secrets arrive on stdin, never as
// flags, because a command line is readable by every process here.
func runnerCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	in := bufio.NewReader(stdin)
	var err error
	switch sub {
	case "start":
		err = runnerStart(args, stdin, stderr)
	case "login":
		err = runnerLogin(args, in, stdout, stderr)
	case "logout":
		err = runnerLogout(args, stdout)
	case "status":
		err = runnerStatus(args, stdout)
	case "repo":
		err = runnerRepo(args, in, stdout, stderr)
	case "set":
		err = runnerSet(args, stdout, stderr)
	case "help", "-h", "--help":
		runnerUsage(stdout)
		return 0
	default:
		runnerUsage(stderr)
		return 2
	}
	if errors.Is(err, errUsage) {
		runnerUsage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "nodevas runner %s: %v\n", sub, err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func runnerUsage(w io.Writer) {
	fmt.Fprint(w, `usage: nodevas runner [start]                 connect this machine to its hub and stay connected
       nodevas runner login --hub <url> [--access-client-id <id>] [--name <label>]
                                             token on stdin's first line; with --access-client-id,
                                             the Access service token's secret on the second
       nodevas runner logout                 forget the token and the Access secret
       nodevas runner status                 settings (secrets masked) and the agents found here
       nodevas runner repo add <path> [--name <n>] [--yes]
                                             allow runs in this git repository (asks to confirm)
       nodevas runner repo list
       nodevas runner repo remove <id|name>
       nodevas runner set <key> <value>      change a run setting ("default" resets it):
                                             maxConcurrent, runTimeoutMinutes, dataDir, autoAccept;
                                             Claude Code: permissionMode (acceptEdits|auto|
                                             bypassPermissions|manual|dontAsk|plan), allowedTools
                                             (one per argument, or comma-separated; "none" clears);
                                             Codex: codexSandbox (read-only|workspace-write|
                                             danger-full-access), codexIgnoreUserConfig (true|false);
                                             Gemini CLI: geminiApprovalMode (default|auto_edit|yolo);
                                             pull requests: autoPR (true|false), prDraft (true|false),
                                             prLabels (one per argument, or comma-separated; {model}
                                             and {agent} are filled in; "none" for no labels),
                                             prPollSeconds (30 or more);
                                             remote merge: remoteMerge (true|false), mergeMethod
                                             (squash|merge|rebase), deleteBranchOnMerge (true|false)
       every subcommand takes --config <file> (default: <user config dir>/nodevas/runner.json)
`)
}

func newRunnerFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("runner "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", "", "settings file")
	return fs, config
}

// parseInterspersed lets flags follow positional arguments, as in
// `repo add <path> --name x`; the standard flag package stops at the first.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, errUsage
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func runnerConfigPath(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue, nil
	}
	return runner.DefaultConfigPath()
}

func loadRunnerConfig(flagValue string) (string, *runner.Config, error) {
	path, err := runnerConfigPath(flagValue)
	if err != nil {
		return "", nil, err
	}
	cfg, err := runner.LoadConfig(path)
	return path, cfg, err
}

func runnerStart(args []string, stdin io.Reader, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("start")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	if cfg.Hub == "" || cfg.Token == "" {
		return fmt.Errorf("not logged in (%s): run nodevas runner login --hub <url> first", path)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Offers are accepted here, at this terminal, by whoever started the
	// runner. Without a terminal (a service, a pipe) nobody can say yes, and
	// offers are declined at once unless autoAccept is on.
	accepter := &runner.TerminalAccepter{In: stdin, Out: stderr, Interactive: stdinIsTerminal(stdin)}
	client := &runner.Client{Config: cfg, Commands: runnerCommands, Log: stderr, Accepter: accepter}
	return client.Run(ctx)
}

func runnerLogin(args []string, in *bufio.Reader, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("login")
	hub := fs.String("hub", "", "hub URL")
	accessID := fs.String("access-client-id", "", "Cloudflare Access service token client ID")
	name := fs.String("name", "", "label for log lines")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	hubURL, err := runner.ValidateHub(*hub)
	if err != nil {
		return err
	}
	if isTerminal(os.Stdin) {
		fmt.Fprint(stderr, "runner token: ")
	}
	token, err := readSecretLine(in)
	if err != nil {
		return fmt.Errorf("reading the runner token from stdin: %w", err)
	}
	if err := runner.ValidateToken(token); err != nil {
		return err
	}
	var secret string
	if id := strings.TrimSpace(*accessID); id != "" {
		if isTerminal(os.Stdin) {
			fmt.Fprint(stderr, "Access client secret: ")
		}
		if secret, err = readSecretLine(in); err != nil || secret == "" {
			return errors.New("--access-client-id needs the Access client secret on stdin's second line")
		}
	}

	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	cfg.Hub = hubURL
	cfg.Token = token
	cfg.AccessClientID = strings.TrimSpace(*accessID)
	cfg.AccessClientSecret = secret
	if strings.TrimSpace(*name) != "" {
		cfg.Name = strings.TrimSpace(*name)
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "saved %s\nstart the runner with: nodevas runner\n", path)
	return nil
}

func runnerLogout(args []string, stdout io.Writer) error {
	fs, configFlag := newRunnerFlags("logout")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	cfg.Token, cfg.AccessClientSecret = "", ""
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed the runner token and Access secret from %s\n", path)
	return nil
}

func runnerStatus(args []string, stdout io.Writer) error {
	fs, configFlag := newRunnerFlags("status")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "config:  %s\n", path)
	fmt.Fprintf(stdout, "hub:     %s\n", orNone(cfg.Hub))
	if cfg.Name != "" {
		fmt.Fprintf(stdout, "name:    %s\n", cfg.Name)
	}
	fmt.Fprintf(stdout, "token:   %s\n", masked(cfg.Token))
	if cfg.AccessClientID != "" {
		fmt.Fprintf(stdout, "access:  client id %s, secret %s\n", cfg.AccessClientID, masked(cfg.AccessClientSecret))
	}
	fmt.Fprintf(stdout, "version: %s\n", runner.Version())
	fmt.Fprintln(stdout, "agents:")
	for _, agent := range runner.Detect(context.Background(), runnerCommands, cfg) {
		state := "not found"
		switch {
		case agent.Installed:
			state = agent.Version
		case agent.Error != "":
			state = "error: " + agent.Error
		}
		models := make([]string, 0, len(agent.Models))
		for _, m := range agent.Models {
			models = append(models, m.ID)
		}
		fmt.Fprintf(stdout, "  %-7s %s  [models: %s]\n", agent.Kind, state, strings.Join(models, ", "))
	}
	printRepos(stdout, cfg)
	printRunSettings(stdout, cfg)
	return nil
}

func printRunSettings(w io.Writer, cfg *runner.Config) {
	dataDir, err := cfg.RunDataDir()
	if err != nil {
		dataDir = "(unknown: " + err.Error() + ")"
	}
	tools := "(none)"
	if len(cfg.AllowedTools) > 0 {
		tools = strings.Join(cfg.AllowedTools, ", ")
	}
	accept := "asked at the runner's terminal"
	if cfg.AutoAccept {
		accept = "AUTO-ACCEPT: anyone who can sign in to the hub as you can run agents here"
	}
	codexConfig := "runs load your Codex config.toml: its model, MCP servers and hooks"
	if cfg.CodexIgnoreUserConfig {
		codexConfig = "runs skip your Codex config.toml; only its sign-in is used"
	}
	setting := func(key string, value any) { fmt.Fprintf(w, "  %-22s %v\n", key, value) }
	fmt.Fprintln(w, "runs:")
	setting("maxConcurrent", cfg.MaxConcurrentRuns())
	setting("runTimeoutMinutes", cfg.RunTimeoutMins())
	setting("dataDir", dataDir)
	setting("autoAccept", fmt.Sprintf("%t (%s)", cfg.AutoAccept, accept))
	fmt.Fprintln(w, "claude runs:")
	setting("permissionMode", cfg.ClaudePermissionMode())
	setting("allowedTools", tools)
	fmt.Fprintln(w, "codex runs:")
	setting("codexSandbox", cfg.CodexSandboxMode())
	setting("codexIgnoreUserConfig", fmt.Sprintf("%t (%s)", cfg.CodexIgnoreUserConfig, codexConfig))
	fmt.Fprintln(w, "gemini runs:")
	setting("geminiApprovalMode", cfg.GeminiApproval())
	labels := "(none)"
	if templates := cfg.PRLabelTemplates(); len(templates) > 0 {
		labels = strings.Join(templates, ", ")
	}
	autoPR := "commit, push the run's branch and open a PR with your gh login"
	if !cfg.AutoPROn() {
		autoPR = "commit only; the branch stays in its worktree"
	}
	fmt.Fprintln(w, "pull requests:")
	setting("autoPR", fmt.Sprintf("%t (%s)", cfg.AutoPROn(), autoPR))
	setting("prDraft", cfg.PRDraft)
	setting("prLabels", labels)
	setting("prPollSeconds", int(cfg.PRPollInterval().Seconds()))
	remote := "a merge approved on the hub is merged here with your gh login, once its CI is green"
	if !cfg.RemoteMergeOn() {
		remote = "approvals on the hub are refused; merge on GitHub yourself"
	}
	setting("remoteMerge", fmt.Sprintf("%t (%s)", cfg.RemoteMergeOn(), remote))
	setting("mergeMethod", cfg.MergeMethodOrDefault())
	setting("deleteBranchOnMerge", cfg.DeleteBranchOnMerge)
}

// runnerSet changes one run setting. A running runner reads its settings at
// start, so the change applies from the next start.
func runnerSet(args []string, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("set")
	rest, err := parseInterspersed(fs, args)
	if err != nil || len(rest) < 2 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	if err := cfg.Set(rest[0], rest[1:]); err != nil {
		return err
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	printRunSettings(stdout, cfg)
	if cfg.AutoAccept {
		fmt.Fprintln(stderr, runner.AutoAcceptWarning)
	}
	if cfg.PermissionMode == "bypassPermissions" {
		fmt.Fprintln(stderr, "note: bypassPermissions lets agents run any command on this machine without asking.")
	}
	if cfg.CodexSandbox == "danger-full-access" {
		fmt.Fprintln(stderr, "note: danger-full-access lets Codex runs run any command on this machine, outside any sandbox.")
	}
	if cfg.GeminiApprovalMode == "yolo" {
		fmt.Fprintln(stderr, "note: yolo lets Gemini runs run any command on this machine without asking.")
	}
	fmt.Fprintln(stdout, "saved; restart a running runner for it to take effect")
	return nil
}

func runnerRepo(args []string, in *bufio.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	sub, args := args[0], args[1:]
	fs, configFlag := newRunnerFlags("repo " + sub)
	name := fs.String("name", "", "display name")
	yes := fs.Bool("yes", false, "skip the confirmation")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	path, cfg, err := loadRunnerConfig(*configFlag)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		if len(rest) > 0 {
			return errUsage
		}
		printRepos(stdout, cfg)
		return nil
	case "add":
		if len(rest) != 1 {
			return errUsage
		}
		repo, err := runner.InspectRepo(context.Background(), runnerCommands, rest[0])
		if err != nil {
			return err
		}
		if strings.TrimSpace(*name) != "" {
			repo.Name = strings.TrimSpace(*name)
		}
		if !*yes {
			where := repo.Path
			if repo.Remote != "" {
				where += " (" + repo.Remote + ")"
			}
			fmt.Fprintf(stderr, "Allow agent runs dispatched from the hub to work in %s? [y/N]: ", where)
			answer, _ := in.ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				return errors.New("not added")
			}
		}
		if err := cfg.AddRepo(repo); err != nil {
			return err
		}
		if err := runner.SaveConfig(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "added %s as %q (id %s); a running runner reports it within 5 minutes, or restart it\n",
			repo.Path, repo.Name, repo.ID)
		return nil
	case "remove":
		if len(rest) != 1 {
			return errUsage
		}
		repo, err := cfg.RemoveRepo(rest[0])
		if err != nil {
			return err
		}
		if err := runner.SaveConfig(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed %s (id %s)\n", repo.Path, repo.ID)
		return nil
	}
	return errUsage
}

func printRepos(w io.Writer, cfg *runner.Config) {
	if len(cfg.Repos) == 0 {
		fmt.Fprintln(w, "repos:   none (add one with nodevas runner repo add <path>)")
		return
	}
	fmt.Fprintln(w, "repos:")
	for _, repo := range cfg.Repos {
		line := fmt.Sprintf("  %s  %-20s %s", repo.ID, repo.Name, repo.Path)
		if repo.Remote != "" {
			line += "  " + repo.Remote
		}
		fmt.Fprintln(w, line)
	}
}

// readSecretLine reads one line, the way a pipe or a paste delivers it.
func readSecretLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func masked(secret string) string {
	if secret == "" {
		return "(not set)"
	}
	return "(set)"
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// stdinIsTerminal reports whether r is a terminal someone can type answers
// into; anything that is not an *os.File (a test's reader) is not.
func stdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && isTerminal(f)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
