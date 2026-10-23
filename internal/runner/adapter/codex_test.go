package adapter

import (
	"io"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func eventTypes(events []runnerwire.RunEvent) []string {
	types := make([]string, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}

// codex_stream.jsonl is a real codex-cli 0.156.0 run (paths sanitised): it
// claims its node over MCP, runs commands, writes a file, gets one MCP call
// wrong and retries it.
func TestCodexParseStream(t *testing.T) {
	events, outcome := parseWith(t, Codex{}.NewParser(), "codex_stream.jsonl")
	const (
		log    = runnerwire.EventLog
		text   = runnerwire.EventText
		call   = runnerwire.EventToolCall
		result = runnerwire.EventToolResult
	)
	want := []string{
		log, log, log, log, // thread.started, three warning items
		text,         // "Claiming node."
		call, result, // claim_task
		call, result, // get_node
		call, result, // shell
		call, result, // file_change
		text,         // "Verifying bytes."
		call, result, // shell
		call, result, // set_node_status, refused
		call, result, // set_node_status again
		text,                  // the final message
		runnerwire.EventUsage, // turn.completed
	}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if !strings.Contains(events[0].Text, "01a169d3-4de5-7b12-b230-46d24af8c811") {
		t.Errorf("thread log = %q", events[0].Text)
	}
	if !strings.HasPrefix(events[1].Text, "codex: Codex is ignoring 1 unrecognized configuration setting") {
		t.Errorf("warning item = %q", events[1].Text)
	}
	claim, claimed := events[5], events[6]
	if claim.Tool != "mcp__nodevas__claim_task" || claim.ToolID != "item_4" || claim.Input != `{"id":"node-0001"}` {
		t.Errorf("claim call = %+v", claim)
	}
	if claimed.ToolID != "item_4" || claimed.IsError || !strings.Contains(claimed.Text, `"owner":"run:codex"`) {
		t.Errorf("claim result = %+v", claimed)
	}
	shell, shellOut := events[9], events[10]
	if shell.Tool != "shell" || !strings.Contains(shell.Input, "Test-Path -LiteralPath hello.txt") ||
		shellOut.Text != "exists=False\r\n" || shellOut.IsError {
		t.Errorf("shell = %+v / %+v", shell, shellOut)
	}
	patch, patched := events[11], events[12]
	if patch.Tool != "apply_patch" || patch.Input != `add C:\work\worktrees\run1\hello.txt` || patched.Text != patch.Input ||
		patched.IsError {
		t.Errorf("file change = %+v / %+v", patch, patched)
	}
	refused := events[17]
	if refused.ToolID != "item_10" || !refused.IsError || !strings.HasPrefix(refused.Text, "status must be done") {
		t.Errorf("refused MCP call = %+v", refused)
	}
	if events[19].IsError {
		t.Errorf("retried MCP call = %+v", events[19])
	}
	usage := events[len(events)-1].Usage
	if usage == nil || usage.InputTokens != 183170-155648 || usage.CacheReadTokens != 155648 || usage.OutputTokens != 1703 ||
		usage.CostUSD != 0 {
		t.Errorf("usage = %+v", usage)
	}
	if !outcome.Done || outcome.IsError || !strings.HasPrefix(outcome.Summary, "Done. Added [hello.txt]") ||
		outcome.Usage == nil || *outcome.Usage != *usage {
		t.Errorf("outcome = %+v", outcome)
	}
}

// codex_model_error.jsonl is real too: the configured model is refused.
func TestCodexParseModelError(t *testing.T) {
	events, outcome := parseWith(t, Codex{}.NewParser(), "codex_model_error.jsonl")
	want := []string{runnerwire.EventLog, runnerwire.EventLog, runnerwire.EventLog, runnerwire.EventLog, runnerwire.EventError}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v (turn.failed must not repeat the error)", got)
	}
	reason := "The 'gpt-6-astra' model is not supported when using Codex with a ChatGPT account. (HTTP 400)"
	if events[4].Text != reason {
		t.Errorf("error = %q", events[4].Text)
	}
	if !outcome.Done || !outcome.IsError || outcome.Error != reason || outcome.Usage != nil {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestCodexParseEdgeCases(t *testing.T) {
	p := Codex{}.NewParser()
	lines := []string{
		"not json",
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"r0","type":"reasoning","text":""}}`,
		`{"type":"item.completed","item":{"id":"r0","type":"reasoning","text":"**Planning** the change"}}`,
		`{"type":"item.updated","item":{"id":"t0","type":"todo_list","items":[{"text":"a","completed":true}]}}`,
		`{"type":"item.completed","item":{"id":"t0","type":"todo_list","items":[{"text":"a","completed":true},{"text":"b","completed":false}]}}`,
		// Completed without a start: the call is reported with the result.
		`{"type":"item.completed","item":{"id":"w0","type":"web_search","query":"codex exec json"}}`,
		`{"type":"item.completed","item":{"id":"c0","type":"command_execution","command":"go test","aggregated_output":"FAIL\n","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"c1","type":"command_execution","command":"rm -rf /","aggregated_output":"","exit_code":null,"status":"declined"}}`,
		`{"type":"item.completed","item":{"id":"m0","type":"mcp_tool_call","server":"nodevas","tool":"get_node","arguments":{"id":"x"},"result":null,"error":{"message":"hub unreachable"},"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"z0","type":"collab_tool_call"}}`,
		// A transient error, then the turn still completes.
		`{"type":"error","message":"Reconnecting... 1/5"}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}`,
		`{"type":"turn.completed","usage":{"input_tokens":5,"cached_input_tokens":0,"output_tokens":1}}`,
	}
	var events []runnerwire.RunEvent
	for _, line := range lines {
		events = append(events, p.Parse([]byte(line))...)
	}
	want := []string{
		runnerwire.EventLog, runnerwire.EventThinking, runnerwire.EventThinking,
		runnerwire.EventToolCall, runnerwire.EventToolResult, // web search
		runnerwire.EventToolCall, runnerwire.EventToolResult, // failed command
		runnerwire.EventToolCall, runnerwire.EventToolResult, // declined command
		runnerwire.EventToolCall, runnerwire.EventToolResult, // failed MCP call
		runnerwire.EventError, runnerwire.EventUsage, runnerwire.EventUsage,
	}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v", got, want)
	}
	if events[2].Text != "plan:\n[x] a\n[ ] b" {
		t.Errorf("todo list = %q", events[2].Text)
	}
	if events[3].Tool != "web_search" || events[3].Input != "codex exec json" || events[4].IsError {
		t.Errorf("web search = %+v / %+v", events[3], events[4])
	}
	if !events[6].IsError || events[6].Text != "FAIL\n(exit code 1)" {
		t.Errorf("failed command = %+v", events[6])
	}
	if !events[8].IsError || !strings.Contains(events[8].Text, "declined") {
		t.Errorf("declined command = %+v", events[8])
	}
	if events[9].Tool != "mcp__nodevas__get_node" || !events[10].IsError || events[10].Text != "hub unreachable" {
		t.Errorf("failed MCP call = %+v / %+v", events[9], events[10])
	}
	// Usage adds up over turns.
	if u := events[len(events)-1].Usage; u.InputTokens != 11 || u.CacheReadTokens != 4 || u.OutputTokens != 3 {
		t.Errorf("usage = %+v", u)
	}
	if out := p.Outcome(); !out.Done || out.IsError || out.Error != "" {
		t.Errorf("outcome after a transient error = %+v", out)
	}

	// An error and then nothing: the run failed with that error.
	p = Codex{}.NewParser()
	p.Parse([]byte(`{"type":"error","message":"stream disconnected before completion"}`))
	if out := p.Outcome(); out.Done || !out.IsError || out.Error != "stream disconnected before completion" {
		t.Errorf("outcome = %+v", out)
	}
	// turn.failed with nothing before it reports its own error.
	p = Codex{}.NewParser()
	if got := p.Parse([]byte(`{"type":"turn.failed","error":{"message":"usage limit reached"}}`)); len(got) != 1 ||
		got[0].Type != runnerwire.EventError || got[0].Text != "usage limit reached" {
		t.Errorf("turn.failed = %+v", got)
	}
}

func TestCodexCommand(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", NodeTitle: "Fix the thing", Prompt: "Do it \"now\" 100%", Model: "gpt-5.5"}
	env := RunEnv{
		Binary:  "/usr/bin/codex",
		Workdir: "/data/worktrees/run-1",
		Branch:  "agent/n-42-run1",
		TempDir: t.TempDir(),
		MCP: MCPServer{Command: "/opt/my tools/nodevas", Args: []string{"mcp", "--server", "http://127.0.0.1:1/r/x",
			"--project", "team's board", "--actor", "run:codex"}},
		PermissionMode: "read-only",
		// Claude's settings mean nothing here.
		AllowedTools: []string{"Bash(git *)"},
	}
	cmd, err := Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/codex", "exec", "--json", "--color", "never", "-s", "read-only", "-C", "/data/worktrees/run-1",
		"-m", "gpt-5.5",
		"-c", "approval_policy='never'",
		"-c", "mcp_servers.nodevas.command='/opt/my tools/nodevas'",
		"-c", `mcp_servers.nodevas.args=['mcp','--server','http://127.0.0.1:1/r/x','--project',"team's board",'--actor','run:codex']`,
		"-c", "mcp_servers.nodevas.default_tools_approval_mode='approve'",
		"-c", "mcp_servers.nodevas.tool_timeout_sec=900",
		"-"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("dir = %q", cmd.Dir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	for _, s := range []string{"node n-42", `claim_task with id "n-42"`, "Do it \"now\" 100%"} {
		if !strings.Contains(string(stdin), s) {
			t.Errorf("prompt missing %q", s)
		}
	}

	// "default" is no -m; no sandbox setting is workspace-write, never the
	// job's say; the owner can leave their config.toml out.
	job.Model = "default"
	env.PermissionMode = ""
	env.IgnoreUserConfig = true
	cmd, err = Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, " -m ") || !strings.Contains(args, "-s workspace-write") ||
		!strings.Contains(args, "--ignore-user-config") || !strings.HasSuffix(args, " -") {
		t.Errorf("args = %s", args)
	}

	if _, err := (Codex{}).Command(job, RunEnv{Binary: "codex"}); err == nil {
		t.Error("no workdir: no error")
	}
}

func TestTOMLStrings(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Program Files\nodevas.exe`: `'C:\Program Files\nodevas.exe'`,
		"it's":                         `"it's"`,
		"a\"b\\c\nd":                   `"a\"b\\c\nd"`,
		"bell\x07":                     `"bell\u0007"`,
		"":                             `''`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
	if got := tomlArray([]string{"a", "b c"}); got != "['a','b c']" {
		t.Errorf("tomlArray = %s", got)
	}
	if err := checkBatchArgs([]string{"exec", "C:\\x y\\z (1)", "a'b"}); err != nil {
		t.Errorf("safe args refused: %v", err)
	}
	for _, bad := range []string{"100%", "a&b", "a|b", "a^b", "<x>", `"q"`, "a\nb"} {
		if err := checkBatchArgs([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
