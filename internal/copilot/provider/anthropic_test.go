package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// sseEvent formats one Anthropic server-sent event.
func sseEvent(typ, data string) string {
	return "event: " + typ + "\ndata: " + data + "\n\n"
}

// anthropicToolTurn is a realistic streamed turn: a signed thinking block,
// text in two deltas, then a tool call whose input arrives split across
// input_json_delta fragments.
var anthropicToolTurn = strings.Join([]string{
	sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":1}}}`),
	sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"look up n12"}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc123"}}`),
	sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
	sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":", world"}}`),
	sseEvent("content_block_stop", `{"type":"content_block_stop","index":1}`),
	sseEvent("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_node","input":{}}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"id\":"}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"n12\"}"}}`),
	sseEvent("content_block_stop", `{"type":"content_block_stop","index":2}`),
	sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":42}}`),
	sseEvent("message_stop", `{"type":"message_stop"}`),
}, "")

var anthropicTextTurn = strings.Join([]string{
	sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":50,"output_tokens":1}}}`),
	sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
	sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}`),
	sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
	sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`),
	sseEvent("message_stop", `{"type":"message_stop"}`),
}, "")

var anthropicRefusalTurn = strings.Join([]string{
	sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_3","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`),
	sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"cyber","explanation":null}},"usage":{"output_tokens":0}}`),
	sseEvent("message_stop", `{"type":"message_stop"}`),
}, "")

// captured is one request the fake server received.
type captured struct {
	header http.Header
	body   []byte
}

