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
		{Type: TypeLLMRequest, LLM: &LLMRequest{ID: "llm-2", AccountID: "u1", Provider: "openai", Model: "m",
			System: "s", Messages: json.RawMessage(`[{"role":"user","blocks":[]}]`),
			ResponseSchema: &LLMResponseSchema{Name: "draft_plan", Schema: json.RawMessage(`{"type":"object"}`)}}},
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

// An llm_request carries a response schema as {"name","schema"}, leaves it
// out when there is none, and an older runner's decoder (no such field)
// still reads the rest.
func TestLLMRequestResponseSchemaWire(t *testing.T) {
	with, _ := json.Marshal(LLMRequest{ID: "a", ResponseSchema: &LLMResponseSchema{Name: "n", Schema: json.RawMessage(`{"type":"object"}`)}})
	if !strings.Contains(string(with), `"responseSchema":{"name":"n","schema":{"type":"object"}}`) {
		t.Fatalf("wire = %s", with)
	}
	without, _ := json.Marshal(LLMRequest{ID: "a"})
	if strings.Contains(string(without), "responseSchema") {
		t.Fatalf("an absent schema is on the wire: %s", without)
	}
	var old struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(with, &old); err != nil || old.ID != "a" {
		t.Fatalf("an older decoder failed: %v %+v", err, old)
	}
}

// C5: an llm_request carries an optional temperature; none is left out.
func TestLLMRequestTemperatureWire(t *testing.T) {
	cool := 0.2
	with, _ := json.Marshal(LLMRequest{ID: "a", Temperature: &cool})
	if !strings.Contains(string(with), `"temperature":0.2`) {
		t.Fatalf("wire = %s", with)
	}
	if without, _ := json.Marshal(LLMRequest{ID: "a"}); strings.Contains(string(without), "temperature") {
		t.Fatalf("an absent temperature is on the wire: %s", without)
	}
}
