package runner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// advisorConfig has a copilot on OpenAI and an advisor on Anthropic, each
// with its own key.
func advisorConfig() *Config {
	cfg := copilotConfig()
	cfg.AdvisorProvider, cfg.AdvisorModel, cfg.AdvisorAPIKey = "anthropic", "claude-haiku-5-5", "sk-advisor"
	return cfg
}

func advisorRequest(id string) *runnerwire.LLMRequest {
	req := llmRequest(id)
	req.Purpose = runnerwire.LLMPurposeAdvisor
	req.Provider, req.Model = "anthropic", "claude-haiku-5-5"
	return req
}

// An advisor call is made with the advisor's settings and key, a copilot
// call with the copilot's: neither ever borrows the other's endpoint.
func TestAdvisorCallsUseTheAdvisorSettings(t *testing.T) {
	t.Setenv(CopilotKeyEnv, "")
	t.Setenv(AdvisorKeyEnv, "")
	var seen []provider.Config
	c := &Client{Config: advisorConfig(), NewLLMProvider: func(cfg provider.Config) (provider.Provider, error) {
		seen = append(seen, cfg)
		return &llmFake{stream: func(context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
			return provider.Response{Message: provider.Message{Role: provider.RoleAssistant,
				Blocks: []provider.Block{{Type: provider.BlockText, Text: `{"implement":[]}`}}}, Stop: provider.StopEndTurn}, nil
		}}, nil
	}}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()

	s.request(advisorRequest("llm-a"))
	if done := doneOf(t, s); done.Error != "" {
		t.Fatalf("advisor call: %+v", done)
	}
	s.request(llmRequest("llm-c"))
	if done := doneOf(t, s); done.Error != "" {
		t.Fatalf("copilot call: %+v", done)
	}
	if len(seen) != 2 ||
		seen[0].Kind != "anthropic" || seen[0].Model != "claude-haiku-5-5" || seen[0].APIKey != "sk-advisor" ||
		seen[1].Kind != "openai" || seen[1].Model != "gpt-test" || seen[1].APIKey != "sk-here" {
		t.Fatalf("providers built = %+v", seen)
	}
}

func TestAdvisorCallsRefused(t *testing.T) {
	cases := []struct {
		name   string
		config func(*Config)
		req    func(*runnerwire.LLMRequest)
		want   string
	}{
		{"no advisor", func(c *Config) { c.AdvisorProvider = "" }, nil,
			"does not answer advisor model calls: advisorProvider is not set"},
		{"no advisor key", func(c *Config) { c.AdvisorAPIKey = "" }, nil, "no advisor key"},
		{"the copilot's model", nil, func(r *runnerwire.LLMRequest) { r.Provider, r.Model = "openai", "gpt-test" },
			"does not match this runner's advisor settings"},
		{"another endpoint", nil, func(r *runnerwire.LLMRequest) { r.BaseURL = "https://evil.example/v1" }, "does not match"},
		{"another account", nil, func(r *runnerwire.LLMRequest) { r.AccountID = "u-bob" }, "its owner's advisor only"},
		{"unknown purpose", nil, func(r *runnerwire.LLMRequest) { r.Purpose = "exfiltrate" }, "unknown purpose"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(CopilotKeyEnv, "")
			t.Setenv(AdvisorKeyEnv, "")
			cfg := advisorConfig()
			if tc.config != nil {
				tc.config(cfg)
			}
			c := &Client{Config: cfg, NewLLMProvider: func(provider.Config) (provider.Provider, error) {
				return nil, errors.New("must not be built")
			}}
			s := c.newLLMSession(context.Background(), "u-ann")
			defer s.stop()
			req := advisorRequest("llm-1")
			if tc.req != nil {
				tc.req(req)
			}
			s.request(req)
			if done := doneOf(t, s); !strings.Contains(done.Error, tc.want) {
				t.Fatalf("done = %+v, want an error with %q", done, tc.want)
			}
		})
	}
}

