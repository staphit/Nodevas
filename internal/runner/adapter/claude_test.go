package adapter

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func parseFixture(t *testing.T, name string) ([]runnerwire.RunEvent, Outcome) {
	t.Helper()
	return parseWith(t, Claude{}.NewParser(), name)
}

// parseWith feeds a fixture to a parser a line at a time, as the runner does.
func parseWith(t *testing.T, parser Parser, name string) ([]runnerwire.RunEvent, Outcome) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []runnerwire.RunEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		events = append(events, parser.Parse(scanner.Bytes())...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events, parser.Outcome()
}

func TestClaudeParseStream(t *testing.T) {
	events, outcome := parseFixture(t, "claude_stream.jsonl")
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	want := []string{
		runnerwire.EventLog,        // init
		runnerwire.EventLog,        // the non-JSON line
		runnerwire.EventThinking,   // thinking block
		runnerwire.EventToolCall,   // Read
		runnerwire.EventToolResult, // Read's result
		runnerwire.EventToolCall,   // Bash
		runnerwire.EventToolResult, // Bash's failed result
		runnerwire.EventText,       // the answer
		runnerwire.EventUsage,      // result
	}
	if !slices.Equal(types, want) {
		t.Fatalf("event types = %v\nwant %v", types, want)
	}
	init := events[0].Text
	if !strings.Contains(init, "fc97caf9-629c-414a-8b84-67ec6e88bd98") || !strings.Contains(init, "claude-haiku-5-5") ||
		!strings.Contains(init, "acceptEdits") {
		t.Errorf("init log = %q", init)
	}
	if events[1].Text != "not json: a warning the CLI printed" {
		t.Errorf("non-JSON line = %q", events[1].Text)
	}
	if events[2].Text != "I should claim the task first." {
		t.Errorf("thinking = %q", events[2].Text)
	}
	read := events[3]
	if read.Tool != "Read" || read.ToolID != "toolu_01XkriQFWUKRdjvEEY3MYbXM" ||
		read.Input != `{"file_path":"C:\\work\\worktrees\\run1\\a.txt"}` {
		t.Errorf("tool call = %+v", read)
	}
	if res := events[4]; res.ToolID != read.ToolID || res.Text != "1\thello fixture\n2\t" || res.IsError {
		t.Errorf("tool result = %+v", res)
	}
	if res := events[6]; res.ToolID != "toolu_02Bash" || res.Text != "FAIL nodevas/internal/x\nexit status 1" || !res.IsError {
		t.Errorf("failed tool result = %+v", res)
	}
	if events[7].Text != `Content: "hello fixture"` {
		t.Errorf("text = %q", events[7].Text)
	}
	usage := events[8].Usage
	if usage == nil || usage.InputTokens != 4 || usage.OutputTokens != 130 || usage.CacheReadTokens != 45006 ||
		usage.CacheWriteTokens != 17059 || usage.CostUSD != 0.00392726 || usage.Model != "claude-haiku-5-5" {
		t.Errorf("usage = %+v", usage)
	}
	if !outcome.Done || outcome.IsError || outcome.Summary != `Content: "hello fixture"` || outcome.Usage == nil ||
		outcome.Usage.OutputTokens != 130 {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestClaudeParseErrorResult(t *testing.T) {
	events, outcome := parseFixture(t, "claude_error.jsonl")
	if len(events) != 3 || events[1].Type != runnerwire.EventUsage || events[2].Type != runnerwire.EventError {
		t.Fatalf("events = %+v", events)
	}
	if !outcome.Done || !outcome.IsError || outcome.Error != "Reached maximum number of turns (50)" ||
		outcome.Usage.CostUSD != 1.25 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// Of a result's models, the one that wrote the most is the run's; with no
// modelUsage, the init line's.
func TestClaudeMainModel(t *testing.T) {
	p := Claude{}.NewParser()
	p.Parse([]byte(`{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5"}`))
	events := p.Parse([]byte(`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":1,"output_tokens":9},` +
		`"modelUsage":{"claude-haiku-5-5":{"outputTokens":3},"claude-sonnet-5-5":{"outputTokens":6}}}`))
	if len(events) == 0 || events[0].Usage == nil || events[0].Usage.Model != "claude-sonnet-5-5" {
		t.Fatalf("events = %+v", events)
	}
	p = Claude{}.NewParser()
	p.Parse([]byte(`{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5"}`))
	events = p.Parse([]byte(`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":1,"output_tokens":9}}`))
	if len(events) == 0 || events[0].Usage == nil || events[0].Usage.Model != "claude-opus-5-5" {
		t.Fatalf("events = %+v", events)
	}
}

func TestClaudeParseEdgeCases(t *testing.T) {
	p := Claude{}.NewParser()
	if got := p.Parse([]byte("  \r\n")); got != nil {
		t.Errorf("blank line = %+v", got)
	}
	// A BOM and CRLF, as a Windows pipe can deliver them.
	if got := p.Parse([]byte("\xEF\xBB\xBF{\"type\":\"system\",\"subtype\":\"init\",\"model\":\"m\"}\r")); len(got) != 1 ||
		!strings.Contains(got[0].Text, "model m") {
		t.Errorf("BOM line = %+v", got)
	}
	// Unknown types and a user message whose content is a plain string.
	for _, line := range []string{
		`{"type":"stream_event","event":{}}`,
		`{"type":"user","message":{"role":"user","content":"the prompt"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"   "}]}}`,
	} {
		if got := p.Parse([]byte(line)); len(got) != 0 {
			t.Errorf("%s → %+v", line, got)
		}
	}
	// Oversize text is cut to the wire limit without splitting a rune.
	long := strings.Repeat("é", runnerwire.MaxEventText)
	raw, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"content": []map[string]string{{"type": "text", "text": long}}}})
	got := p.Parse(raw)
	if len(got) != 1 || len(got[0].Text) > runnerwire.MaxEventText || !strings.HasSuffix(got[0].Text, "é") {
		t.Errorf("long text: %d bytes", len(got[0].Text))
	}
	// An error result without text or errors still says something.
	p = Claude{}.NewParser()
	p.Parse([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true}`))
	if out := p.Outcome(); out.Error != "claude reported an error (error_during_execution)" || out.Usage != nil {
		t.Errorf("bare error outcome = %+v", out)
	}
}

func TestClaudeCommand(t *testing.T) {
	tmp := t.TempDir()
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", NodeTitle: "Fix the thing", Prompt: "Do it \"now\" 100%", Model: "sonnet"}
	env := RunEnv{
		Binary:         "/usr/bin/claude",
		Workdir:        "/data/worktrees/run-1",
		Branch:         "agent/n-42-run1",
		TempDir:        tmp,
		MCP:            MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp", "--server", "http://127.0.0.1:1/r/x"}},
		PermissionMode: "plan",
		AllowedTools:   []string{"Edit", "Bash(git *)"},
	}
	cmd, err := Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(tmp, "mcp.json")
	want := []string{"/usr/bin/claude", "-p", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "plan", "--mcp-config", mcpPath, "--strict-mcp-config",
		"--model", "sonnet", "--allowedTools", "mcp__nodevas", "Edit", "Bash(git *)"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("dir = %q", cmd.Dir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	prompt := string(stdin)
	for _, s := range []string{"node n-42", "agent/n-42-run1", `claim_task with id "n-42"`, "Do not push", "Do it \"now\" 100%"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing %q:\n%s", s, prompt)
		}
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "CLAUDECODE=") {
			t.Errorf("CLAUDECODE leaked into the child env")
		}
	}

	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.MCPServers["nodevas"]; got.Command != "/usr/bin/nodevas" || len(got.Args) != 3 {
		t.Errorf("mcp config = %s", raw)
	}

	// "default" means no --model flag; no allowed tools still allows the
	// nodevas server's tools, and nothing else; an unset permission mode is the safe default, never the job's say.
	job.Model = "default"
	env.PermissionMode, env.AllowedTools = "", nil
	cmd, err = Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--model") || !strings.HasSuffix(args, "--allowedTools mcp__nodevas") ||
		!strings.Contains(args, "--permission-mode acceptEdits") {
		t.Errorf("args = %s", args)
	}
}

func TestForAndTruncate(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "gemini", "opencode", "antigravity"} {
		if a := For(kind); a == nil || a.Kind() != kind {
			t.Errorf("For(%q) = %v", kind, a)
		}
		if _, ok := For(kind).(Interactive); !ok {
			t.Errorf("%s has no interactive command", kind)
		}
	}
	if For("aider") != nil || For("") != nil {
		t.Error("For")
	}
	if Binary("antigravity") != "agy" || Binary("opencode") != "opencode" || Binary("claude") != "claude" {
		t.Error("Binary")
	}
	if got := Truncate("héllo", 2); got != "h" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("abc", 5); got != "abc" {
		t.Errorf("Truncate = %q", got)
	}
}

// A permission_denied line reuses "message" for a plain string; it must
// become an error event, not fail to parse and land as raw JSON.
func TestClaudeParserPermissionDenied(t *testing.T) {
	line := `{"type":"system","subtype":"permission_denied","tool_name":"Bash","tool_use_id":"toolu_1",` +
		`"decision_reason_type":"other","decision_reason":"This command requires approval",` +
		`"message":"This command requires approval","uuid":"u","session_id":"s"}`
	events := Claude{}.NewParser().Parse([]byte(line))
	if len(events) != 1 || events[0].Type != runnerwire.EventError ||
		events[0].Text != "permission denied: Bash (This command requires approval)" {
		t.Fatalf("events = %+v", events)
	}
}

// A review or plan never gets a tool that writes a file or runs a command,
// whatever the owner allowed; implement runs are as configured.
func TestClaudeReadOnlyKinds(t *testing.T) {
	env := RunEnv{Binary: "/usr/bin/claude", Workdir: "/w", TempDir: t.TempDir(),
		PermissionMode: "bypassPermissions", AllowedTools: []string{"Bash"}}
	for kind, readOnly := range map[string]bool{
		runnerwire.JobReview: true, runnerwire.JobPlan: true, runnerwire.JobRevise: false, "": false,
	} {
		cmd, err := Claude{}.Command(runnerwire.Job{RunID: "r", NodeID: "n", Kind: kind}, env)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.Index(cmd.Args, "--disallowedTools")
		if readOnly != (i >= 0) {
			t.Fatalf("kind %q: args %q", kind, cmd.Args)
		}
		if readOnly {
			if cmd.Args[i+1] != "Edit,Write,MultiEdit,NotebookEdit,Bash,PowerShell" || cmd.Args[i+2] != "--allowedTools" ||
				cmd.Args[i+3] != NodevasMCPTools || cmd.Args[i-2] != "--setting-sources" || cmd.Args[i-1] != "user" {
				t.Fatalf("kind %q: args %q", kind, cmd.Args)
			}
		}
	}
}

// With permissionRelay a prompt goes to the hub's approve_tool, which waits
// for a person: the tool is allowed and the MCP timeout outlasts the wait.
// The runner's own limits stay: a read-only run's deny list and the mode.
func TestClaudeCommandPermissionRelay(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x"}
	env := RunEnv{Binary: "/usr/bin/claude", Workdir: "/w", Branch: "b", TempDir: t.TempDir(),
		AllowedTools: []string{"Edit"}}
	off, err := Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(off.Args, " "); strings.Contains(args, "--permission-prompt-tool") {
		t.Errorf("relay off: args = %s", args)
	}
	if _, ok := envValue(off.Env, ClaudeMCPTimeoutEnv); ok {
		t.Errorf("relay off sets %s", ClaudeMCPTimeoutEnv)
	}

	env.PermissionRelay = true
	on, err := Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	prompt := slices.Index(on.Args, "--permission-prompt-tool")
	allowed := slices.Index(on.Args, "--allowedTools")
	if prompt < 0 {
		t.Fatalf("relay on: no --permission-prompt-tool in %q", on.Args)
	}
	if on.Args[prompt+1] != ClaudeApproveTool || allowed < prompt ||
		!slices.Contains(on.Args[allowed:], ClaudeApproveTool) || !slices.Contains(on.Args[allowed:], "Edit") {
		t.Errorf("relay on: args = %q", on.Args)
	}
	// Everything else is as it was.
	stripped := slices.Clone(on.Args)
	stripped = slices.Delete(stripped, prompt, prompt+2)
	stripped = slices.DeleteFunc(stripped, func(a string) bool { return a == ClaudeApproveTool })
	if !slices.Equal(stripped, off.Args) {
		t.Errorf("relay changed more than the prompt tool:\n%q\n%q", stripped, off.Args)
	}
	timeout, ok := envValue(on.Env, ClaudeMCPTimeoutEnv)
	if ms, err := strconv.Atoi(timeout); !ok || err != nil || ms < 330000 {
		t.Errorf("%s = %q", ClaudeMCPTimeoutEnv, timeout)
	}

	// A review stays read-only: the deny list wins over any approval.
	review := job
	review.Kind = runnerwire.JobReview
	cmd, err := Claude{}.Command(review, env)
	if err != nil {
		t.Fatal(err)
	}
	deny := slices.Index(cmd.Args, "--disallowedTools")
	if deny < 0 || cmd.Args[deny+1] != ClaudeReadOnlyDeny || !slices.Contains(cmd.Args, "--permission-prompt-tool") {
		t.Errorf("review with relay: args = %q", cmd.Args)
	}

	// Plan and dontAsk never ask: no prompt tool, whatever the setting.
	for _, mode := range []string{"plan", "dontAsk"} {
		env.PermissionMode = mode
		cmd, err := Claude{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if args := strings.Join(cmd.Args, " "); strings.Contains(args, "--permission-prompt-tool") ||
			!strings.Contains(args, "--permission-mode "+mode) {
			t.Errorf("mode %s with relay: args = %s", mode, args)
		}
	}
}
