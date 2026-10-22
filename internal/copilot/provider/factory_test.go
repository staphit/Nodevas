package provider

import "testing"

func TestNewDefaults(t *testing.T) {
	p, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	a := p.(*anthropicProvider)
	if a.Name() != "anthropic" || a.Model() != "claude-opus-5-5" || a.effort != "low" || !a.fallback {
		t.Errorf("anthropic defaults = %+v", a)
	}

	p, err = New(Config{Kind: " OpenAI ", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	o := p.(*openaiProvider)
	if o.Name() != "openai" || o.base != DefaultOpenAIBaseURL || o.Model() != DefaultModel("openai") || o.effort != "low" {
		t.Errorf("openai defaults = %+v", o)
	}
}

func TestNewErrors(t *testing.T) {
	for name, cfg := range map[string]Config{
		"anthropic without key":   {Kind: "anthropic"},
		"default without key":     {},
		"openai.com without key":  {Kind: "openai"},
		"openai.com/ without key": {Kind: "openai", BaseURL: "https://api.openai.com/v1/"},
		"unknown kind":            {Kind: "gemini", APIKey: "k"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := New(Config{Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1"}); err != nil {
		t.Errorf("local server without key: %v", err)
	}
	if DefaultModel("nope") != "" || DefaultModel("") != "claude-opus-5-5" {
		t.Error("DefaultModel")
	}
}
