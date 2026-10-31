package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
)

// Antigravity runs Google's Antigravity CLI (agy) headless:
//
//	agy --input-format=stream-json --output-format=stream-json --mode=<mode>
//	    [--dangerously-skip-permissions] [--model=<id>] --add-dir=<root>
//	    --disable-slash-commands [--settings=<file>] -p=
//
// with the prompt on stdin as one stream-json message,
// {"event":"user","message":{"content":"…"}}. -p takes the prompt as its own
// value, so it is given empty and comes last. Checked against agy 1.4.6's
// --help and real runs (testdata/antigravity_*.jsonl).
//
// agy reads MCP servers from customization roots only: the owner's global
// ~/.gemini/config and any directory added to the workspace. So the run's
// nodevas server goes in <temp dir>/agyroot/.agents/mcp_config.json, and that
// root is added with --add-dir; the worktree stays clean. An entry of the
// same name in the global config wins over it, which is why the run's
// `nodevas mcp` arguments also go in RunMCPEnv: `nodevas mcp` started by a
// global "nodevas" entry (from `nodevas setup-agent`) takes them from there
// and lands on this run's proxy too.
//
// Headless, a tool that needs permission is denied without asking, MCP tools
// included, unless the owner's ~/.gemini/antigravity-cli/settings.json allows
// it: the runner's detection requires "mcp(nodevas/*)" there (setup-agent
// adds it) and never writes that file itself. With permissionRelay, the
// session's --settings grant shell commands to the gate that asks Nodevas
// (writeAntigravityRelaySettings).
type Antigravity struct{}

// AntigravityModes are the runner's settings for agy runs: agy's own --mode
// choices (plan, accept-edits, auto) and full, which is accept-edits with
// --dangerously-skip-permissions: every tool runs without asking.
var AntigravityModes = []string{"plan", "accept-edits", "auto", "full"}

// DefaultAntigravityMode lets the agent edit files; anything else that needs
// permission (shell commands) is denied, as nobody can approve it headless.
const DefaultAntigravityMode = "accept-edits"

// RunMCPEnv carries a run's `nodevas mcp` arguments (a JSON array starting
// with "mcp"). `nodevas mcp` uses them in place of its own command line when
// it is set.
const RunMCPEnv = "NODEVAS_RUN_MCP"

// RunNodeEnv carries a run's node id to the agent's hooks (the agy gate).
const RunNodeEnv = "NODEVAS_RUN_NODE"

// antigravityRootDir is the customization root inside the run's temp dir.
const antigravityRootDir = "agyroot"

// antigravitySettingsFile is the relay's session settings in the run's temp
// dir (writeAntigravityRelaySettings).
const antigravitySettingsFile = "agysettings.json"

// antigravityToolTimeoutSeconds bounds one nodevas MCP tool call. agy's own
// default cuts a call off after three minutes ("MCP tool call to server
// \"nodevas\" timed out after 3m0s"); ask_user waits up to ten for a person.
// timeoutSeconds is the server entry's key agy 1.4.x reads for it (checked
// against agy 1.4.12: 5 cut a 20 s call at 5 s, 240 let a 200 s call
// finish). Gemini CLI and OpenCode get the same 15 minutes.
const antigravityToolTimeoutSeconds = 900

// AntigravityCommandRule is agy's permission rule for every shell command.
const AntigravityCommandRule = "command(*)"

func (Antigravity) Kind() string { return "antigravity" }

func (Antigravity) NewParser() Parser { return &antigravityParser{} }

