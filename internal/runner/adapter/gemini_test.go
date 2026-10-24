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

// The Gemini fixtures follow the CLI's documented stream-json format; they
// were not recorded from a real run (the CLI was not installed).
func TestGeminiParseStream(t *testing.T) {
	events, outcome := parseWith(t, Gemini{}.NewParser(), "gemini_stream.jsonl")
	want := []string{
		runnerwire.EventLog,                                  // init
		runnerwire.EventText,                                 // two deltas, one event
		runnerwire.EventToolCall, runnerwire.EventToolResult, // claim_task
		runnerwire.EventToolCall, runnerwire.EventToolResult, // shell, refused
		runnerwire.EventLog,                                  // warning
		runnerwire.EventLog,                                  // the non-JSON line
		runnerwire.EventToolCall, runnerwire.EventToolResult, // write_file
		runnerwire.EventText,  // the closing deltas, flushed by the result
		runnerwire.EventUsage, // result
	}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if !strings.Contains(events[0].Text, "6f1d2c3b-gemini-fixture") || !strings.Contains(events[0].Text, "gemini-2.5-pro") {
		t.Errorf("init = %q", events[0].Text)
	}
	if events[1].Text != "I'll claim the task first." {
		t.Errorf("text = %q", events[1].Text)
	}
	if c := events[2]; c.Tool != "claim_task" || c.ToolID != "claim_task-1761267601200-0" || c.Input != `{"id":"n-1"}` {
		t.Errorf("tool call = %+v", c)
	}
	if r := events[3]; r.ToolID != events[2].ToolID || r.IsError || r.Text != `{"id":"n-1","owner":"run:gemini"}` {
		t.Errorf("tool result = %+v", r)
	}
	if r := events[5]; !r.IsError || !strings.Contains(r.Text, "requires approval") {
		t.Errorf("refused tool = %+v", r)
	}
	if events[6].Text != "gemini: Loop detection: repeated tool call" {
		t.Errorf("warning = %q", events[6].Text)
	}
	usage := events[len(events)-1].Usage
	if usage == nil || usage.InputTokens != 4000 || usage.CacheReadTokens != 8000 || usage.OutputTokens != 500 ||
		usage.Model != "gemini-2.5-pro" {
		t.Errorf("usage = %+v", usage)
	}
	if !outcome.Done || outcome.IsError || outcome.Summary != "Created hello.txt and marked the node done." ||
		outcome.Usage == nil || outcome.Usage.OutputTokens != 500 {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestGeminiParseError(t *testing.T) {
	events, outcome := parseWith(t, Gemini{}.NewParser(), "gemini_error.jsonl")
	want := []string{runnerwire.EventLog, runnerwire.EventError, runnerwire.EventUsage, runnerwire.EventError}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v", got)
	}
	if !outcome.Done || !outcome.IsError || outcome.Error != "Reached the quota for this model." {
		t.Errorf("outcome = %+v", outcome)
	}

	// A whole (non-delta) assistant message, and a stream that ends on an
	// error without a result.
	p := Gemini{}.NewParser()
	got := p.Parse([]byte(`{"type":"message","role":"assistant","content":"All done."}`))
	if len(got) != 1 || got[0].Text != "All done." {
		t.Errorf("whole message = %+v", got)
	}
	p.Parse([]byte(`{"type":"message","role":"assistant","content":"half a rep","delta":true}`))
	p.Parse([]byte(`{"type":"error","severity":"error","message":"connection reset"}`))
	if out := p.Outcome(); out.Done || !out.IsError || out.Error != "connection reset" || out.Summary != "half a rep" {
		t.Errorf("outcome = %+v", out)
	}
	// A failed result with nothing to say still says something.
	p = Gemini{}.NewParser()
	p.Parse([]byte(`{"type":"result","status":"error"}`))
	if out := p.Outcome(); out.Error != "gemini reported an error (error)" {
		t.Errorf("bare failure = %+v", out)
	}
}

func TestGeminiCommand(t *testing.T) {
	tmp := t.TempDir()
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "Do it", Model: "gemini-2.5-pro"}
	env := RunEnv{
		Binary:         "/usr/bin/gemini",
		Workdir:        "/data/worktrees/run-1",
		Branch:         "agent/n-42-run1",
		TempDir:        tmp,
		MCP:            MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp", "--actor", "run:gemini"}},
		PermissionMode: "yolo",
	}
	cmd, err := Gemini{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/gemini", "--output-format", "stream-json", "--approval-mode", "yolo",
		"--allowed-mcp-server-names", "nodevas", "-m", "gemini-2.5-pro"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("dir = %q", cmd.Dir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	if !strings.Contains(string(stdin), `claim_task with id "n-42"`) || !strings.Contains(string(stdin), "Do it") {
		t.Errorf("prompt = %q", stdin)
	}
	settingsPath := filepath.Join(tmp, "gemini-settings.json")
	if !slices.Contains(cmd.Env, "GEMINI_CLI_SYSTEM_SETTINGS_PATH="+settingsPath) {
		t.Errorf("settings path not in the child env")
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Trust   bool     `json:"trust"`
			Timeout int      `json:"timeout"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	server := settings.MCPServers["nodevas"]
	if len(settings.MCPServers) != 1 || server.Command != "/usr/bin/nodevas" || !slices.Equal(server.Args, env.MCP.Args) ||
		!server.Trust || server.Timeout != 900_000 {
		t.Errorf("settings = %s", raw)
	}

	// "default" is no -m; no approval mode setting is auto_edit.
	job.Model = "default"
	env.PermissionMode = ""
	cmd, err = Gemini{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, " -m ") || !strings.Contains(args, "--approval-mode auto_edit") {
		t.Errorf("args = %s", args)
	}
}
