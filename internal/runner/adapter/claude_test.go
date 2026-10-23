package adapter

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func parseFixture(t *testing.T, name string) ([]runnerwire.RunEvent, Outcome) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	parser := Claude{}.NewParser()
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
		usage.CacheWriteTokens != 17059 || usage.CostUSD != 0.00392726 {
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
		"--model", "sonnet", "--allowedTools", "Edit", "Bash(git *)"}
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

	// "default" means no --model flag; no allowed tools means no flag either;
	// an unset permission mode is the safe default, never the job's say.
	job.Model = "default"
	env.PermissionMode, env.AllowedTools = "", nil
	cmd, err = Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "--model") || strings.Contains(args, "--allowedTools") ||
		!strings.Contains(args, "--permission-mode acceptEdits") {
		t.Errorf("args = %s", args)
	}
}

func TestForAndTruncate(t *testing.T) {
	if For("claude") == nil || For("codex") != nil || For("") != nil {
		t.Error("For")
	}
	if got := Truncate("héllo", 2); got != "h" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("abc", 5); got != "abc" {
		t.Errorf("Truncate = %q", got)
	}
}