// fakeSSE serves the given streams in order, one per request, and records
// each request so a test can inspect what was sent.
func fakeSSE(t *testing.T, streams ...string) (*httptest.Server, func() []captured) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		n := len(reqs)
		reqs = append(reqs, captured{header: r.Header.Clone(), body: b})
		mu.Unlock()
		if n >= len(streams) {
			http.Error(w, `{"error":{"message":"unexpected request"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streams[n])
	}))
	t.Cleanup(srv.Close)
	return srv, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return append([]captured(nil), reqs...)
	}
}

func newTestAnthropic(t *testing.T, base string) *anthropicProvider {
	t.Helper()
	p, err := New(Config{Kind: "anthropic", APIKey: "sk-test", BaseURL: base})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*anthropicProvider)
}

var getNodeTool = Tool{
	Name:        "get_node",
	Description: "Read one node",
	Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
}

func TestAnthropicStreamToolTurnAndReplay(t *testing.T) {
	srv, reqs := fakeSSE(t, anthropicToolTurn, anthropicTextTurn)
	p := newTestAnthropic(t, srv.URL)

	history := []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "open n12"}}}}
	var deltas []string
	resp, err := p.Stream(context.Background(), Request{
		System: "You are the copilot.", Messages: history, Tools: []Tool{getNodeTool},
	}, func(d Delta) { deltas = append(deltas, d.Text) })
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(deltas, "|"); got != "Hello|, world" {
		t.Errorf("deltas = %q", got)
	}
	if resp.Stop != StopToolUse {
		t.Errorf("stop = %q", resp.Stop)
	}
	if resp.InputTokens != 15 || resp.OutputTokens != 42 {
		t.Errorf("usage = %d/%d, want 15/42", resp.InputTokens, resp.OutputTokens)
	}
	m := resp.Message
	if m.Role != RoleAssistant || m.RawProvider != "anthropic" || m.RawModel != "claude-opus-5-5" {
		t.Errorf("message meta = %+v", m)
	}
	if len(m.Blocks) != 2 || m.Blocks[0].Text != "Hello, world" || m.Blocks[1].Type != BlockToolCall {
		t.Fatalf("blocks = %+v", m.Blocks)
	}
	call := m.Blocks[1]
	if call.ToolCallID != "toolu_1" || call.ToolName != "get_node" || compactJSON(t, call.Input) != `{"id":"n12"}` {
		t.Errorf("tool call = %+v (input %s)", call, call.Input)
	}
	if !strings.Contains(string(m.Raw), `"signature":"sig-abc123"`) || !strings.Contains(string(m.Raw), `"thinking":"look up n12"`) {
		t.Errorf("raw lost the thinking block: %s", m.Raw)
	}

	// First request: system, effort, tool schema with its extra keywords, and
	// no thinking or fallback fields.
	first := decodeBody(t, reqs()[0].body)
	if first["model"] != "claude-opus-5-5" || first["stream"] != true || first["max_tokens"] != float64(16000) {
		t.Errorf("request basics = %v %v %v", first["model"], first["stream"], first["max_tokens"])
	}
	if oc, _ := first["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Errorf("output_config = %v", first["output_config"])
	}
	for _, k := range []string{"thinking", "fallbacks", "tool_choice"} {
		if _, ok := first[k]; ok {
			t.Errorf("request must not set %q", k)
		}
	}
	tool := first["tools"].([]any)[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"] != false || fmt.Sprint(schema["required"]) != "[id]" {
		t.Errorf("input_schema = %v", schema)
	}
	if h := reqs()[0].header.Get("anthropic-beta"); h != "" {
		t.Errorf("fallback beta sent to a non-default base URL: %q", h)
	}

	// Second call: the assistant turn replays verbatim and the tool result is
	// a user turn with is_error set.
	history = append(history, m, Message{Role: RoleUser, Blocks: []Block{
		{Type: BlockToolResult, ToolCallID: "toolu_1", Output: "no such node", IsError: true},
	}})
	resp2, err := p.Stream(context.Background(), Request{Messages: history, Tools: []Tool{getNodeTool}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Stop != StopEndTurn || len(resp2.Message.Blocks) != 1 || resp2.Message.Blocks[0].Text != "Done." {
		t.Errorf("second response = %+v", resp2)
	}

	second := decodeBody(t, reqs()[1].body)
	msgs := second["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("second request has %d messages", len(msgs))
	}
	if compactJSON(t, mustJSON(t, msgs[1])) != compactJSON(t, m.Raw) {
		t.Errorf("assistant turn not replayed verbatim:\n got %s\nwant %s", mustJSON(t, msgs[1]), m.Raw)
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" || result["is_error"] != true {
		t.Errorf("tool result = %v", result)
	}
}

func TestAnthropicRefusal(t *testing.T) {
	srv, _ := fakeSSE(t, anthropicRefusalTurn)
	p := newTestAnthropic(t, srv.URL)
	resp, err := p.Stream(context.Background(), Request{Messages: []Message{
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Stop != StopRefusal || len(resp.Message.Blocks) != 0 || len(resp.Message.Raw) != 0 {
		t.Errorf("refusal = %+v", resp)
	}
}

func TestAnthropicFallbackOptIn(t *testing.T) {
	for _, tc := range []struct {
		model, base string
		want        bool
	}{
		{"claude-opus-5-5", "", true},
		{"claude-sonnet-5-5", "https://api.anthropic.com/", true},
		{"claude-opus-5-5", "https://gateway.example", false},
		{"claude-haiku-5-5", "", false},
	} {
		p := newAnthropic(Config{Model: tc.model, BaseURL: tc.base, APIKey: "k"})
		if p.fallback != tc.want {
			t.Errorf("%s @ %q: fallback = %v, want %v", tc.model, tc.base, p.fallback, tc.want)
		}
	}

	// The opt-in rides on the plain streaming call as a header plus a body
	// field; check both actually reach the wire.
	srv, reqs := fakeSSE(t, anthropicTextTurn)
	p := newTestAnthropic(t, srv.URL)
	p.fallback = true
	if _, err := p.Stream(context.Background(), Request{Messages: []Message{
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}},
	}}, nil); err != nil {
		t.Fatal(err)
	}
	r := reqs()[0]
	if !strings.Contains(strings.Join(r.header.Values("anthropic-beta"), ","), fallbackBeta) {
		t.Errorf("anthropic-beta = %v", r.header.Values("anthropic-beta"))
	}
	if body := decodeBody(t, r.body); body["fallbacks"] != "default" || body["stream"] != true {
		t.Errorf("fallbacks = %v, stream = %v", body["fallbacks"], body["stream"])
	}
}

func TestAnthropicHistoryConversion(t *testing.T) {
	p := newAnthropic(Config{Model: "claude-opus-5-5", APIKey: "k"})
	foreignRaw := json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"other-model"}]}`)
	msgs, err := p.toParams([]Message{
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}},
		// From another model: rebuilt from blocks, its thinking dropped.
		{Role: RoleAssistant, Raw: foreignRaw, RawProvider: "anthropic", RawModel: "claude-sonnet-5-5", Blocks: []Block{
			{Type: BlockText, Text: "calling"},
			{Type: BlockToolCall, ToolCallID: "a", ToolName: "t1", Input: json.RawMessage(`{"x":1}`)},
			{Type: BlockToolCall, ToolCallID: "b", ToolName: "t2"},
		}},
		// Results stored as separate user turns, with a stray text first.
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "also"}, {Type: BlockToolResult, ToolCallID: "a", Output: "ok"}}},
		{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolCallID: "b", Output: "bad", IsError: true}}},
		// An empty refusal turn is dropped.
		{Role: RoleAssistant},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeList(t, mustJSON(t, msgs))
	if len(got) != 3 {
		t.Fatalf("got %d messages: %s", len(got), mustJSON(t, msgs))
	}
	asst := got[1]["content"].([]any)
	if len(asst) != 3 || asst[0].(map[string]any)["type"] != "text" ||
		asst[1].(map[string]any)["type"] != "tool_use" || fmt.Sprint(asst[2].(map[string]any)["input"]) != "map[]" {
		t.Errorf("assistant rebuilt = %s", mustJSON(t, asst))
	}
	user := got[2]["content"].([]any)
	var types []string
	for _, c := range user {
		cm := c.(map[string]any)
		types = append(types, fmt.Sprint(cm["type"], ":", cm["tool_use_id"], ":", cm["is_error"]))
	}
	want := "tool_result:a:false tool_result:b:true text:<nil>:<nil>"
	if strings.Join(types, " ") != want {
		t.Errorf("merged user turn = %v, want %s", types, want)
	}
}

func TestReplayableContentDropsDeclinedPartial(t *testing.T) {
	stream := strings.Join([]string{
		sseEvent("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"declined","signature":"s"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"Part"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":1}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"fallback","from":{"model":"claude-opus-5-5"},"to":{"model":"claude-opus-4-8"}}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":2}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"text","text":"ial answer"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":3}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}, "")
	srv, _ := fakeSSE(t, stream)
	p := newTestAnthropic(t, srv.URL)
	resp, err := p.Stream(context.Background(), Request{Messages: []Message{
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(resp.Message.Raw)
	if strings.Contains(raw, "declined") || strings.Contains(raw, "fallback") || !strings.Contains(raw, "Part") || !strings.Contains(raw, "ial answer") {
		t.Errorf("raw = %s", raw)
	}
}

func decodeBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

func decodeList(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var l []map[string]any
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return l
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// compactJSON normalizes key order and spacing so two encodings of the same
// value compare equal.
func compactJSON(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return string(mustJSON(t, v))
}
