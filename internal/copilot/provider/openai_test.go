package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func oaData(chunk string) string { return "data: " + chunk + "\n\n" }

// openaiToolTurn streams text, then one tool call whose id and name arrive in
// the first fragment and whose arguments are split over three, then a usage
// chunk as stream_options.include_usage produces.
var openaiToolTurn = strings.Join([]string{
	": keep-alive\n\n",
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Let me"},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" check."},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_node","arguments":""}}]},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\""}}]},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"n1"}}]},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"2\"}"}}]},"finish_reason":null}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
	oaData(`{"id":"c1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}}`),
	"data: [DONE]\n\n",
}, "")

var openaiTextTurn = strings.Join([]string{
	oaData(`{"choices":[{"index":0,"delta":{"content":"Done."},"finish_reason":null}]}`),
	oaData(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
	"data: [DONE]\n\n",
}, "")

func newTestOpenAI(t *testing.T, base, model string) *openaiProvider {
	t.Helper()
	p, err := New(Config{Kind: "openai", BaseURL: base, Model: model, APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*openaiProvider)
}

func TestOpenAIStreamToolTurnAndReplay(t *testing.T) {
	srv, reqs := fakeSSE(t, openaiToolTurn, openaiTextTurn)
	p := newTestOpenAI(t, srv.URL+"/v1/", "gpt-5-mini")

	history := []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "open n12"}}}}
	var deltas []string
	resp, err := p.Stream(context.Background(), Request{
		System: "You are the copilot.", Messages: history, Tools: []Tool{getNodeTool}, MaxTokens: 500,
	}, func(d Delta) { deltas = append(deltas, d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, "|"); got != "Let me| check." {
		t.Errorf("deltas = %q", got)
	}
	if resp.Stop != StopToolUse || resp.InputTokens != 20 || resp.OutputTokens != 9 {
		t.Errorf("stop/usage = %q %d/%d", resp.Stop, resp.InputTokens, resp.OutputTokens)
	}
	m := resp.Message
	if m.RawProvider != "openai" || m.RawModel != "gpt-5-mini" || len(m.Blocks) != 2 {
		t.Fatalf("message = %+v", m)
	}
	call := m.Blocks[1]
	if call.Type != BlockToolCall || call.ToolCallID != "call_abc" || call.ToolName != "get_node" || compactJSON(t, call.Input) != `{"id":"n12"}` {
		t.Errorf("tool call = %+v (input %s)", call, call.Input)
	}

	r0 := reqs()[0]
	if r0.header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("auth = %q", r0.header.Get("Authorization"))
	}
	first := decodeBody(t, r0.body)
	if first["stream"] != true || first["reasoning_effort"] != "low" || first["max_completion_tokens"] != float64(500) {
		t.Errorf("request = %v", first)
	}
	if so, _ := first["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("stream_options = %v", first["stream_options"])
	}
	msgs := first["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", msgs)
	}
	fn := first["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_node" || fn["parameters"].(map[string]any)["type"] != "object" {
		t.Errorf("tool = %v", fn)
	}

	history = append(history, m, Message{Role: RoleUser, Blocks: []Block{
		{Type: BlockToolResult, ToolCallID: "call_abc", Output: "no such node", IsError: true},
		{Type: BlockText, Text: "and then?"},
	}})
	resp2, err := p.Stream(context.Background(), Request{Messages: history}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Stop != StopEndTurn || resp2.Message.Blocks[0].Text != "Done." {
		t.Errorf("second = %+v", resp2)
	}
	msgs = decodeBody(t, reqs()[1].body)["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("second request messages = %v", msgs)
	}
	if compactJSON(t, mustJSON(t, msgs[1])) != compactJSON(t, m.Raw) {
		t.Errorf("assistant not replayed verbatim:\n got %s\nwant %s", mustJSON(t, msgs[1]), m.Raw)
	}
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_abc" || tool["content"] != "Error: no such node" {
		t.Errorf("tool message = %v", tool)
	}
	if u := msgs[3].(map[string]any); u["role"] != "user" || u["content"] != "and then?" {
		t.Errorf("user message = %v", u)
	}
}

func TestOpenAINeutralHistoryAndLocalModel(t *testing.T) {
	srv, reqs := fakeSSE(t, openaiTextTurn)
	p, err := New(Config{Kind: "openai", BaseURL: srv.URL, Model: "llama3.1"})
	if err != nil {
		t.Fatalf("local server without key: %v", err)
	}
	_, err = p.Stream(context.Background(), Request{Messages: []Message{
		{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}},
		// From Anthropic: rebuilt from neutral blocks, raw ignored.
		{Role: RoleAssistant, Raw: []byte(`{"role":"assistant","content":[]}`), RawProvider: "anthropic", RawModel: "claude-opus-5-5", Blocks: []Block{
			{Type: BlockText, Text: "ok"},
			{Type: BlockToolCall, ToolCallID: "toolu_1", ToolName: "t", Input: []byte(`{"a":1}`)},
		}},
		{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolCallID: "toolu_1", Output: "fine"}}},
	}, MaxTokens: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := reqs()[0]
	if r.header.Get("Authorization") != "" {
		t.Errorf("no key, yet Authorization = %q", r.header.Get("Authorization"))
	}
	body := decodeBody(t, r.body)
	if _, ok := body["reasoning_effort"]; ok {
		t.Error("reasoning_effort sent to a non-reasoning model")
	}
	if body["max_tokens"] != float64(100) {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	asst := body["messages"].([]any)[1].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if asst["content"] != "ok" || tc["id"] != "toolu_1" || tc["function"].(map[string]any)["arguments"] != `{"a":1}` {
		t.Errorf("assistant = %v", asst)
	}
}

func TestOpenAIErrors(t *testing.T) {
	long := strings.Repeat("x", 900)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + long + `","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()
	p := newTestOpenAI(t, srv.URL, "gpt-5")
	_, err := p.Stream(context.Background(), Request{}, nil)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("err = %v", err)
	}
	if strings.Count(err.Error(), "x") > 500 {
		t.Errorf("error message not truncated: %d chars", len(err.Error()))
	}

	// A connection cut before [DONE] and any finish reason is an error.
	srv2, _ := fakeSSE(t, oaData(`{"choices":[{"index":0,"delta":{"content":"par"}}]}`))
	_, err = newTestOpenAI(t, srv2.URL, "gpt-5").Stream(context.Background(), Request{}, nil)
	if err == nil {
		t.Error("truncated stream accepted")
	}

	// content_filter maps to a refusal.
	srv3, _ := fakeSSE(t, oaData(`{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`)+"data: [DONE]\n\n")
	resp, err := newTestOpenAI(t, srv3.URL, "gpt-5").Stream(context.Background(), Request{}, nil)
	if err != nil || resp.Stop != StopRefusal {
		t.Errorf("content_filter: %v %q", err, resp.Stop)
	}
}

func TestOpenAICancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(oaData(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	p := newTestOpenAI(t, srv.URL, "gpt-5")
	done := make(chan error, 1)
	go func() {
		_, err := p.Stream(ctx, Request{}, func(Delta) { cancel() })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}
}

func TestReasoningModel(t *testing.T) {
	for model, want := range map[string]bool{
		"gpt-5": true, "gpt-5-mini": true, "o3": true, "o4-mini": true, "openai/o3": true,
		"gpt-4o": false, "llama3.1": false, "openhermes": false, "olmo-2": false,
	} {
		if got := reasoningModel(model); got != want {
			t.Errorf("reasoningModel(%q) = %v", model, got)
		}
	}
}
