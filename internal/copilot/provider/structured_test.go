package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var draftSchema = &ResponseSchema{
	Name:   "emit_draft",
	Schema: []byte(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`),
}

func TestOpenAISendsResponseFormat(t *testing.T) {
	srv, reqs := fakeSSE(t, openaiTextTurn)
	_, err := newTestOpenAI(t, srv.URL, "qwen3:8b").Stream(context.Background(), Request{
		Messages:       []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}}},
		ResponseSchema: draftSchema,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	format, _ := decodeBody(t, reqs()[0].body)["response_format"].(map[string]any)
	inner, _ := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || inner["name"] != "emit_draft" || inner["schema"] == nil {
		t.Fatalf("response_format = %v", format)
	}
	if _, strict := inner["strict"]; strict {
		t.Error("strict sent; the draft schema is not written for strict mode")
	}
	srv2, reqs2 := fakeSSE(t, openaiTextTurn)
	if _, err := newTestOpenAI(t, srv2.URL, "qwen3:8b").Stream(context.Background(), Request{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeBody(t, reqs2()[0].body)["response_format"]; ok {
		t.Error("response_format sent without a schema")
	}
}

// rejectFirst answers the first request with status and the rest with stream.
func rejectFirst(t *testing.T, status int, stream string) (*httptest.Server, func() [][]byte) {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, b)
		mu.Unlock()
		if n == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"unsupported: response format"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	t.Cleanup(srv.Close)
	return srv, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), bodies...)
	}
}

// A local server that does not know response_format answers 400; the call
// is made again without it, and the caller validates the answer anyway.
func TestOpenAIRetriesWithoutResponseFormatWhenRefused(t *testing.T) {
	srv, bodies := rejectFirst(t, http.StatusBadRequest, openaiTextTurn)
	resp, err := newTestOpenAI(t, srv.URL, "llama3.1").Stream(context.Background(), Request{ResponseSchema: draftSchema}, nil)
	if err != nil || messageTextOf(resp) != "Done." {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	got := bodies()
	if len(got) != 2 || !strings.Contains(string(got[0]), "response_format") || strings.Contains(string(got[1]), "response_format") {
		t.Fatalf("bodies = %q", got)
	}
	// Other failures are not retried.
	srv2, bodies2 := rejectFirst(t, http.StatusUnauthorized, openaiTextTurn)
	if _, err := newTestOpenAI(t, srv2.URL, "llama3.1").Stream(context.Background(), Request{ResponseSchema: draftSchema}, nil); err == nil || len(bodies2()) != 1 {
		t.Fatalf("401 retried or accepted: %v, %d requests", err, len(bodies2()))
	}
}

func TestAnthropicSendsOutputFormat(t *testing.T) {
	srv, reqs := fakeSSE(t, anthropicTextTurn)
	resp, err := newTestAnthropic(t, srv.URL).Stream(context.Background(), Request{
		Messages:       []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}}},
		ResponseSchema: draftSchema,
	}, nil)
	if err != nil || messageTextOf(resp) != "Done." {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	config, _ := decodeBody(t, reqs()[0].body)["output_config"].(map[string]any)
	format, _ := config["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["type"] != "object" {
		t.Fatalf("output_config = %v", config)
	}
	if _, ok := decodeBody(t, reqs()[0].body)["tool_choice"]; ok {
		t.Error("tool_choice sent: a forced tool does not work with thinking on")
	}
}

func TestAnthropicRetriesWithoutOutputFormatWhenRefused(t *testing.T) {
	srv, bodies := rejectFirst(t, http.StatusBadRequest, anthropicTextTurn)
	resp, err := newTestAnthropic(t, srv.URL).Stream(context.Background(), Request{
		Messages:       []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}}},
		ResponseSchema: draftSchema,
	}, nil)
	if err != nil || messageTextOf(resp) != "Done." {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	got := bodies()
	if len(got) != 2 || !strings.Contains(string(got[0]), `"format"`) || strings.Contains(string(got[1]), `"format"`) {
		t.Fatalf("bodies = %q", got)
	}
}

func messageTextOf(resp Response) string {
	var parts []string
	for _, block := range resp.Message.Blocks {
		if block.Type == BlockText {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

// C5: a request's temperature goes to an OpenAI-compatible model that takes
// one (Ollama, LM Studio, gpt-4o), never to a reasoning model, and never
// when unset; Anthropic ignores it (thinking is on, which refuses one).
func TestTemperatureIsSentWhereTaken(t *testing.T) {
	cool := 0.2
	for model, want := range map[string]bool{"qwen3:8b": true, "gpt-4o": true, "gpt-5-mini": false, "o4-mini": false} {
		srv, reqs := fakeSSE(t, openaiTextTurn)
		if _, err := newTestOpenAI(t, srv.URL, model).Stream(context.Background(), Request{Temperature: &cool}, nil); err != nil {
			t.Fatal(err)
		}
		got, sent := decodeBody(t, reqs()[0].body)["temperature"]
		if sent != want || (want && got != 0.2) {
			t.Errorf("%s: temperature sent %v (%v), want %v", model, sent, got, want)
		}
	}
	srv, reqs := fakeSSE(t, openaiTextTurn)
	if _, err := newTestOpenAI(t, srv.URL, "qwen3:8b").Stream(context.Background(), Request{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, sent := decodeBody(t, reqs()[0].body)["temperature"]; sent {
		t.Error("temperature sent though the request set none")
	}
	srv2, reqs2 := fakeSSE(t, anthropicTextTurn)
	if _, err := newTestAnthropic(t, srv2.URL).Stream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "go"}}}}, Temperature: &cool,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, sent := decodeBody(t, reqs2()[0].body)["temperature"]; sent {
		t.Error("Anthropic was sent a temperature with thinking on")
	}
}
