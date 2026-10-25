package agenthook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	agentsapi "nodevas/internal/httpapi/agents"
	"nodevas/internal/mcp"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Claude Code's transcript lines, shaped like real ones (trimmed): a message
// streamed in two lines repeats its id and usage; subagents keep their own
// files next to the transcript.
func TestClaudeUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-1.jsonl")
	assistant := func(id, model string, in, out, write, read int) string {
		b, _ := json.Marshal(map[string]any{
			"type": "assistant", "uuid": "u-" + id, "requestId": "req-" + id, "isSidechain": false,
			"message": map[string]any{"id": id, "model": model, "role": "assistant", "type": "message",
				"content": []any{map[string]any{"type": "text", "text": "hi"}},
				"usage": map[string]any{"input_tokens": in, "output_tokens": out,
					"cache_creation_input_tokens": write, "cache_read_input_tokens": read,
					"output_tokens_details": map[string]any{"thinking_tokens": 1}}},
		})
		return string(b)
	}
	writeLines(t, path,
		`{"type":"user","message":{"role":"user","content":"do it"},"uuid":"u0"}`,
		assistant("msg_1", "claude-opus-5-5", 2, 10, 100, 1000),
		assistant("msg_1", "claude-opus-5-5", 2, 10, 100, 1000), // the same message, streamed on
		assistant("msg_2", "claude-opus-5-5", 3, 20, 0, 2000),
		assistant("msg_3", "<synthetic>", 0, 0, 0, 0),
		`not json`,
	)
	writeLines(t, filepath.Join(dir, "sess-1", "subagents", "agent-a.jsonl"),
		assistant("msg_9", "claude-haiku-5-5", 5, 7, 0, 300),
	)
	got := SessionUsage(context.Background(), "claude", Input{TranscriptPath: path})
	want := []mcp.SessionModelUsage{
		{Model: "claude-haiku-5-5", InputTokens: 5, OutputTokens: 7, CacheReadTokens: 300},
		{Model: "claude-opus-5-5", InputTokens: 5, OutputTokens: 30, CacheReadTokens: 3000, CacheWriteTokens: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage = %+v\nwant %+v", got, want)
	}
}

// Codex rollout lines, shaped like real ones (codex-cli 0.156): token_count
// carries the running total, turn_context the turn's model.
func TestCodexUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-10-24T01-41-35-abc.jsonl")
	total := func(in, cached, out int) string {
		return `{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":` +
			`{"input_tokens":` + itoa(in) + `,"cached_input_tokens":` + itoa(cached) + `,"cache_write_input_tokens":0,` +
			`"output_tokens":` + itoa(out) + `,"reasoning_output_tokens":1,"total_tokens":` + itoa(in+out) + `},` +
			`"last_token_usage":{"input_tokens":1}},"rate_limits":{}}}`
	}
	writeLines(t, path,
		`{"timestamp":"t","type":"session_meta","payload":{"id":"abc","model_provider":"openai","cli_version":"0.156.0"}}`,
		`{"timestamp":"t","type":"turn_context","payload":{"model":"gpt-5.6-terra","cwd":"x"}}`,
		total(1000, 800, 50),
		`{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":null}}`,
		total(3000, 2500, 90),
		`{"timestamp":"t","type":"turn_context","payload":{"model":"gpt-5.4-mini","cwd":"x"}}`,
		total(4000, 3000, 100),
	)
	got := SessionUsage(context.Background(), "codex", Input{TranscriptPath: path})
	want := []mcp.SessionModelUsage{
		{Model: "gpt-5.4-mini", InputTokens: 500, OutputTokens: 10, CacheReadTokens: 500},
		{Model: "gpt-5.6-terra", InputTokens: 500, OutputTokens: 90, CacheReadTokens: 2500},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage = %+v\nwant %+v", got, want)
	}
}

func TestCodexRolloutLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "sessions", "2026", "10", "24", "rollout-2026-10-24T01-41-35-01a169da-949b.jsonl")
	writeLines(t, path, `{}`)
	if got := codexRollout("01a169da-949b"); got != path {
		t.Fatalf("rollout = %q, want %q", got, path)
	}
	if got := codexRollout("../x*"); got != "" {
		t.Fatalf("a pattern found %q", got)
	}
}

// Antigravity's transcript lines, shaped like real ones (agy 1.4.7): model
// steps carry their tokens, cached ones inside input_tokens.
func TestAntigravityUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeLines(t, path,
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"do it"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","input_tokens":8083,"cache_read_tokens":0,"output_tokens":248}`,
		`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE"}`,
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","input_tokens":9000,"cache_read_tokens":4000,"output_tokens":58}`,
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","input_tokens":9000,"cache_read_tokens":4000,"output_tokens":58}`,
	)
	got := SessionUsage(context.Background(), "antigravity", Input{TranscriptPath: path, Model: "gemini-3.8-flash-medium"})
	want := []mcp.SessionModelUsage{{Model: "gemini-3.8-flash-medium", InputTokens: 13083, OutputTokens: 306, CacheReadTokens: 4000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage = %+v\nwant %+v", got, want)
	}
	if got := SessionUsage(context.Background(), "antigravity", Input{TranscriptPath: path}); len(got) != 1 || got[0].Model != unknownModel {
		t.Fatalf("no model = %+v", got)
	}
}