// The advisor's key: its own, then the environment, then the copilot's when
// both name the same provider, and never the copilot's for another provider.
func TestAdvisorKeySources(t *testing.T) {
	t.Setenv(CopilotKeyEnv, "")
	t.Setenv(AdvisorKeyEnv, "")
	c := Config{CopilotProvider: "anthropic", CopilotAPIKey: "sk-copilot", AdvisorProvider: "anthropic"}
	if key, source := c.AdvisorKey(); key != "sk-copilot" || source != "copilot" {
		t.Fatalf("shared key = %q from %q", key, source)
	}
	c.AdvisorProvider = "openai"
	if _, _, reason := c.AdvisorLLM(); !strings.Contains(reason, "no advisor key") {
		t.Fatalf("another provider borrowed the copilot key: %q", reason)
	}
	t.Setenv(AdvisorKeyEnv, "sk-env")
	if key, source := c.AdvisorKey(); key != "sk-env" || source != "environment" {
		t.Fatalf("env key = %q from %q", key, source)
	}
	c.AdvisorAPIKey = "sk-stored"
	if key, source := c.AdvisorKey(); key != "sk-stored" || source != "stored" {
		t.Fatalf("stored key = %q from %q", key, source)
	}
	if info, _, reason := c.AdvisorLLM(); reason != "" || info.Purpose != runnerwire.LLMPurposeAdvisor ||
		info.Model != provider.DefaultModel("openai") {
		t.Fatalf("advisor = %+v, %q", info, reason)
	}
}

func TestAdvisorSettings(t *testing.T) {
	var c Config
	if err := c.Set("advisorProvider", []string{"Anthropic"}); err != nil || c.AdvisorProvider != "anthropic" {
		t.Fatalf("provider = %q, %v", c.AdvisorProvider, err)
	}
	if err := c.Set("advisorModel", []string{"claude-haiku-5-5"}); err != nil || c.AdvisorModel != "claude-haiku-5-5" {
		t.Fatalf("model = %q, %v", c.AdvisorModel, err)
	}
	if err := c.Set("advisorBaseURL", []string{"http://api.example.com/v1"}); err == nil ||
		!strings.Contains(err.Error(), "advisorBaseURL") {
		t.Fatalf("plain http to a public host: %v", err)
	}
	if err := c.Set("advisorBaseURL", []string{"http://mac-mini.local:1234/v1"}); err != nil {
		t.Fatalf("LAN endpoint: %v", err)
	}
	if c.CopilotProvider != "" || c.CopilotBaseURL != "" {
		t.Fatalf("advisor settings touched the copilot's: %+v", c)
	}
	if err := c.Set("advisorProvider", []string{"none"}); err != nil || c.AdvisorProvider != "" {
		t.Fatalf("clearing: %q, %v", c.AdvisorProvider, err)
	}
}

// The hello names the advisor model and its purpose, never its key.
func TestHelloOffersTheAdvisorWithoutTheKey(t *testing.T) {
	t.Setenv(CopilotKeyEnv, "")
	t.Setenv(AdvisorKeyEnv, "")
	cfg := advisorConfig()
	info := BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if !slices.Contains(info.Capabilities, runnerwire.CapLLMAdvisor) || info.Advisor == nil ||
		info.Advisor.Model != "claude-haiku-5-5" || info.Advisor.Purpose != runnerwire.LLMPurposeAdvisor {
		t.Fatalf("hello = %+v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "sk-advisor") || strings.Contains(string(raw), "sk-here") {
		t.Fatalf("the hello carries a key: %s", raw)
	}
	cfg.AdvisorProvider = ""
	info = BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if slices.Contains(info.Capabilities, runnerwire.CapLLMAdvisor) || info.Advisor != nil {
		t.Fatalf("hello without an advisor = %+v", info)
	}
}
