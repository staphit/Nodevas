package adapter

import (
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// opencode_stream.jsonl is a real opencode 1.18.23 run (paths sanitised): it
// reads a file, calls an MCP tool on a server named nodevas, writes a file,
// runs a command and answers.
func TestOpenCodeParseStream(t *testing.T) {
	events, outcome := parseWith(t, OpenCode{}.NewParser(), "opencode_stream.jsonl")
	const (
		log    = runnerwire.EventLog
		text   = runnerwire.EventText
		call   = runnerwire.EventToolCall
		result = runnerwire.EventToolResult
		usage  = runnerwire.EventUsage
	)
	want := []string{log, call, result, call, result, usage, call, result, usage, call, result, usage, text, usage}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if events[0].Text != "opencode session started ses_e9357fbabffendk108ceo7txuW" {
		t.Errorf("start = %q", events[0].Text)
	}
	if c := events[1]; c.Tool != "read" || c.ToolID != "call_00_zmsz4nl7h9ovwvrqqd4r4inm" || c.Input != `{"filePath":"C:\\work\\repo\\a.txt"}` {
		t.Errorf("read call = %+v", c)
	}
	if r := events[2]; r.ToolID != events[1].ToolID || r.IsError || !strings.Contains(r.Text, "1: hi") {
		t.Errorf("read result = %+v", r)
	}
	// MCP tools are <server>_<tool>; nodevas's read as Claude Code's do.
	if c := events[3]; c.Tool != "mcp__nodevas__probe" || c.Input != "{}" {
		t.Errorf("mcp call = %+v", c)
	}
	if c := events[9]; c.Tool != "bash" || c.Input != `{"command":"git status"}` {
		t.Errorf("bash call = %+v", c)
	}
	if events[12].Text != "DONE" {
		t.Errorf("text = %q", events[12].Text)
	}
	// Usage adds up across steps; input is the uncached part already.
	last := events[len(events)-1].Usage
	if last == nil || last.InputTokens != 12739 || last.OutputTokens != 282 || last.CacheReadTokens != 44544 {
		t.Errorf("usage = %+v", last)
	}
	if !outcome.Done || outcome.IsError || outcome.Summary != "DONE" || outcome.Usage == nil || outcome.Usage.InputTokens != 12739 {
		t.Errorf("outcome = %+v", outcome)
	}
}

// opencode_denied.jsonl: a real run with bash denied. The tool is not offered
// at all; the model's attempt comes back as the "invalid" tool, and the run
// still ends normally.
func TestOpenCodeParseDenied(t *testing.T) {
	events, outcome := parseWith(t, OpenCode{}.NewParser(), "opencode_denied.jsonl")
	if c := events[1]; c.Tool != "invalid" || !strings.Contains(c.Input, "unavailable tool 'bash'") {
		t.Errorf("call = %+v", c)
	}
	if !outcome.Done || outcome.IsError || !strings.Contains(outcome.Summary, "No shell tool") {
		t.Errorf("outcome = %+v", outcome)
	}
}

// opencode_error.jsonl: the real line a run prints when the model's provider
// is unreachable, and nothing else.
func TestOpenCodeParseError(t *testing.T) {
	events, outcome := parseWith(t, OpenCode{}.NewParser(), "opencode_error.jsonl")
	if got := eventTypes(events); !slices.Equal(got, []string{runnerwire.EventLog, runnerwire.EventError}) {
		t.Fatalf("event types = %v", got)
	}
	const reason = "APIError: Cannot connect to API: Unable to connect. Is the computer able to access the url?"
	if outcome.Done || !outcome.IsError || outcome.Error != reason {
		t.Errorf("outcome = %+v", outcome)
	}

	// A tool that failed; an error a later finished turn got past; noise.
	p := OpenCode{}.NewParser()
	got := p.Parse([]byte(`{"type":"tool_use","part":{"tool":"edit","callID":"c1","state":{"status":"error","input":{"filePath":"x"},"error":"no such file"}}}`))
	if len(got) != 2 || !got[1].IsError || got[1].Text != "no such file" {
		t.Errorf("failed tool = %+v", got)
	}
	p.Parse([]byte(`{"type":"error","error":"stream reset"}`))
	p.Parse([]byte(`{"type":"step_finish","part":{"reason":"stop"}}`))
	if out := p.Outcome(); !out.Done || out.IsError {
		t.Errorf("recovered = %+v", out)
	}
	if got := p.Parse([]byte("Warning: config changed")); len(got) != 1 || got[0].Type != runnerwire.EventLog {
		t.Errorf("non-JSON = %+v", got)
	}
}

func TestOpenCodeCommand(t *testing.T) {
	t.Setenv(openCodeConfigEnv, `{"permission":{"bash":"allow"}}`)
	t.Setenv(RunIDEnv, "stale")
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "Do it \"now\" 100%", Model: "opencode/big-pickle"}
	env := RunEnv{
		Binary:  "/usr/bin/opencode",
		Workdir: "/data/worktrees/run-1",
		Branch:  "agent/n-42-run1",
		MCP:     MCPServer{Command: "/opt/nodevas", Args: []string{"mcp", "--server", "http://127.0.0.1:1/r/x"}},
	}
	cmd, err := OpenCode{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/opencode", "run", "--format", "json", "--dir", env.Workdir, "-m", "opencode/big-pickle"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("dir = %q", cmd.Dir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	if !strings.Contains(string(stdin), `claim_task with id "n-42"`) || !strings.Contains(string(stdin), "Do it \"now\" 100%") {
		t.Errorf("prompt = %q", stdin)
	}
	// OpenCode follows PWD over its working directory.
	if pwd, _ := envValue(cmd.Env, "PWD"); pwd != env.Workdir || countEnv(cmd.Env, "PWD") != 1 {
		t.Errorf("PWD = %q", pwd)
	}
	if n := countEnv(cmd.Env, RunIDEnv); n != 1 {
		t.Errorf("%d %s entries", n, RunIDEnv)
	}
	if id, _ := envValue(cmd.Env, RunIDEnv); id != "run-1" {
		t.Errorf("run id env = %q", id)
	}
	if n := countEnv(cmd.Env, openCodeConfigEnv); n != 1 {
		t.Errorf("%d %s entries; the runner's own must be replaced", n, openCodeConfigEnv)
	}
	raw, _ := envValue(cmd.Env, openCodeConfigEnv)
	var config struct {
		MCP map[string]struct {
			Type        string            `json:"type"`
			Command     []string          `json:"command"`
			Enabled     bool              `json:"enabled"`
			Timeout     int               `json:"timeout"`
			Environment map[string]string `json:"environment"`
		} `json:"mcp"`
		Permission map[string]string `json:"permission"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("config %q: %v", raw, err)
	}
	server := config.MCP["nodevas"]
	if len(config.MCP) != 1 || server.Type != "local" || !server.Enabled || server.Timeout != 900_000 ||
		!slices.Equal(server.Command, []string{"/opt/nodevas", "mcp", "--server", "http://127.0.0.1:1/r/x"}) ||
		server.Environment["NODEVAS_AGENT"] != "opencode" {
		t.Errorf("mcp = %s", raw)
	}
	// No setting is edit: edits yes, the shell and the web no.
	if p := config.Permission; p["edit"] != "allow" || p["bash"] != "deny" || p["webfetch"] != "deny" || p["websearch"] != "deny" ||
		p["external_directory"] != "deny" {
		t.Errorf("permission = %v", p)
	}

	for mode, want := range map[string]map[string]string{
		"read-only": {"edit": "deny", "bash": "deny", "webfetch": "deny", "websearch": "deny"},
		"full":      {"edit": "allow", "bash": "allow", "webfetch": "allow", "websearch": "allow"},
	} {
		env.PermissionMode = mode
		job.Model = "default"
		cmd, err := OpenCode{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(cmd.Args, "-m") {
			t.Errorf("%s: default model got -m: %q", mode, cmd.Args)
		}
		raw, _ := envValue(cmd.Env, openCodeConfigEnv)
		config.Permission = nil
		_ = json.Unmarshal([]byte(raw), &config)
		for tool, rule := range want {
			if config.Permission[tool] != rule {
				t.Errorf("%s: %s = %q, want %q", mode, tool, config.Permission[tool], rule)
			}
		}
	}
	env.PermissionMode = "yolo"
	if _, err := (OpenCode{}).Command(job, env); err == nil {
		t.Error("unknown mode: no error")
	}
}

func countEnv(env []string, name string) int {
	n := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			n++
		}
	}
	return n
}

func TestOpenCodeInteractiveCommand(t *testing.T) {
	env := interactiveEnv(t, "/usr/local/bin/opencode")
	job := interactiveJob()
	job.Agent, job.Model = "opencode", "opencode/big-pickle"
	cmd, err := OpenCode{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	want := []string{env.Workdir, "-m", "opencode/big-pickle", "--prompt"}
	if len(args) != len(want)+1 || !slices.Equal(args[:len(want)], want) || !strings.Contains(args[len(args)-1], "do the thing") {
		t.Fatalf("args = %q", args)
	}
	if slices.Contains(args, "run") || slices.Contains(args, "--format") {
		t.Fatal("headless flags in interactive command")
	}
	raw, _ := envValue(cmd.Env, openCodeConfigEnv)
	var config struct {
		Permission map[string]string `json:"permission"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	// Someone is at the terminal: what headless denies is asked about.
	if config.Permission["edit"] != "allow" || config.Permission["bash"] != "ask" ||
		config.Permission["external_directory"] != "ask" {
		t.Errorf("permission = %v", config.Permission)
	}
	if term, _ := envValue(cmd.Env, "TERM"); term != "xterm-256color" {
		t.Errorf("TERM = %q", term)
	}
	if pwd, _ := envValue(cmd.Env, "PWD"); pwd != env.Workdir {
		t.Errorf("PWD = %q", pwd)
	}
	if id, _ := envValue(cmd.Env, RunIDEnv); id != "run-1" {
		t.Errorf("run id env = %q", id)
	}
}

// opencode2_stream.jsonl is a real opencode 2.1.4 run (paths sanitised) with
// the run's 2.x config: nodevas's tools offered directly, execute denied. It
// globs, calls nodevas_ping, reads two files, asks a subagent and answers.
// 2.x calls the call ID part.id and prints no step_finish after the answer.
func TestOpenCode2ParseStream(t *testing.T) {
	events, outcome := parseWith(t, OpenCode{}.NewParser(), "opencode2_stream.jsonl")
	const (
		log    = runnerwire.EventLog
		text   = runnerwire.EventText
		call   = runnerwire.EventToolCall
		result = runnerwire.EventToolResult
		usage  = runnerwire.EventUsage
	)
	want := []string{log, call, result, call, result, usage, call, result, call, result, call, result, usage,
		call, result, usage, text}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if c := events[1]; c.Tool != "glob" || c.ToolID != "call_dee5cd698ddd46e2b1cc8e93" {
		t.Errorf("glob call = %+v", c)
	}
	if c, r := events[6], events[7]; c.Tool != "mcp__nodevas__ping" || r.ToolID != c.ToolID || r.Text != "the secret word is zebra" {
		t.Errorf("mcp call = %+v, result %+v", c, r)
	}
	if c := events[13]; c.Tool != "subagent" {
		t.Errorf("subagent call = %+v", c)
	}
	last := events[15].Usage
	if last == nil || last.InputTokens != 11708 || last.OutputTokens != 1626 || last.CacheReadTokens != 6144 {
		t.Errorf("usage = %+v", last)
	}
	if outcome.IsError || !strings.Contains(outcome.Summary, "zebra") || outcome.Usage == nil {
		t.Errorf("outcome = %+v", outcome)
	}
}

// opencode2_error.jsonl: 2.x's line for a model it has no route to.
func TestOpenCode2ParseError(t *testing.T) {
	_, outcome := parseWith(t, OpenCode{}.NewParser(), "opencode2_error.jsonl")
	if !outcome.IsError || outcome.Error != "provider.no-route: Model unavailable: nosuch/model" {
		t.Errorf("outcome = %+v", outcome)
	}
}

// OpenCode 2: a private server per run, no --dir, MCP servers under
// mcp.servers, nodevas's tools out of Code Mode, and execute (JavaScript with
// fetch) denied short of full.
func TestOpenCode2Command(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "Do it", Model: "opencode/big-pickle"}
	env := RunEnv{
		Binary:     "/usr/bin/opencode",
		Workdir:    "/data/worktrees/run-1",
		MCP:        MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}},
		DisableMCP: []string{"repoEvil", "nodevas"},
		Version:    "opencode v2.1.4",
	}
	cmd, err := OpenCode{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/opencode", "run", "--standalone", "--format", "json", "-m", "opencode/big-pickle"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if pwd, _ := envValue(cmd.Env, "PWD"); cmd.Dir != env.Workdir || pwd != env.Workdir {
		t.Errorf("dir = %q, PWD = %q", cmd.Dir, pwd)
	}
	type config struct {
		MCP struct {
			Servers map[string]map[string]any `json:"servers"`
		} `json:"mcp"`
		Permission map[string]string `json:"permission"`
	}
	read := func(cmd *exec.Cmd) config {
		t.Helper()
		raw, _ := envValue(cmd.Env, openCodeConfigEnv)
		var c config
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("config %q: %v", raw, err)
		}
		return c
	}
	c := read(cmd)
	nodevas, evil := c.MCP.Servers["nodevas"], c.MCP.Servers["repoEvil"]
	if len(c.MCP.Servers) != 2 || nodevas["type"] != "local" || nodevas["codemode"] != false ||
		nodevas["timeout"] != float64(900_000) || nodevas["enabled"] != nil {
		t.Errorf("nodevas = %v", nodevas)
	}
	// A server is switched off with a whole entry: 2.x drops one without a
	// type, and a remote one starts no program.
	if evil["type"] != "remote" || evil["disabled"] != true || evil["url"] == "" || evil["command"] != nil {
		t.Errorf("repoEvil = %v", evil)
	}
	if p := c.Permission; p["execute"] != "deny" || p["nodevas_*"] != "allow" || p["bash"] != "deny" || p["webfetch"] != "deny" {
		t.Errorf("permission = %v", p)
	}
	for mode, rule := range map[string]string{"read-only": "deny", "edit": "deny", "full": "allow"} {
		env.PermissionMode = mode
		cmd, err := OpenCode{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if got := read(cmd).Permission["execute"]; got != rule {
			t.Errorf("%s: execute = %q, want %q", mode, got, rule)
		}
	}

	// An unknown version is 1.x.
	env.Version = ""
	if cmd, _ := (OpenCode{}).Command(job, env); !slices.Contains(cmd.Args, "--dir") || slices.Contains(cmd.Args, "--standalone") {
		t.Errorf("unknown version args = %q", cmd.Args)
	}
}

func TestOpenCode2InteractiveCommand(t *testing.T) {
	env := interactiveEnv(t, "/usr/local/bin/opencode")
	env.Version = "2.1.4"
	job := interactiveJob()
	job.Agent, job.Model = "opencode", "opencode/big-pickle"
	cmd, err := OpenCode{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	if want := []string{env.Workdir, "--standalone", "--prompt"}; len(args) != 4 || !slices.Equal(args[:3], want) {
		t.Fatalf("args = %q", args)
	}
	raw, _ := envValue(cmd.Env, openCodeConfigEnv)
	var config struct {
		Model      string            `json:"model"`
		Permission map[string]string `json:"permission"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	// The TUI takes no -m; someone at the terminal is asked about execute.
	if config.Model != "opencode/big-pickle" || config.Permission["execute"] != "ask" {
		t.Errorf("config = %s", raw)
	}
}
