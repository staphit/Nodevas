package runner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// llmFake is a provider whose Stream is the test's.
type llmFake struct {
	stream func(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error)
}

func (f *llmFake) Name() string  { return "openai" }
func (f *llmFake) Model() string { return "gpt-test" }
func (f *llmFake) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	return f.stream(ctx, req, onDelta)
}

func copilotConfig() *Config {
	return &Config{CopilotProvider: "openai", CopilotModel: "gpt-test", CopilotAPIKey: "sk-here"}
}

func llmRequest(id string) *runnerwire.LLMRequest {
	return &runnerwire.LLMRequest{
		ID: id, AccountID: "u-ann", Provider: "openai", Model: "gpt-test",
		System:   "be brief",
		Messages: json.RawMessage(`[{"role":"user","blocks":[{"type":"text","text":"hi"}]}]`),
	}
}

// next returns the session's next message, failing after a while.
func next(t *testing.T, s *llmSession) runnerwire.Envelope {
	t.Helper()
	select {
	case env := <-s.out:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("no message from the copilot session")
		return runnerwire.Envelope{}
	}
}

func doneOf(t *testing.T, s *llmSession) *runnerwire.LLMDone {
	t.Helper()
	for {
		env := next(t, s)
		if env.Type == runnerwire.TypeLLMDone {
			return env.LLMDone
		}
	}
}

func TestLLMSessionRefuses(t *testing.T) {
	cases := []struct {
		name   string
		config func(*Config)
		req    func(*runnerwire.LLMRequest)
		want   string
	}{
		{"no provider", func(c *Config) { c.CopilotProvider = "" }, nil, "copilotProvider is not set"},
		{"no key", func(c *Config) { c.CopilotAPIKey = "" }, nil, "no copilot key"},
		{"another account", nil, func(r *runnerwire.LLMRequest) { r.AccountID = "u-bob" }, "its owner's copilot only"},
		{"another provider", nil, func(r *runnerwire.LLMRequest) { r.Provider = "anthropic" }, "does not match"},
		{"another model", nil, func(r *runnerwire.LLMRequest) { r.Model = "gpt-other" }, "does not match"},
		{"another endpoint", nil, func(r *runnerwire.LLMRequest) { r.BaseURL = "https://evil.example/v1" }, "does not match"},
		{"bad effort", nil, func(r *runnerwire.LLMRequest) { r.Effort = "max" }, "unknown effort"},
		{"no messages", nil, func(r *runnerwire.LLMRequest) { r.Messages = json.RawMessage(`[]`) }, "no readable messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(CopilotKeyEnv, "")
			cfg := copilotConfig()
			if tc.config != nil {
				tc.config(cfg)
			}
			called := false
			c := &Client{Config: cfg, NewLLMProvider: func(provider.Config) (provider.Provider, error) {
				called = true
				return nil, errors.New("must not be built")
			}}
			s := c.newLLMSession(context.Background(), "u-ann")
			defer s.stop()
			req := llmRequest("llm-1")
			if tc.req != nil {
				tc.req(req)
			}
			s.request(req)
			done := doneOf(t, s)
			if done.ID != "llm-1" || !strings.Contains(done.Error, tc.want) || len(done.Message) != 0 {
				t.Fatalf("done = %+v, want an error with %q", done, tc.want)
			}
			if called {
				t.Fatal("a provider was built for a refused call")
			}
		})
	}
}

func TestLLMSessionStreamsTheAnswer(t *testing.T) {
	var seen provider.Config
	var asked provider.Request
	c := &Client{Config: copilotConfig(), NewLLMProvider: func(cfg provider.Config) (provider.Provider, error) {
		seen = cfg
		return &llmFake{stream: func(_ context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
			asked = req
			onDelta(provider.Delta{Text: "Hel"})
			onDelta(provider.Delta{Text: "lo"})
			return provider.Response{
				Message: provider.Message{Role: provider.RoleAssistant,
					Blocks: []provider.Block{{Type: provider.BlockText, Text: "Hello"}}, RawProvider: "openai", RawModel: "gpt-test"},
				Stop: provider.StopEndTurn, InputTokens: 7, OutputTokens: 2,
			}, nil
		}}, nil
	}}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()
	req := llmRequest("llm-2")
	req.Effort = "medium"
	req.MaxTokens = 10 * runnerwire.MaxLLMMaxTokens
	s.request(req)

	var texts []string
	var done *runnerwire.LLMDone
	for done == nil {
		env := next(t, s)
		switch env.Type {
		case runnerwire.TypeLLMDelta:
			texts = append(texts, env.LLMDelta.Text)
		case runnerwire.TypeLLMDone:
			done = env.LLMDone
		}
	}
	if strings.Join(texts, "|") != "Hel|lo" {
		t.Fatalf("deltas = %q", texts)
	}
	var message provider.Message
	if err := json.Unmarshal(done.Message, &message); err != nil || done.Error != "" ||
		message.Blocks[0].Text != "Hello" || done.Stop != "end_turn" || done.InputTokens != 7 || done.OutputTokens != 2 {
		t.Fatalf("done = %+v (%v)", done, err)
	}
	if seen.APIKey != "sk-here" || seen.Kind != "openai" || seen.Model != "gpt-test" || seen.Effort != "medium" {
		t.Fatalf("provider config = %+v", seen)
	}
	if asked.MaxTokens != runnerwire.MaxLLMMaxTokens || asked.System != "be brief" || len(asked.Messages) != 1 {
		t.Fatalf("request = %+v", asked)
	}
}

