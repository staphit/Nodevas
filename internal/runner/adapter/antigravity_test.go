package adapter

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// The antigravity_*.jsonl fixtures are real agy 1.4.6 runs (paths sanitised).

// antigravity_stream.jsonl: plan mode reads a file, writes its plan and
// answers in text deltas.
func TestAntigravityParseStream(t *testing.T) {
	events, outcome := parseWith(t, Antigravity{}.NewParser(), "antigravity_stream.jsonl")
	const (
		log    = runnerwire.EventLog
		text   = runnerwire.EventText
		call   = runnerwire.EventToolCall
		result = runnerwire.EventToolResult
		usage  = runnerwire.EventUsage
	)
	want := []string{log, call, result, call, result, text, usage}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if events[0].Text != "antigravity session started d8a7404f-c28f-48f9-bfe5-2c4e8fd8bc64 (model gemini-3.8-flash-low)" {
		t.Errorf("init = %q", events[0].Text)
	}
	if c := events[1]; c.Tool != "read_file" || c.ToolID != "2" || c.Input != `{"Path":"C:\\work\\repo\\a.txt"}` {
		t.Errorf("read call = %+v", c)
	}
	if r := events[2]; r.ToolID != "2" || r.IsError || r.Text != "1 lines, 3 bytes" {
		t.Errorf("read result = %+v", r)
	}
	// Nine deltas, one event.
	if txt := events[5].Text; !strings.HasPrefix(txt, "Plan created: ") || !strings.HasSuffix(txt, "DONE\n") {
		t.Errorf("text = %q", txt)
	}
	if u := events[6].Usage; u == nil || u.InputTokens != 24789 || u.OutputTokens != 406 {
		t.Errorf("usage = %+v", u)
	}
	if !outcome.Done || outcome.IsError || !strings.Contains(outcome.Summary, "```\nhi\n```") ||
		outcome.Usage == nil || outcome.Usage.OutputTokens != 406 {
		t.Errorf("outcome = %+v", outcome)
	}
}

// antigravity_mcp.jsonl: an MCP tool call allowed by the owner's settings.
func TestAntigravityParseMCP(t *testing.T) {
	events, outcome := parseWith(t, Antigravity{}.NewParser(), "antigravity_mcp.jsonl")
	if c := events[1]; c.Type != runnerwire.EventToolCall || c.Tool != "mcp__probe__probe" || c.Input != "{}" {
		t.Errorf("mcp call = %+v", c)
	}
	if r := events[2]; r.IsError || r.Text != "secret word: pelican; env=fromenv" {
		t.Errorf("mcp result = %+v", r)
	}
	if !outcome.Done || outcome.IsError || !strings.Contains(outcome.Summary, "pelican") {
		t.Errorf("outcome = %+v", outcome)
	}
}

// antigravity_denied.jsonl: a shell command headless mode denied without
// asking; the turn still succeeds, with nothing to say.
func TestAntigravityParseDenied(t *testing.T) {
	events, outcome := parseWith(t, Antigravity{}.NewParser(), "antigravity_denied.jsonl")
	var denied, refused *runnerwire.RunEvent
	for i := range events {
		if events[i].Type == runnerwire.EventError {
			denied = &events[i]
		}
		if events[i].Type == runnerwire.EventToolResult && events[i].ToolID == "6" {
			refused = &events[i]
		}
	}
	if refused == nil || !refused.IsError || !strings.Contains(refused.Text, "user denied permission to run command") {
		t.Errorf("refused command = %+v", refused)
	}
	if denied == nil || denied.Text != "antigravity denied without asking (headless): command (RunCommand)" {
		t.Errorf("denied = %+v", denied)
	}
	if !outcome.Done || outcome.IsError || outcome.Summary != "" {
		t.Errorf("outcome = %+v", outcome)
	}
}

// antigravity_error.jsonl: a turn that ended in an executor error.
func TestAntigravityParseError(t *testing.T) {
	events, outcome := parseWith(t, Antigravity{}.NewParser(), "antigravity_error.jsonl")
	if last := events[len(events)-1]; last.Type != runnerwire.EventError || !strings.Contains(last.Text, "agent executor error") {
		t.Errorf("last = %+v", last)
	}
	if !outcome.Done || !outcome.IsError || !strings.HasPrefix(outcome.Error, "agent executor error: ") {
		t.Errorf("outcome = %+v", outcome)
	}

	// A tool step that only ever arrives finished; deltas cut off without a
	// result; noise.
	p := Antigravity{}.NewParser()
	got := p.Parse([]byte(`{"event":"step_update","step_update":{"step_index":3,"state":"DONE","step_type":"tool","tool_name":"view_file","tool_info":{"name":"view_file","parameters":{"Path":"x"},"output":"ok"}}}`))
	if len(got) != 2 || got[0].Type != runnerwire.EventToolCall || got[1].Text != "ok" {
		t.Errorf("finished-only tool = %+v", got)
	}
	p.Parse([]byte(`{"event":"step_update","step_update":{"step_index":4,"state":"ACTIVE","step_type":"agent_response","text_delta":"half a rep"}}`))
	if out := p.Outcome(); out.Done || out.Summary != "half a rep" {
		t.Errorf("cut off = %+v", out)
	}
	if got := p.Parse([]byte("Fetching available models...")); len(got) != 1 || got[0].Type != runnerwire.EventLog {
		t.Errorf("non-JSON = %+v", got)
	}
}

