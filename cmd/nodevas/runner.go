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
	"strconv"
	"strings"
	"syscall"
	"time"

	"nodevas/internal/agenthook"
	"nodevas/internal/buildinfo"
	"nodevas/internal/runner"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
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
		err = runnerLogout(args, stdout, stderr)
	case "status":
		err = runnerStatus(args, stdout, stderr)
	case "hub-version":
		err = runnerHubVersion(args, stdout, stderr)
	case "repo":
		err = runnerRepo(args, in, stdout, stderr)
	case "set":
		err = runnerSet(args, stdout, stderr)
	case "autoaccept", "auto-accept":
		err = runnerAutoAccept(args, stdout, stderr)
	case "copilot-key":
		err = runnerCopilotKey(args, in, stdout, stderr)
	case "advisor-key":
		err = runnerAdvisorKey(args, in, stdout, stderr)
	case "agy-gate":
		// Not for people: the PreToolUse hook of a headless Antigravity run
		// (adapter.AntigravityGate). It never fails: agy reads its stdout.
		fmt.Fprintln(stdout, adapter.AntigravityGate(agenthook.ReadStdin(stdin, agenthook.StdinLimit),
			len(args) > 0 && args[0] == adapter.AntigravityGateReadOnly))
		return 0
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
       nodevas runner login --link [--hub <url>] [--name <label>]
                                             a device link (Runners → Link a new device) on stdin's
                                             first line, prompted hidden at a terminal; never as an
                                             argument. It brings the hub and its Access service token
       nodevas runner logout                 forget the token and the Access secret
       nodevas runner status                 settings (secrets masked) and the agents found here
       nodevas runner hub-version            the commit the hub was built from (the installer uses it
                                             to fetch the matching runner binary from GitHub)
       nodevas runner repo add <path> [--name <n>] [--yes]
                                             allow runs in this git repository (asks to confirm)
       nodevas runner repo list
       nodevas runner repo remove <id|name>
       nodevas runner repo set <id|name> --test-command "go test ./..." [--test-timeout <minutes>]
                                             the repository's test command, for verify jobs (red
                                             before the work, green after); it stays on this
                                             machine, the hub only learns that one is set.
                                             --test-command "" clears it
       nodevas runner autoaccept on|off|show the offers that start without asking here:
       nodevas runner autoaccept step-up required|optional
       nodevas runner autoaccept repo add|remove <id|name|path>
       nodevas runner autoaccept agent add|remove <kind>
       nodevas runner autoaccept mode add|remove headless|interactive
       nodevas runner autoaccept allow-shell on|off
       nodevas runner autoaccept remote-typing on|off
       nodevas runner autoaccept verify on|off  take verify jobs (the repo's test command on code an
                                             agent wrote; no agent runs) unasked, for listed repos
                                             an offer is auto-accepted only when auto-accept is on,
                                             the dispatcher stepped up on the hub (unless optional),
                                             its repo, agent and mode are all listed, and its agent
                                             cannot run any shell command (unless allow-shell on);
                                             anything else is asked about at this terminal. A step-up
                                             does not protect you from a compromised hub.
                                             remote-typing on lets the dispatcher type into
                                             auto-accepted interactive runs (default: view only)
       nodevas runner set <key> <value>      change a run setting ("default" resets it):
                                             maxConcurrent, runTimeoutMinutes, reviewTimeoutMinutes
                                             (default 20), planTimeoutMinutes (default 30), dataDir, autoAccept
                                             (true = autoaccept on with the defaults: step-up required,
                                             every repo here, installed agents, headless; false = off),
                                             interactive (true|false, default true: take runs whose
                                             agent TUI the dispatcher watches, typing only if allowed;
                                             each still accepted here or by autoaccept);
                                             Claude Code: permissionMode (acceptEdits|auto|
                                             bypassPermissions|manual|dontAsk|plan), allowedTools
                                             (one per argument, or comma-separated; "none" clears);
                                             Codex: codexSandbox (read-only|workspace-write|
                                             danger-full-access), codexIgnoreUserConfig (true|false);
                                             Gemini CLI: geminiApprovalMode (default|auto_edit|yolo);
                                             OpenCode: opencodePermission (read-only|edit|full),
                                             opencodeProtocol (run|acp; acp is experimental);
                                             Antigravity CLI (agy): antigravityMode (plan|accept-edits|
                                             auto|full; full skips every permission);
                                             pull requests: autoPR (true|false), prDraft (true|false),
                                             prLabels (one per argument, or comma-separated; {model}
                                             and {agent} are filled in; "none" for no labels),
                                             prPollSeconds (30 or more);
                                             remote merge: remoteMerge (true|false, default false), mergeMethod
                                             (squash|merge|rebase), deleteBranchOnMerge (true|false);
                                             copilot: copilotProvider (anthropic|openai|none|off: this runner
                                             makes your voice copilot's model calls with a key kept here;
                                             with none, a local Ollama or LM Studio is used when found;
                                             off turns that off too),
                                             copilotModel, copilotBaseURL (https, or http to this machine
                                             or the local network);
                                             autopilot advisor: advisorProvider (anthropic|openai|systemone|
                                             none: a fast model here picks the agent and model for each
                                             autopilot task), advisorModel, advisorBaseURL (same rules as
                                             copilot). systemone is a System One endpoint: Laya on this PC
                                             (in a venv: pip install "laya[serve]==0.4.14", then
                                             LAYA_HOST=127.0.0.1 laya-serve; base URL defaults
                                             to http://127.0.0.1:8000) or TypeSafe's Jev (advisorBaseURL
                                             https://api.typesafe.ai, advisorModel jev-latest, advisor-key)
       nodevas runner copilot-key [--clear]  store the copilot's API key, read from stdin (never a flag);
                                             the hub never sees it (or set NODEVAS_COPILOT_API_KEY here)
       nodevas runner advisor-key [--clear]  store the advisor's API key the same way (or set
                                             NODEVAS_ADVISOR_API_KEY; with the copilot's provider, the
                                             copilot key serves both)
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