func (Antigravity) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" || env.TempDir == "" {
		return nil, fmt.Errorf("antigravity: binary, workdir and temp dir are required")
	}
	// Headless, nobody can approve a command: the gate turns what agy would
	// refuse, ending the turn, into a denial the agent can work around.
	readOnly := ReadOnly(job)
	relay := env.PermissionRelay && !readOnly && PermissionRelayApplies("antigravity", env.PermissionMode, false)
	var settings string
	if relay {
		var err error
		if settings, err = writeAntigravityRelaySettings(env.TempDir); err != nil {
			return nil, err
		}
		// Without session settings there is no allow rule, and agy refuses
		// a command even after a person allowed it: nobody is asked in vain,
		// the plain gate denies it instead.
		relay = settings != ""
	}
	root, relayGate, err := writeAntigravityRoot(env, antigravityGated(env.PermissionMode) || readOnly, readOnly, relay)
	if err != nil {
		return nil, err
	}
	if !relayGate && settings != "" {
		// The grant goes only with the gate that asks about every command
		// (none when no hook can run the nodevas binary).
		_ = os.Remove(settings)
		settings = ""
	}
	modeArgs, err := antigravityModeArgs(env.PermissionMode)
	if err != nil {
		return nil, err
	}
	args := []string{"--input-format=stream-json", "--output-format=stream-json"}
	args = append(args, modeArgs...)
	if job.Model != "" && job.Model != "default" {
		args = append(args, "--model="+job.Model)
	}
	args = append(args, "--add-dir="+root, "--disable-slash-commands")
	if settings != "" {
		args = append(args, "--settings="+settings)
	}
	args = append(args, "-p=")
	input, err := json.Marshal(map[string]any{
		"event":   "user",
		"message": map[string]string{"content": Prompt(job, env.Branch)},
	})
	if err != nil {
		return nil, err
	}
	extra, err := antigravityEnv(job, env)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	cmd.Env = jobEnv(job, extra...)
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("antigravity: %w", err)
	}
	return cmd, nil
}

// antigravityModeArgs are the flags for the owner's mode setting.
func antigravityModeArgs(mode string) ([]string, error) {
	if mode == "" {
		mode = DefaultAntigravityMode
	}
	switch mode {
	case "plan", "accept-edits", "auto":
		return []string{"--mode=" + mode}, nil
	case "full":
		return []string{"--mode=accept-edits", "--dangerously-skip-permissions"}, nil
	}
	return nil, fmt.Errorf("antigravity: unknown mode %q (want one of %s)", mode, strings.Join(AntigravityModes, ", "))
}

// antigravityEnv is what the agy process gets on top of jobEnv: the run's MCP
// arguments and its node, which agy hands its hooks too (the gate's asks name
// the node), and the session id its `nodevas mcp` reports (RunSessionEnv),
// which the gate's asks carry too.
func antigravityEnv(job runnerwire.Job, env RunEnv) ([]string, error) {
	args, err := json.Marshal(env.MCP.Args)
	if err != nil {
		return nil, err
	}
	return append([]string{RunMCPEnv + "=" + string(args), RunNodeEnv + "=" + job.NodeID}, runSessionEnv(env)...), nil
}

// writeAntigravityRoot writes the run's customization root, which defines the
// nodevas MCP server (and, with gate, the PreToolUse gate in
// antigravity_gate.go; relay has it ask Nodevas about shell commands), into
// the temp dir and returns its path. relayGate reports whether the gate that
// asks Nodevas is in it (relay outside a read-only run, and a hook path for
// the nodevas binary).
func writeAntigravityRoot(env RunEnv, gate, readOnly, relay bool) (root string, relayGate bool, err error) {
	root = filepath.Join(env.TempDir, antigravityRootDir)
	dir := filepath.Join(root, ".agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, fmt.Errorf("making the Antigravity config dir: %w", err)
	}
	config, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"nodevas": map[string]any{
				"command": env.MCP.Command,
				"args":    env.MCP.Args,
				"env":     map[string]string{"NODEVAS_AGENT": "antigravity"},
				// agy's three minutes would cut ask_user's wait short.
				"timeoutSeconds": antigravityToolTimeoutSeconds,
			},
		},
	}, "", "  ")
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_config.json"), config, 0o600); err != nil {
		return "", false, fmt.Errorf("writing the Antigravity MCP config: %w", err)
	}
	if hooks := antigravityGateHooks(env.MCP.Command, readOnly, relay); gate && hooks != nil {
		data, err := json.MarshalIndent(hooks, "", "  ")
		if err != nil {
			return "", false, err
		}
		if err := os.WriteFile(filepath.Join(dir, "hooks.json"), data, 0o600); err != nil {
			return "", false, fmt.Errorf("writing the Antigravity hooks: %w", err)
		}
		relayGate = relay && !readOnly
	}
	return root, relayGate, nil
}

// antigravityOwnerSettingsPath is the owner's agy settings file, whose
// permission rules the relay's session settings carry over. Tests point it
// elsewhere.
var antigravityOwnerSettingsPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), nil
}

