package adapter

import (
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestParsersReportSessionID(t *testing.T) {
	cases := []struct {
		name    string
		parser  func() Parser
		fixture string
		want    string
	}{
		{"claude stream", func() Parser { return Claude{}.NewParser() }, "claude_stream.jsonl", "fc97caf9-629c-414a-8b84-67ec6e88bd98"},
		{"claude error", func() Parser { return Claude{}.NewParser() }, "claude_error.jsonl", "0b3f2a1c-0000-4000-8000-000000000002"},
		{"codex stream", func() Parser { return Codex{}.NewParser() }, "codex_stream.jsonl", "01a169d3-4de5-7b12-b230-46d24af8c811"},
		{"opencode stream", func() Parser { return OpenCode{}.NewParser() }, "opencode_stream.jsonl", "ses_e9357fbabffendk108ceo7txuW"},
		{"opencode2 stream", func() Parser { return OpenCode{}.NewParser() }, "opencode2_stream.jsonl", "ses_e894d29c3ffePlUFojKaPwhqVA"},
		{"antigravity stream", func() Parser { return Antigravity{}.NewParser() }, "antigravity_stream.jsonl", "d8a7404f-c28f-48f9-bfe5-2c4e8fd8bc64"},
		{"gemini stream", func() Parser { return Gemini{}.NewParser() }, "gemini_stream.jsonl", "6f1d2c3b-gemini-fixture"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.parser()
			sr, ok := p.(SessionReporter)
			if !ok {
				t.Fatalf("%T does not implement SessionReporter", p)
			}
			parseWith(t, p, tc.fixture)
			if got := sr.Session(); got != tc.want {
				t.Errorf("Session() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("opencode acp stream", func(t *testing.T) {
		conv := testACPSession()
		sr, ok := conv.(SessionReporter)
		if !ok {
			t.Fatalf("%T does not implement SessionReporter", conv)
		}
		converse(t, conv, "opencode_acp_stream.jsonl")
		if got := sr.Session(); got != "ses_e8e098febffe77arUVHDyg67YN" {
			t.Errorf("Session() = %q, want ses_e8e098febffe77arUVHDyg67YN", got)
		}
	})

	t.Run("rejects invalid session ids", func(t *testing.T) {
		bad := []struct {
			name   string
			parser Parser
			line   string
		}{
			{"claude", Claude{}.NewParser(), `{"type":"system","subtype":"init","session_id":"--bad id"}`},
			{"codex", Codex{}.NewParser(), `{"type":"thread.started","thread_id":"bad;rm -rf /"}`},
			{"opencode", OpenCode{}.NewParser(), `{"type":"step_start","sessionID":"../escape"}`},
			{"antigravity", Antigravity{}.NewParser(), `{"event":"init","conversation_id":"-leading-dash"}`},
			{"gemini", Gemini{}.NewParser(), `{"type":"init","session_id":"has space"}`},
		}
		for _, tc := range bad {
			tc.parser.Parse([]byte(tc.line))
			if got := tc.parser.(SessionReporter).Session(); got != "" {
				t.Errorf("%s accepted invalid session id %q", tc.name, got)
			}
		}
	})
}

func followUpGlobalJob(agent, model string) runnerwire.Job {
	return runnerwire.Job{
		RunID:           "run-g2",
		NodeTitle:       "Make the button bigger",
		Prompt:          "Make the button bigger",
		Agent:           agent,
		Model:           model,
		Base:            "main",
		Global:          true,
		Slug:            "tidy-ui",
		ContinueOf:      "run-g1",
		WorkRoot:        "run-g1",
		ContinueTask:    "Build the button component",
		ContinueSummary: "Added Button.tsx and tests.",
	}
}

func TestClaudeResumeArgs(t *testing.T) {
	r, ok := For("claude").(Resumer)
	if !ok || !r.SupportsResume(false) || !r.SupportsResume(true) {
		t.Fatalf("claude Resumer support = (%v, headless=%v, interactive=%v), want true for both",
			ok, ok && r.SupportsResume(false), ok && r.SupportsResume(true))
	}

	const sid = "23178440-c2db-45a6-a50b-c9fb34fc1d8b"
	job := followUpGlobalJob("claude", "sonnet")
	env := globalEnv(t, plainBinary())
	env.AllowedTools = []string{"Bash(go test *)"}
	env.Resume = sid

	// Headless resume: -p --resume <id> ... --allowedTools ... with short prompt on stdin.
	cmd, err := Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(env.TempDir, "mcp.json")
	want := []string{
		plainBinary(), "-p", "--resume", sid,
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", DefaultClaudePermissionMode,
		"--mcp-config", mcpPath, "--strict-mcp-config",
		"--model", "sonnet",
		"--allowedTools", "Bash(go test *)",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("headless args = %q\nwant %q", cmd.Args, want)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	prompt := string(stdin)
	wantAll(t, "claude headless resume prompt", prompt, "Make the button bigger", "same worktree", "do not push")
	wantNone(t, "claude headless resume prompt", prompt, "You are running headless", "Earlier task")

	// Interactive resume: claude --resume <id> <prompt> ... (and ignores SessionID when Resume is set).
	env.SessionID = "11111111-2222-3333-4444-555555555555"
	tui, err := Claude{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := tui.Args[1:]
	if len(args) < 4 || args[0] != "--resume" || args[1] != sid {
		t.Fatalf("interactive args = %q, want leading --resume %s <prompt>", args, sid)
	}
	if slices.Contains(args, "--session-id") {
		t.Errorf("interactive resume must not pass --session-id: %q", args)
	}
	wantAll(t, "claude interactive resume prompt", args[2], "Make the button bigger", "same worktree", "do not push")
	wantNone(t, "claude interactive resume prompt", args[2], "running interactively for Nodevas")

	// Invalid resume ID is refused.
	env.Resume = "--bad id"
	if _, err := (Claude{}).Command(job, env); err == nil {
		t.Error("headless: invalid resume id was accepted")
	}
	if _, err := (Claude{}).InteractiveCommand(job, env); err == nil {
		t.Error("interactive: invalid resume id was accepted")
	}
}

func TestClaudeInteractiveSessionIDArg(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	job := globalJob()
	env := globalEnv(t, plainBinary())
	env.SessionID = uuid

	cmd, err := Claude{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	if len(args) < 4 || args[0] != "--session-id" || args[1] != uuid {
		t.Fatalf("args = %q, want leading --session-id %s <prompt>", args, uuid)
	}
	if slices.Contains(args, "--resume") {
		t.Errorf("fresh interactive run passed --resume: %q", args)
	}
	if !strings.Contains(args[2], "as a global agent") || !strings.Contains(args[2], "fix typos") {
		t.Errorf("prompt = %q", args[2])
	}

	env.SessionID = "bad;id"
	if _, err := (Claude{}).InteractiveCommand(job, env); err == nil {
		t.Error("invalid session id was accepted")
	}
}

func TestCodexResumeArgs(t *testing.T) {
	r, ok := For("codex").(Resumer)
	if !ok || !r.SupportsResume(false) || r.SupportsResume(true) {
		t.Fatalf("codex Resumer support = (%v, headless=%v, interactive=%v), want headless=true interactive=false",
			ok, ok && r.SupportsResume(false), ok && r.SupportsResume(true))
	}

	const tid = "01a1f900-3bbb-7d52-b70e-67c5b3133e46"
	job := followUpGlobalJob("codex", "gpt-5.5")
	env := globalEnv(t, "/usr/bin/codex")
	env.PermissionMode = "read-only"
	env.IgnoreUserConfig = true
	env.Resume = tid

	cmd, err := Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/codex", "exec", "resume", "--json",
		"-m", "gpt-5.5",
		"--ignore-user-config",
		"-c", "sandbox_mode='read-only'",
		"-c", "approval_policy='never'",
		tid, "-",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	for _, forbidden := range []string{"-s", "--sandbox", "-C", "--color"} {
		if slices.Contains(cmd.Args, forbidden) {
			t.Errorf("codex exec resume must not pass %s: %q", forbidden, cmd.Args)
		}
	}
	if cmd.Dir != env.Workdir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, env.Workdir)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	wantAll(t, "codex resume prompt", string(stdin), "Make the button bigger", "same worktree", "do not push")
	wantNone(t, "codex resume prompt", string(stdin), "Earlier task")

	// Default sandbox goes through -c sandbox_mode='workspace-write'.
	env.PermissionMode = ""
	job.Model = "default"
	cmd, err = Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "sandbox_mode='workspace-write'") || slices.Contains(cmd.Args, "-m") {
		t.Errorf("default sandbox args = %q", cmd.Args)
	}

	// Interactive codex has no resume: ignores env.Resume and uses the fallback prompt.
	tui, err := Codex{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tui.Args, "resume") || slices.Contains(tui.Args, tid) {
		t.Errorf("interactive codex should not resume: %q", tui.Args)
	}
	wantAll(t, "codex interactive fallback", tui.Args[len(tui.Args)-1], "continuing your earlier work", "Build the button component", "Make the button bigger")

	// Invalid resume ID is refused.
	env.Resume = "bad id"
	if _, err := (Codex{}).Command(job, env); err == nil {
		t.Error("invalid resume id was accepted")
	}
}

func TestOpenCodeSessionArg(t *testing.T) {
	r, ok := For("opencode").(Resumer)
	if !ok || !r.SupportsResume(false) || !r.SupportsResume(true) {
		t.Fatalf("opencode Resumer support = (%v, headless=%v, interactive=%v), want true for both",
			ok, ok && r.SupportsResume(false), ok && r.SupportsResume(true))
	}

	const sid = "ses_e9357fbabffendk108ceo7txuW"
	job := followUpGlobalJob("opencode", "opencode/big-pickle")

	for _, version := range []string{"opencode 1.18.23", "opencode v2.1.4"} {
		t.Run(version, func(t *testing.T) {
			env := globalEnv(t, "/usr/bin/opencode")
			env.Version = version
			env.Resume = sid

			cmd, err := OpenCode{}.Command(job, env)
			if err != nil {
				t.Fatal(err)
			}
			idx := slices.Index(cmd.Args, "--session")
			if idx < 0 || idx+1 >= len(cmd.Args) || cmd.Args[idx+1] != sid {
				t.Fatalf("headless args = %q, want --session %s", cmd.Args, sid)
			}
			stdin, _ := io.ReadAll(cmd.Stdin)
			wantAll(t, "opencode headless resume prompt", string(stdin), "Make the button bigger", "same worktree", "do not push")

			tui, err := OpenCode{}.InteractiveCommand(job, env)
			if err != nil {
				t.Fatal(err)
			}
			idx = slices.Index(tui.Args, "--session")
			if idx < 0 || idx+1 >= len(tui.Args) || tui.Args[idx+1] != sid {
				t.Fatalf("interactive args = %q, want --session %s", tui.Args, sid)
			}
			pIdx := slices.Index(tui.Args, "--prompt")
			if pIdx < 0 || pIdx+1 >= len(tui.Args) {
				t.Fatalf("interactive args missing --prompt: %q", tui.Args)
			}
			wantAll(t, "opencode interactive resume prompt", tui.Args[pIdx+1], "Make the button bigger", "same worktree")
		})
	}

	env := globalEnv(t, "/usr/bin/opencode")
	env.Resume = "--bad"
	if _, err := (OpenCode{}).Command(job, env); err == nil {
		t.Error("headless: invalid resume id was accepted")
	}
	if _, err := (OpenCode{}).InteractiveCommand(job, env); err == nil {
		t.Error("interactive: invalid resume id was accepted")
	}
}

func TestAgyConversationArg(t *testing.T) {
	r, ok := For("antigravity").(Resumer)
	if !ok || !r.SupportsResume(false) || !r.SupportsResume(true) {
		t.Fatalf("antigravity Resumer support = (%v, headless=%v, interactive=%v), want true for both",
			ok, ok && r.SupportsResume(false), ok && r.SupportsResume(true))
	}

	const cid = "00782da5-b17f-4161-8af6-bf1d3c717845"
	job := followUpGlobalJob("antigravity", "gemini-3.8-flash-low")
	env := globalEnv(t, "/usr/bin/agy")
	env.Resume = cid

	cmd, err := Antigravity{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "--conversation="+cid) || cmd.Args[len(cmd.Args)-1] != "-p=" {
		t.Fatalf("headless args = %q, want --conversation=%s and trailing -p=", cmd.Args, cid)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	var input struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(stdin, &input); err != nil {
		t.Fatalf("stdin = %q: %v", stdin, err)
	}
	wantAll(t, "agy headless resume prompt", input.Message.Content, "Make the button bigger", "same worktree", "do not push")

	tui, err := Antigravity{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tui.Args, "--conversation="+cid) {
		t.Fatalf("interactive args = %q, want --conversation=%s", tui.Args, cid)
	}
	wantAll(t, "agy interactive resume prompt", tui.Args[1], "-i=", "Make the button bigger", "same worktree")

	env.Resume = "bad/id"
	if _, err := (Antigravity{}).Command(job, env); err == nil {
		t.Error("headless: invalid resume id was accepted")
	}
	if _, err := (Antigravity{}).InteractiveCommand(job, env); err == nil {
		t.Error("interactive: invalid resume id was accepted")
	}
}

func TestACPLoadSession(t *testing.T) {
	r, ok := any(OpenCodeACP{}).(Resumer)
	if !ok || !r.SupportsResume(false) || !r.SupportsResume(true) {
		t.Fatalf("OpenCodeACP Resumer support = (%v, headless=%v, interactive=%v), want true for both",
			ok, ok && r.SupportsResume(false), ok && r.SupportsResume(true))
	}

	const prevID = "ses_prev_123"
	job := followUpGlobalJob("opencode", "default")
	env := globalEnv(t, "/usr/bin/opencode")
	env.Resume = prevID

	t.Run("loadSession supported", func(t *testing.T) {
		conv := OpenCodeACP{}.NewConversation(job, env)
		stdin := newRecordingStdin()
		conv.Begin(stdin)

		// initialize advertises loadSession: true
		conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"agentInfo":{"name":"OpenCode","version":"1.18.20"}}}`))
		// Replayed history while session/load is in flight must not be emitted or pollute summary.
		if evs := conv.Parse([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_prev_123","update":{"sessionUpdate":"agent_message_chunk","messageId":"old","content":{"type":"text","text":"old turn replay"}}}}`)); len(evs) != 0 {
			t.Fatalf("replayed update emitted during load: %+v", evs)
		}
		// session/load succeeds (ACP returns {} or configOptions without repeating sessionId)
		loadEvents := conv.Parse([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		if len(loadEvents) != 1 || !strings.Contains(loadEvents[0].Text, prevID) {
			t.Errorf("load events = %+v", loadEvents)
		}
		// Turn 2 answer and finish
		conv.Parse([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_prev_123","update":{"sessionUpdate":"agent_message_chunk","messageId":"new","content":{"type":"text","text":"Made the button bigger."}}}}`))
		conv.Parse([]byte(`{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`))

		sent := stdin.messages(t)
		if len(sent) != 3 {
			t.Fatalf("sent %d messages: %v", len(sent), sent)
		}
		if sent[0]["method"] != "initialize" || sent[1]["method"] != "session/load" || sent[2]["method"] != "session/prompt" {
			t.Fatalf("methods = [%v, %v, %v], want [initialize, session/load, session/prompt]",
				sent[0]["method"], sent[1]["method"], sent[2]["method"])
		}
		loadParams := sent[1]["params"].(map[string]any)
		if loadParams["sessionId"] != prevID || loadParams["cwd"] != env.Workdir {
			t.Errorf("session/load params = %v", loadParams)
		}
		promptParams := sent[2]["params"].(map[string]any)
		promptText := promptParams["prompt"].([]any)[0].(map[string]any)["text"].(string)
		if promptParams["sessionId"] != prevID {
			t.Errorf("session/prompt sessionId = %v, want %s", promptParams["sessionId"], prevID)
		}
		wantAll(t, "acp resume prompt", promptText, "Make the button bigger", "same worktree", "do not push")
		wantNone(t, "acp resume prompt", promptText, "Earlier task")

		if got := conv.(SessionReporter).Session(); got != prevID {
			t.Errorf("Session() = %q, want %q", got, prevID)
		}
		if out := conv.Outcome(); !out.Done || out.IsError || out.Summary != "Made the button bigger." {
			t.Errorf("outcome = %+v", out)
		}
	})

	t.Run("loadSession unsupported falls back to session/new", func(t *testing.T) {
		conv := OpenCodeACP{}.NewConversation(job, env)
		stdin := newRecordingStdin()
		conv.Begin(stdin)

		conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":false}}}`))
		conv.Parse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"ses_fresh_456"}}`))
		conv.Parse([]byte(`{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`))

		sent := stdin.messages(t)
		if len(sent) != 3 {
			t.Fatalf("sent %d messages: %v", len(sent), sent)
		}
		if sent[1]["method"] != "session/new" || sent[2]["method"] != "session/prompt" {
			t.Fatalf("methods = [%v, %v, %v], want [initialize, session/new, session/prompt]",
				sent[0]["method"], sent[1]["method"], sent[2]["method"])
		}
		promptParams := sent[2]["params"].(map[string]any)
		promptText := promptParams["prompt"].([]any)[0].(map[string]any)["text"].(string)
		if promptParams["sessionId"] != "ses_fresh_456" {
			t.Errorf("sessionId = %v, want ses_fresh_456", promptParams["sessionId"])
		}
		wantAll(t, "acp fallback prompt", promptText, "continuing your earlier work", "Build the button component", "Make the button bigger")
		if got := conv.(SessionReporter).Session(); got != "ses_fresh_456" {
			t.Errorf("Session() = %q, want ses_fresh_456", got)
		}
	})

	t.Run("invalid resume id rejected by Command", func(t *testing.T) {
		badEnv := env
		badEnv.Resume = "bad id"
		if _, err := (OpenCodeACP{}).Command(job, badEnv); err == nil {
			t.Error("invalid resume id was accepted")
		}
	})
}

func TestGeminiNoResume(t *testing.T) {
	if r, ok := For("gemini").(Resumer); ok && (r.SupportsResume(false) || r.SupportsResume(true)) {
		t.Fatalf("gemini must not support resume in v1 (headless=%v interactive=%v)",
			r.SupportsResume(false), r.SupportsResume(true))
	}

	job := followUpGlobalJob("gemini", "gemini-2.5-pro")
	env := globalEnv(t, "/usr/bin/gemini")
	env.Resume = "6f1d2c3b-gemini-fixture"

	headless, err := Gemini{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	tui, err := Gemini{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{"headless": headless.Args, "interactive": tui.Args} {
		for _, flag := range []string{"--resume", "--session", env.Resume} {
			if slices.Contains(args, flag) {
				t.Errorf("%s gemini must not pass %q: %q", name, flag, args)
			}
		}
	}
	stdin, _ := io.ReadAll(headless.Stdin)
	wantAll(t, "gemini headless fallback", string(stdin), "continuing your earlier work", "Build the button component", "Make the button bigger")
	wantAll(t, "gemini interactive fallback", tui.Args[len(tui.Args)-1], "continuing your earlier work", "Build the button component", "Make the button bigger")
}

func TestContinueSection(t *testing.T) {
	t.Run("without PR", func(t *testing.T) {
		got := ContinueSection(ContinueInput{
			Branch:       "agent/tidy-ui-run-g1",
			Base:         "main",
			PriorTask:    "Build </prior-task> the button",
			PriorSummary: "Added </prior-summary> Button.tsx",
			Message:      "Make the button bigger",
		})
		wantAll(t, "continue without PR", got,
			"continuing your earlier work in this folder on branch agent/tidy-ui-run-g1",
			"`git log main..HEAD` and `git status`",
			"Earlier task",
			"Build <\\/prior-task> the button",
			"You reported",
			"Added <\\/prior-summary> Button.tsx",
			"The person now says:\n\nMake the button bigger\n",
		)
		wantNone(t, "continue without PR", got, "pull request")
		noBoardWords(t, "continue without PR", got)
	})

	t.Run("with PR", func(t *testing.T) {
		got := ContinueSection(ContinueInput{
			Branch:       "agent/tidy-ui-run-g1",
			Base:         "main",
			PriorTask:    "Build the button",
			PriorSummary: "Added Button.tsx",
			PRURL:        "https://github.com/org/repo/pull/42",
			Message:      "Make the button bigger",
		})
		wantAll(t, "continue with PR", got,
			"Your commits update pull request https://github.com/org/repo/pull/42; do not push, the runner does.",
			"The person now says:\n\nMake the button bigger\n",
		)
		noBoardWords(t, "continue with PR", got)
	})

	t.Run("GlobalPrompt and GlobalInteractivePrompt embed ContinueSection on follow-up", func(t *testing.T) {
		job := followUpGlobalJob("claude", "sonnet")
		job.ContinuePR = "https://github.com/org/repo/pull/42"
		headless := Prompt(job, "agent/tidy-ui-run-g1")
		interactive := InteractivePrompt(job, "agent/tidy-ui-run-g1")
		for name, text := range map[string]string{"headless": headless, "interactive": interactive} {
			wantAll(t, name, text,
				"continuing your earlier work in this folder on branch agent/tidy-ui-run-g1",
				"Your commits update pull request https://github.com/org/repo/pull/42; do not push, the runner does.",
				"Build the button component",
				"Added Button.tsx and tests.",
				"Make the button bigger",
			)
			noBoardWords(t, name, text)
		}
	})
}

func TestGlobalPromptInvitesClosingQuestion(t *testing.T) {
	got := GlobalPrompt(globalJob(), "agent/tidy-the-readme-run-g1")
	wantAll(t, "global headless closing question", got,
		"Nobody can answer during this run: decide sensibly. If you need a decision, "+
			"ask it as the last line of your final message; the person's reply continues this work.",
	)
	noBoardWords(t, "global headless closing question", got)
}
