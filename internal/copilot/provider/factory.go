package provider

import (
	"errors"
	"fmt"
	"strings"
)

// Provider kinds accepted in Config.Kind.
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
)

// DefaultOpenAIBaseURL is where an "openai" provider points when no base URL is
// configured. Any other base URL is taken to be a self-hosted or third-party
// compatible server, which is why only this one insists on an API key.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// DefaultEffort is the effort sent when none is configured. A voice turn is
// waiting on the first spoken word, so latency wins over depth by default.
const DefaultEffort = "low"

// DefaultModel is the model a provider kind uses when Config.Model is empty.
// An unknown kind has no default.
func DefaultModel(kind string) string {
	switch normalizeKind(kind) {
	case KindAnthropic:
		return "claude-opus-5-5"
	case KindOpenAI:
		return "gpt-5"
	}
	return ""
}

// New builds the provider Config names, filling in defaults. It fails on an
// unknown kind and on a missing key where the endpoint is known to need one,
// so a misconfiguration surfaces when the copilot starts rather than as an
// opaque 401 on the first spoken request.
func New(cfg Config) (Provider, error) {
	kind := normalizeKind(cfg.Kind)
	if cfg.Model == "" {
		cfg.Model = DefaultModel(kind)
	}
	if cfg.Effort == "" {
		cfg.Effort = DefaultEffort
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)

	switch kind {
	case KindAnthropic:
		if cfg.APIKey == "" {
			return nil, errors.New("copilot provider anthropic: API key is required")
		}
		return newAnthropic(cfg), nil
	case KindOpenAI:
		if cfg.BaseURL == "" {
			cfg.BaseURL = DefaultOpenAIBaseURL
		}
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
		// Ollama, LM Studio and vLLM on a local port take no key; OpenAI itself
		// always does, so an empty key there can only be a mistake.
		if cfg.APIKey == "" && cfg.BaseURL == DefaultOpenAIBaseURL {
			return nil, errors.New("copilot provider openai: API key is required for " + DefaultOpenAIBaseURL)
		}
		return newOpenAI(cfg), nil
	}
	return nil, fmt.Errorf("copilot provider: unknown kind %q (want %q or %q)", cfg.Kind, KindAnthropic, KindOpenAI)
}

// normalizeKind maps the empty kind to the default vendor and tolerates case
// and stray whitespace from a hand-edited config file.
func normalizeKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return KindAnthropic
	}
	return kind
}