func TestAntigravityCommand(t *testing.T) {
	t.Setenv(RunMCPEnv, `["stale"]`)
	tmp := t.TempDir()
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "Do it \"now\"\nplease", Model: "gemini-3.8-flash-low"}
	env := RunEnv{
		Binary:  "/usr/bin/agy",
		Workdir: "/data/worktrees/run-1",
		Branch:  "agent/n-42-run1",
		TempDir: tmp,
		MCP:     MCPServer{Command: "/opt/nodevas", Args: []string{"mcp", "--server", "http://127.0.0.1:1/r/x"}},
	}
	cmd, err := Antigravity{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "agyroot")
	want := []string{"/usr/bin/agy", "--input-format=stream-json", "--output-format=stream-json", "--mode=accept-edits",
		"--model=gemini-3.8-flash-low", "--add-dir=" + root, "--disable-slash-commands", "-p="}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("dir = %q", cmd.Dir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	if n := strings.Count(string(stdin), "\n"); n != 1 || !strings.HasSuffix(string(stdin), "\n") {
		t.Fatalf("stdin is not one line: %q", stdin)
	}
	var input struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(stdin, &input); err != nil || input.Event != "user" ||
		!strings.Contains(input.Message.Content, `claim_task with id "n-42"`) || !strings.Contains(input.Message.Content, "Do it \"now\"\nplease") {
		t.Errorf("stdin = %q (%v)", stdin, err)
	}
	if args, _ := envValue(cmd.Env, RunMCPEnv); args != `["mcp","--server","http://127.0.0.1:1/r/x"]` || countEnv(cmd.Env, RunMCPEnv) != 1 {
		t.Errorf("%s = %q", RunMCPEnv, args)
	}
	if id, _ := envValue(cmd.Env, RunIDEnv); id != "run-1" {
		t.Errorf("run id env = %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".agents", "mcp_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	server := config.MCPServers["nodevas"]
	if len(config.MCPServers) != 1 || server.Command != "/opt/nodevas" || !slices.Equal(server.Args, env.MCP.Args) ||
		server.Env["NODEVAS_AGENT"] != "antigravity" {
		t.Errorf("mcp config = %s", raw)
	}

	// full is accept-edits with every permission skipped; plan and auto are
	// agy's own; the default model is no flag.
	for mode, flags := range map[string][]string{
		"full": {"--mode=accept-edits", "--dangerously-skip-permissions"},
		"plan": {"--mode=plan"},
		"auto": {"--mode=auto"},
	} {
		env.PermissionMode = mode
		job.Model = "default"
		cmd, err := Antigravity{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args[3:3+len(flags)], flags) || slices.ContainsFunc(cmd.Args, func(a string) bool {
			return strings.HasPrefix(a, "--model")
		}) {
			t.Errorf("%s: args = %q", mode, cmd.Args)
		}
		if mode != "full" && slices.Contains(cmd.Args, "--dangerously-skip-permissions") {
			t.Errorf("%s skips permissions", mode)
		}
	}
	env.PermissionMode = "yolo"
	if _, err := (Antigravity{}).Command(job, env); err == nil {
		t.Error("unknown mode: no error")
	}
}

func TestAntigravityInteractiveCommand(t *testing.T) {
	env := interactiveEnv(t, "/usr/local/bin/agy")
	job := interactiveJob()
	job.Agent, job.Model = "antigravity", "default"
	cmd, err := Antigravity{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	if !strings.HasPrefix(args[0], "-i=") || !strings.Contains(args[0], "do the thing") {
		t.Fatalf("prompt = %q", args[0])
	}
	want := []string{"--mode=accept-edits", "--add-dir=" + filepath.Join(env.TempDir, "agyroot")}
	if !slices.Equal(args[1:], want) {
		t.Fatalf("args = %q\nwant %q", args[1:], want)
	}
	if _, err := os.Stat(filepath.Join(env.TempDir, "agyroot", ".agents", "mcp_config.json")); err != nil {
		t.Error(err)
	}
	if _, ok := envValue(cmd.Env, RunMCPEnv); !ok {
		t.Errorf("no %s", RunMCPEnv)
	}
	if term, _ := envValue(cmd.Env, "TERM"); term != "xterm-256color" {
		t.Errorf("TERM = %q", term)
	}

	// A prompt too long for the command line goes in the task file, and the
	// temp dir joins the workspace so the agent can read it.
	job.Prompt = strings.Repeat("x", maxPromptArg+1)
	cmd, err = Antigravity{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Args[1], TaskFileName) || cmd.Args[len(cmd.Args)-1] != "--add-dir="+env.TempDir {
		t.Errorf("args = %q", cmd.Args)
	}
}