// writeAntigravityRelaySettings writes the session settings of a run whose
// gate asks Nodevas about shell commands, and returns their path, or "" for
// none. Headless, agy refuses a command that needs permission even after a
// PreToolUse hook said "allow" (agy 1.4.10: "user denied permission to run
// command"), and only an allow rule grants it; so the session gets
// AntigravityCommandRule and the gate, which sees every run_command and
// send_command_input first, is the only say: its own denials, then the
// person's answer, a denial for anything else (agy also refuses the call when
// the hook fails, times out or prints nothing it can read). --settings
// replaces the owner's permissions for the session, so their rules (the
// nodevas MCP allow rule, any deny rules) are carried over; an owner file
// whose permissions do not read as such gets no grant, which leaves the
// owner's rules to agy as they are and the run to the plain gate.
func writeAntigravityRelaySettings(tempDir string) (string, error) {
	perms := map[string]any{}
	if path, err := antigravityOwnerSettingsPath(); err == nil {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			var owner struct {
				Permissions json.RawMessage `json:"permissions"`
			}
			if json.Unmarshal(bytes.TrimPrefix(raw, utf8BOM), &owner) != nil {
				return "", nil
			}
			if len(owner.Permissions) > 0 && string(owner.Permissions) != "null" {
				if json.Unmarshal(owner.Permissions, &perms) != nil || perms == nil {
					return "", nil
				}
			}
		case !os.IsNotExist(err):
			return "", nil
		}
	}
	var allow []any
	if v, ok := perms["allow"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return "", nil
		}
		allow = list
	}
	if !slices.Contains(allow, any(AntigravityCommandRule)) {
		allow = append(allow, AntigravityCommandRule)
	}
	perms["allow"] = allow
	data, err := json.MarshalIndent(map[string]any{"permissions": perms}, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(tempDir, antigravitySettingsFile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("writing the Antigravity session settings: %w", err)
	}
	return path, nil
}

// antigravityParser reads `--output-format stream-json`: one JSON object per
// line, keyed by "event": init, step_update (one per change of a step: the
// user's input, an agent response streaming text deltas, a tool call going
// ACTIVE then DONE or ERROR) and a closing result with the turn's response,
// usage and any actions headless mode denied.
type antigravityParser struct {
	outcome Outcome
	// model is the model the init line named.
	model string
	// textStep is the agent_response step whose deltas are in pending.
	textStep int
	pending  strings.Builder
	// lastText is the latest finished agent text: the summary when the
	// result has no response.
	lastText string
	// called holds the steps whose tool_call went out at ACTIVE.
	called map[int]bool
}

type antigravityLine struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Model string `json:"model"`
	} `json:"init"`
	StepUpdate *antigravityStep   `json:"step_update"`
	Result     *antigravityResult `json:"result"`
}

type antigravityStep struct {
	StepIndex int    `json:"step_index"`
	State     string `json:"state"`
	StepType  string `json:"step_type"`
	TextDelta string `json:"text_delta"`
	ToolName  string `json:"tool_name"`
	ToolInfo  *struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
		Output     string          `json:"output"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"tool_info"`
}

type antigravityResult struct {
	Status        string            `json:"status"`
	Response      string            `json:"response"`
	Error         string            `json:"error"`
	Usage         *antigravityUsage `json:"usage"`
	DeniedActions []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

type antigravityUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

func (p *antigravityParser) Outcome() Outcome {
	out := p.outcome
	summary := out.Summary
	if summary == "" {
		summary = p.lastText
		if p.pending.Len() > 0 {
			summary = p.pending.String()
		}
	}
	out.Summary = Truncate(summary, runnerwire.MaxSummaryBytes)
	out.Text = Truncate(summary, MaxTextBytes)
	return out
}

// flush turns the gathered deltas into one text event.
func (p *antigravityParser) flush() []runnerwire.RunEvent {
	text := p.pending.String()
	p.pending.Reset()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	p.lastText = text
	return []runnerwire.RunEvent{{Type: runnerwire.EventText, Text: clip(text)}}
}

func (p *antigravityParser) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg antigravityLine
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil {
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	switch msg.Event {
	case "init":
		if msg.Init != nil {
			p.model = strings.TrimSpace(msg.Init.Model)
		}
		text := "antigravity session started"
		if msg.ConversationID != "" {
			text += " " + msg.ConversationID
		}
		if msg.Init != nil && msg.Init.Model != "" {
			text += " (model " + msg.Init.Model + ")"
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: text}}
	case "step_update":
		if msg.StepUpdate != nil {
			return p.step(msg.StepUpdate)
		}
	case "result":
		if msg.Result != nil {
			return append(p.flush(), p.result(msg.Result)...)
		}
	}
	return nil
}

