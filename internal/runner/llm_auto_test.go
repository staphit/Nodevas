package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"nodevas/internal/copilot/localllm"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// TestMain keeps the tests off whatever model server the machine running
// them happens to have: no local model unless a test sets one.
func TestMain(m *testing.M) {
	detectLocalLLM = func(context.Context) (localllm.Found, bool) { return localllm.Found{}, false }
	os.Exit(m.Run())
}

// withLocalModel makes detection find an Ollama-like server, real on
// loopback, for the test.
func withLocalModel(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma3:4b"},{"name":"qwen3:8b"}]}`))
	}))
	t.Cleanup(server.Close)
	detector := &localllm.Detector{Candidates: []localllm.Candidate{{Server: localllm.ServerOllama, Root: server.URL}}}
	previous := detectLocalLLM
	detectLocalLLM = detector.Detect
	t.Cleanup(func() { detectLocalLLM = previous })
	return server.URL + "/v1"
}

func TestHelloOffersTheLocalModelWhenNoProviderIsSet(t *testing.T) {
	base := withLocalModel(t)
	t.Setenv(CopilotKeyEnv, "sk-must-not-travel")
	info := BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, &Config{})
	if info.LLM == nil || !info.LLM.Auto || info.LLM.Local != localllm.ServerOllama ||
		info.LLM.Provider != provider.KindOpenAI || info.LLM.Model != "qwen3:8b" || info.LLM.BaseURL != base ||
		!slices.Contains(info.Capabilities, runnerwire.CapLLM) {
		t.Fatalf("hello = %+v (llm %+v)", info, info.LLM)
	}

	// A provider the owner set wins over the local model.
	info = BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, copilotConfig())
	if info.LLM == nil || info.LLM.Auto || info.LLM.Model != "gpt-test" {
		t.Fatalf("configured hello = %+v", info.LLM)
	}

	// off turns the local model off too.
	cfg := &Config{}
	if err := cfg.Set("copilotProvider", []string{"off"}); err != nil {
		t.Fatal(err)
	}
	info = BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if info.LLM != nil || slices.Contains(info.Capabilities, runnerwire.CapLLM) {
		t.Fatalf("off hello = %+v", info.LLM)
	}
	if _, _, reason := cfg.CopilotLLM(); reason != "copilotProvider is off" {
		t.Fatalf("off reason = %q", reason)
	}
}

// A call for the local model goes to it without the runner's copilot key.
func TestLocalModelCallIsKeyless(t *testing.T) {
	base := withLocalModel(t)
	t.Setenv(CopilotKeyEnv, "sk-must-not-travel")
	var built provider.Config
	c := &Client{Config: &Config{}, NewLLMProvider: func(cfg provider.Config) (provider.Provider, error) {
		built = cfg
		return &llmFake{stream: func(context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
			return provider.Response{Message: provider.Message{Role: provider.RoleAssistant,
				Blocks: []provider.Block{{Type: provider.BlockText, Text: "hi"}}}, Stop: provider.StopEndTurn}, nil
		}}, nil
	}}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()
	req := llmRequest("llm-auto")
	req.Model, req.BaseURL = "qwen3:8b", base
	s.request(req)
	done := doneOf(t, s)
	if done.Error != "" {
		t.Fatalf("done = %+v", done)
	}
	var message provider.Message
	if err := json.Unmarshal(done.Message, &message); err != nil || messageTextOf(message) != "hi" {
		t.Fatalf("message = %s", done.Message)
	}
	if built.APIKey != "" || built.BaseURL != base || built.Model != "qwen3:8b" || built.Kind != provider.KindOpenAI {
		t.Fatalf("provider config = %+v", built)
	}

	// A request naming something else is still refused.
	other := llmRequest("llm-other")
	other.Model, other.BaseURL = "qwen3:8b", "https://evil.example/v1"
	s.request(other)
	if done := doneOf(t, s); done.Error == "" {
		t.Fatal("a request for another endpoint was answered")
	}
}

func messageTextOf(message provider.Message) string {
	text := ""
	for _, block := range message.Blocks {
		text += block.Text
	}
	return text
}