// loadRunnerConfig reads the settings, migrating (and saving) W1's boolean
// autoAccept on the way; its one-time warning goes to stderr.
func loadRunnerConfig(flagValue string, stderr io.Writer) (string, *runner.Config, error) {
	path, err := runnerConfigPath(flagValue)
	if err != nil {
		return "", nil, err
	}
	cfg, err := runner.LoadConfig(path)
	if err != nil {
		return path, nil, err
	}
	return path, cfg, migrateRunnerConfig(path, cfg, stderr)
}

func runnerStart(args []string, stdin io.Reader, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("start")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
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
	// offers the auto-accept policy does not cover are declined at once.
	accepter := &runner.TerminalAccepter{In: stdin, Out: stderr, Interactive: stdinIsTerminal(stdin)}
	client := &runner.Client{Config: cfg, Commands: runnerCommands, Log: stderr, Accepter: accepter}
	return client.Run(ctx)
}

func runnerLogin(args []string, in *bufio.Reader, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("login")
	hub := fs.String("hub", "", "hub URL")
	accessID := fs.String("access-client-id", "", "Cloudflare Access service token client ID")
	name := fs.String("name", "", "label for log lines")
	link := fs.Bool("link", false, "redeem a device link read from stdin")
	// Checked before parsing, so `--link=nvlink1.…` is refused for what it
	// is rather than as a malformed boolean.
	if linkInArgs(args) {
		return errLinkArgument
	}
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	if *link {
		if strings.TrimSpace(*accessID) != "" {
			return errors.New("--link brings the hub's Access service token with it; drop --access-client-id")
		}
		return runnerLoginLink(in, stdout, stderr, *configFlag, *hub, *name)
	}
	hubURL, err := runner.ValidateHub(*hub)
	if err != nil {
		return err
	}
	token, err := readHiddenLine(in, stderr, "runner token: ")
	if err != nil {
		return fmt.Errorf("reading the runner token from stdin: %w", err)
	}
	if err := runner.ValidateToken(token); err != nil {
		return err
	}
	var secret string
	if id := strings.TrimSpace(*accessID); id != "" {
		if secret, err = readHiddenLine(in, stderr, "Access client secret: "); err != nil || secret == "" {
			return errors.New("--access-client-id needs the Access client secret on stdin's second line")
		}
	}

	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
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

func runnerLogout(args []string, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("logout")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
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

func runnerStatus(args []string, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("status")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
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
	if commit := buildinfo.Commit(); commit != "" {
		fmt.Fprintf(stdout, "commit:  %s\n", commit)
	}
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
	codexConfig := "runs load your Codex config.toml: its model, MCP servers and hooks"
	if cfg.CodexIgnoreUserConfig {
		codexConfig = "runs skip your Codex config.toml; only its sign-in is used"
	}
	setting := func(key string, value any) { fmt.Fprintf(w, "  %-22s %v\n", key, value) }
	fmt.Fprintln(w, "runs:")
	setting("maxConcurrent", cfg.MaxConcurrentRuns())
	setting("runTimeoutMinutes", cfg.RunTimeoutMins())
	setting("reviewTimeoutMinutes", int(cfg.RunTimeoutFor(runnerwire.JobReview)/time.Minute))
	setting("planTimeoutMinutes", int(cfg.RunTimeoutFor(runnerwire.JobPlan)/time.Minute))
	setting("dataDir", dataDir)
	interactive := "interactive runs: the agent's TUI in a terminal here; the dispatcher watches, " +
		"and types only where you allowed it"
	if !cfg.InteractiveOn() {
		interactive = "interactive runs are declined; headless only"
	} else if cfg.Interactive == nil {
		interactive += " (the default)"
	}
	setting("interactive", fmt.Sprintf("%t (%s)", cfg.InteractiveOn(), interactive))
	repoBrowse := "the hub's Explorer may list and read your registered repos, read-only, secrets hidden"
	if !cfg.RepoBrowseOn() {
		repoBrowse = "repo browsing from the hub is refused"
	} else if cfg.RepoBrowse == nil {
		repoBrowse += " (the default)"
	}
	setting("repoBrowse", fmt.Sprintf("%t (%s)", cfg.RepoBrowseOn(), repoBrowse))
	fmt.Fprintln(w, "claude runs:")
	setting("permissionMode", cfg.ClaudePermissionMode())
	setting("allowedTools", tools)
	fmt.Fprintln(w, "codex runs:")
	setting("codexSandbox", cfg.CodexSandboxMode())
	setting("codexIgnoreUserConfig", fmt.Sprintf("%t (%s)", cfg.CodexIgnoreUserConfig, codexConfig))
	fmt.Fprintln(w, "gemini runs:")
	setting("geminiApprovalMode", cfg.GeminiApproval())
	fmt.Fprintln(w, "opencode runs:")
	setting("opencodePermission", cfg.OpenCodePermissionMode())
	setting("opencodeProtocol", cfg.OpenCodeProtocolOrDefault())
	fmt.Fprintln(w, "antigravity runs:")
	setting("antigravityMode", cfg.AntigravityModeOrDefault())
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
		remote = "approvals on the hub are refused; merge on GitHub yourself (the default)"
	}
	setting("remoteMerge", fmt.Sprintf("%t (%s)", cfg.RemoteMergeOn(), remote))
	setting("mergeMethod", cfg.MergeMethodOrDefault())
	setting("deleteBranchOnMerge", cfg.DeleteBranchOnMerge)
	printAutoAccept(w, cfg)
	printCopilot(w, cfg)
	printAdvisor(w, cfg)
}

// printAdvisor shows the autopilot advisor settings (W7); the key only as
// where it comes from.
func printAdvisor(w io.Writer, cfg *runner.Config) {
	setting := func(key string, value any) { fmt.Fprintf(w, "  %-22s %v\n", key, value) }
	fmt.Fprintln(w, "autopilot advisor:")
	if cfg.AdvisorProvider == "" {
		setting("advisorProvider", "none (autopilot picks agents by its rules)")
		return
	}
	info, _, reason := cfg.AdvisorLLM()
	setting("advisorProvider", cfg.AdvisorProvider)
	systemOne := cfg.AdvisorProvider == runnerwire.ProviderSystemOne
	model := cfg.AdvisorModel
	if model == "" {
		model = "(default)"
		if systemOne {
			model = "(the endpoint's own)"
		}
	}
	setting("advisorModel", model)
	base := cfg.AdvisorBaseURL
	if base == "" {
		base = "(the vendor's own)"
		if systemOne {
			base = runnerwire.DefaultSystemOneBaseURL + " (Laya's laya-serve)"
		}
	}
	setting("advisorBaseURL", base)
	key := "none"
	switch _, source := cfg.AdvisorKey(); source {
	case "stored":
		key = "stored in this file"
	case "environment":
		key = "from " + runner.AdvisorKeyEnv
	case "copilot":
		key = "the copilot's (same provider)"
	}
	setting("advisorKey", key)
	switch {
	case reason != "":
		setting("state", "off: "+reason)
	case systemOne:
		setting("state", fmt.Sprintf("on: autopilot asks the System One endpoint at %s which agent and model take each task "+
			"(policy advisor \"systemone\")", info.BaseURL))
	default:
		setting("state", fmt.Sprintf("on: autopilot asks %s/%s which agent and model take each task", info.Provider, info.Model))
	}
}

// runnerAdvisorKey stores (or with --clear forgets) the advisor's API key,
// read from stdin like the copilot's.
func runnerAdvisorKey(args []string, in *bufio.Reader, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("advisor-key")
	clear := fs.Bool("clear", false, "forget the stored key")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
	if err != nil {
		return err
	}
	if *clear {
		cfg.AdvisorAPIKey = ""
	} else {
		key, err := readHiddenLine(in, stderr, "advisor API key: ")
		if err != nil {
			return fmt.Errorf("reading the key from stdin: %w", err)
		}
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 4096 || strings.ContainsAny(key, " \t\r\n") {
			return errors.New("that is not an API key")
		}
		cfg.AdvisorAPIKey = key
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintln(stdout, "advisor key forgotten")
	} else {
		fmt.Fprintln(stdout, "advisor key saved")
	}
	if cfg.AdvisorProvider == "" {
		fmt.Fprintln(stdout, "set the provider too: nodevas runner set advisorProvider anthropic (or openai, or systemone)")
	}
	fmt.Fprintln(stdout, "restart a running runner for it to take effect")
	return nil
}

// printCopilot shows the copilot settings; the key only as where it comes from.
func printCopilot(w io.Writer, cfg *runner.Config) {
	setting := func(key string, value any) { fmt.Fprintf(w, "  %-22s %v\n", key, value) }
	fmt.Fprintln(w, "copilot:")
	if cfg.CopilotProvider == "" {
		setting("copilotProvider", "none (a local Ollama or LM Studio answers the copilot when one runs here)")
		return
	}
	if cfg.CopilotProvider == runner.CopilotProviderOff {
		setting("copilotProvider", "off (the voice copilot's model calls are not made here)")
		return
	}
	info, _, reason := cfg.CopilotLLM()
	setting("copilotProvider", cfg.CopilotProvider)
	model := cfg.CopilotModel
	if model == "" {
		model = "(default)"
	}
	setting("copilotModel", model)
	base := cfg.CopilotBaseURL
	if base == "" {
		base = "(the vendor's own)"
	}
	setting("copilotBaseURL", base)
	key := "none"
	if _, source := cfg.CopilotKey(); source == "stored" {
		key = "stored in this file"
	} else if source == "environment" {
		key = "from " + runner.CopilotKeyEnv
	}
	setting("copilotKey", key)
	if reason != "" {
		setting("state", "off: "+reason)
	} else {
		setting("state", fmt.Sprintf("on: your copilot's model calls run here (%s/%s)", info.Provider, info.Model))
	}
}

// runnerCopilotKey stores (or with --clear forgets) the copilot's API key.
// It is read from stdin so it is never on a command line, which every process
// on the machine can read.
func runnerCopilotKey(args []string, in *bufio.Reader, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("copilot-key")
	clear := fs.Bool("clear", false, "forget the stored key")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
	if err != nil {
		return err
	}
	if *clear {
		cfg.CopilotAPIKey = ""
	} else {
		key, err := readHiddenLine(in, stderr, "copilot API key: ")
		if err != nil {
			return fmt.Errorf("reading the key from stdin: %w", err)
		}
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 4096 || strings.ContainsAny(key, " \t\r\n") {
			return errors.New("that is not an API key")
		}
		cfg.CopilotAPIKey = key
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintln(stdout, "copilot key forgotten")
	} else {
		fmt.Fprintln(stdout, "copilot key saved")
	}
	if cfg.CopilotProvider == "" {
		fmt.Fprintln(stdout, "set the provider too: nodevas runner set copilotProvider anthropic (or openai)")
	}
	fmt.Fprintln(stdout, "restart a running runner for it to take effect")
	return nil
}

// runnerSet changes one run setting. A running runner reads its settings at
// start, so the change applies from the next start.
func runnerSet(args []string, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("set")
	rest, err := parseInterspersed(fs, args)
	if err != nil || len(rest) < 2 {
		return errUsage
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
	if err != nil {
		return err
	}
	if strings.EqualFold(rest[0], "autoAccept") && len(rest) == 2 && !strings.EqualFold(rest[1], "default") {
		// The defaults list the agents installed here, which only the CLI
		// can detect.
		on, err := strconv.ParseBool(strings.TrimSpace(rest[1]))
		if err != nil {
			return errors.New("autoAccept must be true or false")
		}
		cfg.SetAutoAccept(on, runner.InstalledKinds(context.Background(), runnerCommands, cfg))
	} else if err := cfg.Set(rest[0], rest[1:]); err != nil {
		return err
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return err
	}
	printRunSettings(stdout, cfg)
	warnAutoAccept(stderr, cfg, false)
	if cfg.PermissionMode == "bypassPermissions" {
		fmt.Fprintln(stderr, "note: bypassPermissions lets agents run any command on this machine without asking.")
	}
	if cfg.CodexSandbox == "danger-full-access" {
		fmt.Fprintln(stderr, "note: danger-full-access lets Codex runs run any command on this machine, outside any sandbox.")
	}
	if cfg.GeminiApprovalMode == "yolo" {
		fmt.Fprintln(stderr, "note: yolo lets Gemini runs run any command on this machine without asking.")
	}
	if cfg.OpenCodePermission == "full" {
		fmt.Fprintln(stderr, "note: full lets OpenCode runs run any command on this machine without asking.")
	}
	switch cfg.AntigravityMode {
	case "full":
		fmt.Fprintln(stderr, "note: full lets Antigravity runs use every tool, any command included, without asking.")
	case "auto":
		fmt.Fprintln(stderr, "note: auto lets Antigravity runs run the commands agy judges safe without asking.")
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
	testCommand := fs.String("test-command", "", "repo set: the repository's test command, argv-style (no shell unless you write sh -c)")
	testTimeout := fs.Int("test-timeout", 0, "repo set: minutes before the test command is stopped (0 is the default, 20)")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	path, cfg, err := loadRunnerConfig(*configFlag, stderr)
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
	case "set":
		if len(rest) != 1 {
			return errUsage
		}
		set := false
		fs.Visit(func(f *flag.Flag) { set = set || f.Name == "test-command" || f.Name == "test-timeout" })
		if !set {
			return errUsage
		}
		repo, err := cfg.SetRepoTestCommand(rest[0], *testCommand, *testTimeout)
		if err != nil {
			return err
		}
		if err := runner.SaveConfig(path, cfg); err != nil {
			return err
		}
		if command, timeout := cfg.RepoTestCommand(repo.ID); len(command) > 0 {
			fmt.Fprintf(stdout, "%s: test command %q (stopped after %s); restart the runner to use it\n",
				repo.Name, strings.Join(command, " "), timeout)
		} else {
			fmt.Fprintf(stdout, "%s: no test command; verify jobs are declined until one is set\n", repo.Name)
		}
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
		if command, timeout := cfg.RepoTestCommand(repo.ID); len(command) > 0 {
			fmt.Fprintf(w, "      test command: %s (stopped after %s)\n", strings.Join(command, " "), timeout)
		}
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