func (p *antigravityParser) step(s *antigravityStep) []runnerwire.RunEvent {
	var events []runnerwire.RunEvent
	if p.pending.Len() > 0 && s.StepIndex != p.textStep {
		events = p.flush()
	}
	switch s.StepType {
	case "agent_response":
		if s.TextDelta != "" && p.pending.Len() < 1<<20 {
			p.textStep = s.StepIndex
			p.pending.WriteString(s.TextDelta)
		}
		if s.State == "DONE" && s.StepIndex == p.textStep {
			events = append(events, p.flush()...)
		}
	case "tool":
		events = append(events, p.tool(s)...)
	}
	// user_input, error_message (the result says what went wrong): no event.
	return events
}

func (p *antigravityParser) tool(s *antigravityStep) []runnerwire.RunEvent {
	id := strconv.Itoa(s.StepIndex)
	name, input := s.ToolName, ""
	if info := s.ToolInfo; info != nil {
		if name == "" {
			name = info.Name
		}
		name, input = antigravityToolName(name, info.Parameters)
	}
	call := runnerwire.RunEvent{Type: runnerwire.EventToolCall, Tool: name, ToolID: id, Input: clip(input)}
	if s.State == "ACTIVE" {
		if p.called == nil {
			p.called = map[int]bool{}
		}
		if p.called[s.StepIndex] {
			return nil
		}
		p.called[s.StepIndex] = true
		return []runnerwire.RunEvent{call}
	}
	if s.State != "DONE" && s.State != "ERROR" {
		return nil
	}
	var events []runnerwire.RunEvent
	if !p.called[s.StepIndex] {
		events = append(events, call)
	}
	delete(p.called, s.StepIndex)
	result := runnerwire.RunEvent{Type: runnerwire.EventToolResult, ToolID: id, IsError: s.State == "ERROR"}
	if info := s.ToolInfo; info != nil {
		result.Text = info.Output
		if info.Error != nil && info.Error.Message != "" {
			result.IsError = true
			result.Text = info.Error.Message
		}
	}
	result.Text = clip(result.Text)
	return append(events, result)
}

// antigravityToolName names a tool call for the UI. MCP calls all go through
// call_mcp_tool; they are named the way Claude Code names MCP tools, with
// their own arguments as the input.
func antigravityToolName(name string, params json.RawMessage) (string, string) {
	if name == "call_mcp_tool" {
		var mcp struct {
			ServerName string          `json:"ServerName"`
			ToolName   string          `json:"ToolName"`
			Arguments  json.RawMessage `json:"Arguments"`
		}
		if json.Unmarshal(params, &mcp) == nil && mcp.ServerName != "" && mcp.ToolName != "" {
			return "mcp__" + mcp.ServerName + "__" + mcp.ToolName, compactJSON(mcp.Arguments)
		}
	}
	return name, compactJSON(params)
}

func (p *antigravityParser) result(r *antigravityResult) []runnerwire.RunEvent {
	p.outcome.Done = true
	p.outcome.Summary = r.Response
	if strings.TrimSpace(r.Response) != "" {
		p.lastText = r.Response
	}
	var events []runnerwire.RunEvent
	if len(r.DeniedActions) > 0 {
		denied := make([]string, 0, len(r.DeniedActions))
		for _, a := range r.DeniedActions {
			item := a.Action
			if a.DisplayName != "" {
				item += " (" + a.DisplayName + ")"
			}
			denied = append(denied, item)
		}
		// A denial ends agy's turn there and then, whatever was left to do:
		// the run did not finish its task, though agy says SUCCESS.
		reason := "antigravity stopped after denying without asking (headless): " + strings.Join(denied, ", ")
		p.outcome.IsError = true
		p.outcome.Error = Truncate(reason, runnerwire.MaxSummaryBytes)
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
	}
	if u := r.Usage; u != nil {
		// agy's input_tokens include the cached ones; input here is the
		// uncached part, as for the other agents. output_tokens include the
		// thinking tokens already.
		usage := &runnerwire.Usage{
			InputTokens:     max(u.InputTokens-u.CacheReadTokens, 0),
			OutputTokens:    u.OutputTokens,
			CacheReadTokens: u.CacheReadTokens,
			Model:           p.model,
		}
		p.outcome.Usage = usage
		copied := *usage
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &copied})
	}
	if r.Status != "" && r.Status != "SUCCESS" {
		reason := strings.TrimSpace(r.Error)
		if reason == "" {
			reason = "antigravity reported an error (" + r.Status + ")"
		}
		p.outcome.IsError = true
		p.outcome.Error = Truncate(reason, runnerwire.MaxSummaryBytes)
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
	}
	return events
}