func TestLLMSessionCancelAndConcurrency(t *testing.T) {
	started := make(chan string, 4)
	c := &Client{Config: copilotConfig(), NewLLMProvider: func(provider.Config) (provider.Provider, error) {
		return &llmFake{stream: func(ctx context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
			started <- req.System
			<-ctx.Done()
			return provider.Response{}, ctx.Err()
		}}, nil
	}}
	s := c.newLLMSession(context.Background(), "")
	defer s.stop()
	for _, id := range []string{"a", "b"} {
		req := llmRequest(id)
		req.System = id
		s.request(req)
		<-started
	}
	// A third at once is refused; the first two keep running.
	s.request(llmRequest("c"))
	if done := doneOf(t, s); done.ID != "c" || !strings.Contains(done.Error, "at once") {
		t.Fatalf("third call = %+v", done)
	}
	s.cancel("a")
	if done := doneOf(t, s); done.ID != "a" || done.Error != "cancelled" {
		t.Fatalf("cancelled call = %+v", done)
	}
	s.stop() // ends b too, without waiting on anyone to read its answer
}

func TestLLMLimiter(t *testing.T) {
	var l llmLimiter
	now := time.Now()
	for i := 0; i < llmRatePerMinute; i++ {
		if !l.allow(now) {
			t.Fatalf("call %d refused", i+1)
		}
	}
	if l.allow(now) {
		t.Fatal("over the limit allowed")
	}
	if !l.allow(now.Add(61 * time.Second)) {
		t.Fatal("a minute later still refused")
	}
}

func TestCopilotSettings(t *testing.T) {
	t.Setenv(CopilotKeyEnv, "")
	var c Config
	if err := c.Set("copilotProvider", []string{"OpenAI"}); err != nil || c.CopilotProvider != "openai" {
		t.Fatalf("provider = %q, %v", c.CopilotProvider, err)
	}
	if err := c.Set("copilotProvider", []string{"gemini"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	for value, ok := range map[string]bool{
		"https://api.example.com/v1": true, "http://localhost:1234/v1": true, "http://127.0.0.1:11434": true,
		"http://192.168.1.20:1234/v1": true, "http://mac-mini.local:1234/v1": true,
		"http://api.example.com/v1": false, "ftp://example.com": false, "https://user:pw@example.com": false, "nonsense": false,
	} {
		err := c.Set("copilotBaseURL", []string{value})
		if (err == nil) != ok {
			t.Errorf("copilotBaseURL %q: err = %v, want ok=%v", value, err, ok)
		}
	}
	if err := c.Set("copilotModel", []string{"two words"}); err == nil {
		t.Fatal("a model with a space accepted")
	}

	// Without a key, a self-hosted OpenAI-compatible endpoint still counts;
	// OpenAI itself does not.
	c = Config{CopilotProvider: "openai", CopilotBaseURL: "http://localhost:1234/v1"}
	if _, _, reason := c.CopilotLLM(); reason != "" {
		t.Fatalf("keyless local endpoint: %s", reason)
	}
	c = Config{CopilotProvider: "openai"}
	if _, _, reason := c.CopilotLLM(); reason == "" {
		t.Fatal("OpenAI without a key accepted")
	}
	t.Setenv(CopilotKeyEnv, "sk-env")
	if info, key, reason := c.CopilotLLM(); reason != "" || key != "sk-env" || info.Model != provider.DefaultModel("openai") {
		t.Fatalf("env key: %+v %q %q", info, key, reason)
	}
}

// The hello says what the runner calls, never the key.
func TestHelloOffersLLMWithoutTheKey(t *testing.T) {
	t.Setenv(CopilotKeyEnv, "")
	cfg := copilotConfig()
	info := BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if !slices.Contains(info.Capabilities, runnerwire.CapLLM) || info.LLM == nil || info.LLM.Model != "gpt-test" {
		t.Fatalf("hello = %+v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "sk-here") {
		t.Fatalf("the hello carries the key: %s", raw)
	}
	cfg.CopilotProvider = ""
	info = BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if slices.Contains(info.Capabilities, runnerwire.CapLLM) || info.LLM != nil {
		t.Fatalf("hello without a copilot = %+v", info)
	}
}
