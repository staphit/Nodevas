package runnerwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLLMEnvelopesRoundTrip(t *testing.T) {
	in := []Envelope{
		{Type: TypeLLMRequest, LLM: &LLMRequest{ID: "llm-1", AccountID: "u1", Provider: "anthropic", Model: "m",
			System: "s", Messages: json.RawMessage(`[{"role":"user","blocks":[]}]`), MaxTokens: 100}},
		{Type: TypeLLMDelta, LLMDelta: &LLMDelta{ID: "llm-1", Text: "he"}},
		{Type: TypeLLMDone, LLMDone: &LLMDone{ID: "llm-1", Message: json.RawMessage(`{"role":"assistant"}`), Stop: "end_turn"}},
		{Type: TypeLLMCancel, LLMID: "llm-1"},
		{Type: TypeHello, Hello: &Info{Capabilities: []string{CapLLM}, LLM: &LLMInfo{Provider: "openai", Model: "g"}}},
	}
	for _, env := range in {
		raw, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		var out Envelope
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(out)
		if string(again) != string(raw) {
			t.Fatalf("round trip changed %s into %s", raw, again)
		}
		if strings.Contains(string(raw), "apiKey") {
			t.Fatalf("a key field on the wire: %s", raw)
		}
	}
}
