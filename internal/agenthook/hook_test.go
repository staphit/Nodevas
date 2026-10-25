package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	agentsapi "nodevas/internal/httpapi/agents"
)

func TestParseInput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Input
	}{
		{"claude", `{"session_id":"abc","cwd":"/w","stop_hook_active":true}`, Input{SessionID: "abc", Cwd: "/w", StopHookActive: true}},
		{"bom", "\xef\xbb\xbf" + `{"session_id":"abc"}`, Input{SessionID: "abc"}},
		{"camel", `{"sessionId":"g1"}`, Input{SessionID: "g1"}},
		{"conversation", `{"conversation_id":"c1"}`, Input{SessionID: "c1"}},
		{"antigravity", `{"conversationId":"ag1","workspacePaths":["/extra","/repo"],"modelName":"gemini-3.8-flash-low"}`,
			Input{SessionID: "ag1", Cwd: filepath.Clean("/repo"), Model: "gemini-3.8-flash-low"}},
		{"antigravity auto model", `{"conversationId":"ag1","modelName":"auto"}`, Input{SessionID: "ag1"}},
		{"antigravity first stop", `{"conversationId":"ag1","executionNum":0,"terminationReason":"NO_TOOL_CALL"}`, Input{SessionID: "ag1"}},
		{"antigravity stop after a block", `{"conversationId":"ag1","executionNum":1,"terminationReason":"NO_TOOL_CALL"}`,
			Input{SessionID: "ag1", StopHookActive: true}},
		{"antigravity stop on error", `{"conversationId":"ag1","executionNum":0,"terminationReason":"ERROR"}`,
			Input{SessionID: "ag1", StopHookActive: true}},
		{"antigravity tool", `{"conversationId":"ag1","toolCall":{"name":"write_to_file","args":{"TargetFile":"/repo/a.go"}}}`,
			Input{SessionID: "ag1", ToolName: "write_to_file", Files: []string{"/repo/a.go"}}},
		{"garbage", `not json`, Input{}},
		{"empty", ``, Input{}},
	}
	for _, tc := range cases {
		if got := ParseInput([]byte(tc.raw)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ParseInput = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		agent, event, want string
	}{
		{"claude", EventSessionStart, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"SessionStart"}}`},
		{"claude", EventUserPromptSubmit, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"UserPromptSubmit"}}`},
		{"codex", EventPostToolUse, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"PostToolUse"}}`},
		{"codex", EventStop, `{"decision":"block","reason":"hi"}`},
		{"gemini", EventSessionStart, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"SessionStart"}}`},
		{"gemini", EventUserPromptSubmit, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"BeforeAgent"}}`},
		{"gemini", EventPostToolUse, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"AfterTool"}}`},
		{"gemini", EventStop, `{"decision":"block","reason":"hi"}`},
		{"mystery", EventUserPromptSubmit, `{"hookSpecificOutput":{"additionalContext":"hi","hookEventName":"UserPromptSubmit"}}`},
		{"claude", EventSessionEnd, ``},
		{"opencode", EventUserPromptSubmit, `hi`},
		{"opencode", EventStop, `hi`},
		{"antigravity", EventSessionStart, `{"injectSteps":[{"systemMessage":{"systemMessage":"hi"}}]}`},
		{"antigravity", EventUserPromptSubmit, `{"injectSteps":[{"ephemeralMessage":"hi"}]}`},
		{"antigravity", EventStop, `{"decision":"continue","reason":"hi"}`},
		{"antigravity", EventPostToolUse, ``},
	}
	for _, tc := range cases {
		if got := Format(tc.agent, tc.event, "hi", false); got != tc.want {
			t.Errorf("Format(%s, %s) = %s, want %s", tc.agent, tc.event, got, tc.want)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := Fallback("antigravity"); got != "{}" {
		t.Fatalf("Fallback(antigravity) = %q, want {}", got)
	}
	for _, agent := range []string{"claude", "codex", "gemini", "opencode", ""} {
		if got := Fallback(agent); got != "" {
			t.Fatalf("Fallback(%s) = %q, want nothing", agent, got)
		}
	}
	// Plain text is not JSON: escaping it would put \u sequences in front of
	// the model.
	if got := Format("opencode", EventUserPromptSubmit, "訊息", true); got != "訊息" {
		t.Fatalf("opencode text was escaped: %q", got)
	}
}

func TestRunSkipsRunnerRuns(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	env := map[string]string{RunIDEnv: "run-1"}
	out := Run(context.Background(), "claude", EventSessionStart, Input{SessionID: "s1", Cwd: t.TempDir()}, Options{
		Server:        srv.URL,
		ExclusionFile: filepath.Join(t.TempDir(), "none.json"),
		Getenv:        func(k string) string { return env[k] },
	})
	if out != "" || calls != 0 {
		t.Fatalf("a runner's run reached the server: out %q, %d calls", out, calls)
	}
}

func TestEscapeNonASCII(t *testing.T) {
	got := Format("claude", EventStop, "訊息 <ok> 🙂", true)
	want := `{"decision":"block","reason":"\u8a0a\u606f <ok> \ud83d\ude42"}`
	if got != want {
		t.Fatalf("escaped = %s, want %s", got, want)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(got), &v); err != nil || v["reason"] != "訊息 <ok> 🙂" {
		t.Fatalf("escaped JSON does not round-trip: %v %q", err, v["reason"])
	}
}

func TestRunDeliversMailAndTracksState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	agentsapi.New(nil, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	defer srv.Close()

	ascii := false
	opts := Options{Server: srv.URL, Actor: "mcp:alice", HTTPClient: srv.Client(), Windows: &ascii}
	ctx := context.Background()
	run := func(event string, in Input) string {
		t.Helper()
		if in.SessionID == "" {
			in.SessionID = "s1"
		}
		return Run(ctx, "claude", event, in, opts)
	}
	session := func() agents.Session {
		t.Helper()
		s, ok := hub.SessionByID("", "claude:s1")
		if !ok {
			t.Fatal("session claude:s1 not registered")
		}
		return s
	}

	out := run(EventSessionStart, Input{})
	if !strings.Contains(out, `"SessionStart"`) || !strings.Contains(out, "you are alice/") {
		t.Fatalf("session-start = %s, want identity line", out)
	}
	name := session().Name

	if out := run(EventUserPromptSubmit, Input{}); out != "" {
		t.Fatalf("prompt with no mail printed %s", out)
	}
	if got := session().State; got != agents.StateBusy {
		t.Fatalf("after prompt state = %q, want busy", got)
	}
	// A tool call names no state, so the turn stays busy.
	if out := run(EventPostToolUse, Input{}); out != "" {
		t.Fatalf("post-tool-use with no mail printed %s", out)
	}
	if got := session().State; got != agents.StateBusy {
		t.Fatalf("after tool state = %q, want busy", got)
	}

	if _, err := hub.Touch(agents.Heartbeat{SessionID: "codex:b1", ActorID: "mcp:bob", Name: "bob-bot", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Send(ctx, agents.SendInput{SenderActor: "mcp:bob", SenderSession: "bob-bot", To: name, Body: "hello from bob"}); err != nil {
		t.Fatal(err)
	}

	// A stop hook already re-run by its own block must not block again.
	if out := run(EventStop, Input{StopHookActive: true}); out != "" {
		t.Fatalf("stop_hook_active stop printed %s", out)
	}
	if n := hub.UnreadCount(ctx, "", name, "mcp:alice"); n != 1 {
		t.Fatalf("unread after guarded stop = %d, want 1", n)
	}

	out = run(EventStop, Input{})
	var block struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &block); err != nil {
		t.Fatalf("stop output %q: %v", out, err)
	}
	if block.Decision != "block" || !strings.Contains(block.Reason, "hello from bob") || !strings.Contains(block.Reason, "<nodevas-message") {
		t.Fatalf("stop block = %+v", block)
	}
	if got := session().State; got != agents.StateIdle {
		t.Fatalf("after stop state = %q, want idle", got)
	}
	if out := run(EventStop, Input{}); out != "" {
		t.Fatalf("second stop printed %s, want nothing (mail already delivered)", out)
	}

	if out := run(EventSessionEnd, Input{}); out != "" {
		t.Fatalf("session-end printed %s", out)
	}
	if session().Online {
		t.Fatal("session still online after session-end")
	}
	for _, s := range hub.ListSessions("", "", false, nil) {
		if s.ID == "claude:s1" {
			t.Fatal("ended session listed as online")
		}
	}
}

func TestRunSilentWhenServerDown(t *testing.T) {
	srv := httptest.NewServer(gin.New())
	url := srv.URL
	srv.Close()
	if out := Run(context.Background(), "claude", EventSessionStart, Input{SessionID: "x"}, Options{Server: url}); out != "" {
		t.Fatalf("down server printed %s", out)
	}
	if out := Run(context.Background(), "claude", EventSessionStart, Input{}, Options{Server: url}); out != "" {
		t.Fatalf("no session id printed %s", out)
	}
}

func TestLeaveIsActorBound(t *testing.T) {
	hub := agents.NewHub(nil, nil)
	if _, err := hub.Touch(agents.Heartbeat{SessionID: "claude:a", ActorID: "mcp:alice"}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Leave("", "claude:a", "mcp:bob"); !errors.Is(err, agents.ErrRefused) {
		t.Fatalf("foreign leave err = %v, want ErrRefused", err)
	}
	if s, _ := hub.SessionByID("", "claude:a"); !s.Online {
		t.Fatal("foreign leave took the session offline")
	}
	if err := hub.Leave("", "claude:a", "mcp:alice"); err != nil {
		t.Fatal(err)
	}
	if s, _ := hub.SessionByID("", "claude:a"); s.Online {
		t.Fatal("own leave left the session online")
	}
	if err := hub.Leave("", "claude:unknown", "mcp:alice"); err != nil {
		t.Fatalf("unknown leave err = %v, want nil", err)
	}
}