// A transcript that cannot be read whole reports nothing, not a smaller
// total; so does an agent whose transcript is not read.
func TestSessionUsageNothingPartial(t *testing.T) {
	if got := SessionUsage(context.Background(), "claude", Input{TranscriptPath: filepath.Join(t.TempDir(), "missing.jsonl")}); got != nil {
		t.Fatalf("missing transcript = %+v", got)
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeLines(t, path, `{"type":"assistant","message":{"id":"m","model":"x","usage":{"input_tokens":1}}}`)
	r := &lineReader{ctx: context.Background(), budget: 10}
	if err := r.each(path, nil, func([]byte) {}); err != errTranscriptTooBig {
		t.Fatalf("over budget = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := SessionUsage(ctx, "claude", Input{TranscriptPath: path}); got != nil {
		t.Fatalf("cancelled read = %+v", got)
	}
	if got := SessionUsage(context.Background(), "gemini", Input{TranscriptPath: path}); got != nil {
		t.Fatalf("gemini = %+v", got)
	}
}

// OpenCode's plugin passes the totals in; they are cleaned, not trusted.
func TestParseInputUsage(t *testing.T) {
	in := ParseInput([]byte(`{"session_id":"ses_1","cwd":"/w","usage":[` +
		`{"model":"opencode/deepseek","inputTokens":12065,"outputTokens":3,"cacheReadTokens":1792,"cacheWriteTokens":0,"costUsd":0.01},` +
		`{"model":"opencode/deepseek","inputTokens":5,"outputTokens":1,"cacheReadTokens":0,"cacheWriteTokens":0,"costUsd":0},` +
		`{"model":"","inputTokens":-4,"outputTokens":0},"junk",{"model":"zero"}]}`))
	got := SessionUsage(context.Background(), "opencode", in)
	want := []mcp.SessionModelUsage{{Model: "opencode/deepseek", InputTokens: 12070, OutputTokens: 4, CacheReadTokens: 1792, CostUSD: 0.01}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage = %+v\nwant %+v", got, want)
	}
	if in := ParseInput([]byte(`{"session_id":"s","transcript_path":"/a/b.jsonl"}`)); in.TranscriptPath != filepath.Clean("/a/b.jsonl") {
		t.Fatalf("transcript_path = %q", in.TranscriptPath)
	}
	if in := ParseInput([]byte(`{"conversationId":"c","transcriptPath":"C:/x/transcript.jsonl"}`)); in.TranscriptPath == "" {
		t.Fatal("transcriptPath not read")
	}
}

// At Stop and at session-end the hook sends the session's usage after its
// heartbeat, on the session's own id.
func TestRunReportsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	agentsapi.New(nil, hub).Register(router.Group("/api"))
	var (
		mu     sync.Mutex
		paths  []string
		bodies []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/usage") {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			paths, bodies = append(paths, r.URL.Path), append(bodies, body)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer srv.Close()

	opts := Options{Server: srv.URL, Actor: "mcp:opencode", ExclusionFile: filepath.Join(t.TempDir(), "none.json"),
		Getenv: func(string) string { return "" }}
	in := ParseInput([]byte(`{"session_id":"ses_1","cwd":` + jsonString(t.TempDir()) +
		`,"usage":[{"model":"opencode/m","inputTokens":10,"outputTokens":2,"cacheReadTokens":0,"cacheWriteTokens":0,"costUsd":0}]}`))
	Run(context.Background(), "opencode", EventSessionStart, Input{SessionID: in.SessionID, Cwd: in.Cwd}, opts)
	Run(context.Background(), "opencode", EventStop, in, opts)
	Run(context.Background(), "opencode", EventSessionEnd, in, opts)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("usage reports = %v", paths)
	}
	for i, p := range paths {
		if p != "/api/agents/sessions/opencode:ses_1/usage" {
			t.Fatalf("report %d went to %q", i, p)
		}
		models, _ := bodies[i]["models"].([]any)
		first, _ := models[0].(map[string]any)
		if bodies[i]["agent"] != "opencode" || bodies[i]["actor"] != "mcp:opencode" || len(models) != 1 ||
			first["model"] != "opencode/m" || first["inputTokens"] != float64(10) {
			t.Fatalf("report %d = %+v", i, bodies[i])
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
