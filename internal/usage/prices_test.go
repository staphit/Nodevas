package usage

import (
	"errors"
	"math"
	"testing"
)

func TestLookup(t *testing.T) {
	table := NewTable(nil)
	for _, tc := range []struct {
		model string
		want  string // the entry that prices it; "" for none
	}{
		{"claude-opus-5-5", "claude-opus-5-5"},
		{"Claude-Opus-5-5", "claude-opus-5-5"},
		{"claude-opus-5-5[1m]", "claude-opus-5-5"},
		{"claude-opus-5-5-20260401", "claude-opus-5-5"},
		{"claude-opus-5", "claude-opus-5"},
		{"claude-opus-4-1-20250805", "claude-opus-4"},
		{"claude-opus-4-5-20251101", "claude-opus-4-5"},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4"},
		{"claude-haiku-4-5@20251001", "claude-haiku-4-5"},
		{"anthropic/claude-sonnet-5-5", "claude-sonnet-5-5"},
		{"us.anthropic.claude-haiku-5-5-v1:0", "claude-haiku-5-5"},
		{"opus", "opus"},
		{"opencode/deepseek-v4.1-flash-free", "opencode/*-free"},
		{"opencode/big-pickle", ""},
		// Estimate prices for CLIs that report no cost (estimate.go).
		{"gpt-5.5", "gpt-5.5"},
		{"gemini-3.8-flash-medium", ""},
		{"claude-opus-50", ""},
		{"", ""},
		{"unknown", ""},
	} {
		p, ok := table.Lookup(tc.model)
		if got := map[bool]string{true: p.Model, false: ""}[ok]; got != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestCustomPricesWin(t *testing.T) {
	table := NewTable([]Price{
		{Model: "gpt-5.5", InputPerMTok: 1.25, OutputPerMTok: 10},
		// Same entry as a built-in one: the custom one wins.
		{Model: "Claude-Opus-5-5", InputPerMTok: 1, OutputPerMTok: 2},
		{Model: "gemini-*", InputPerMTok: 0.5, OutputPerMTok: 1},
	})
	if p, ok := table.Lookup("gpt-5.5"); !ok || p.Source != SourceCustom || p.InputPerMTok != 1.25 {
		t.Errorf("gpt-5.5 = %+v %v", p, ok)
	}
	if p, ok := table.Lookup("claude-opus-5-5"); !ok || p.Source != SourceCustom || p.InputPerMTok != 1 {
		t.Errorf("custom opus = %+v %v", p, ok)
	}
	if p, ok := table.Lookup("gemini-3.8-flash-medium"); !ok || p.Model != "gemini-*" {
		t.Errorf("wildcard = %+v %v", p, ok)
	}
	// A longer built-in entry still beats a shorter custom one.
	table = NewTable([]Price{{Model: "claude", InputPerMTok: 99}})
	if p, _ := table.Lookup("claude-sonnet-5-5"); p.Model != "claude-sonnet-5-5" {
		t.Errorf("longer built-in lost to %+v", p)
	}
	list := table.Prices()
	if list[0].Source != SourceCustom || len(list) != len(builtinPrices)+1 {
		t.Errorf("Prices() = %d entries, first %+v", len(list), list[0])
	}
}

func TestCost(t *testing.T) {
	p, _ := NewTable(nil).Lookup("claude-opus-5-5")
	// 1M input at $4, 0.5M output at $20, 2M cache reads at $0.20, 0.1M
	// cache writes at $5: 4 + 10 + 0.4 + 0.5.
	got := p.Cost(Tokens{Input: 1_000_000, Output: 500_000, CacheRead: 2_000_000, CacheWrite: 100_000})
	if math.Abs(got-14.9) > 1e-9 {
		t.Fatalf("cost = %v, want 14.9", got)
	}
	if total := (Tokens{1, 2, 3, 4}).Total(); total != 10 {
		t.Fatalf("total = %d", total)
	}
}

func TestValidatePrices(t *testing.T) {
	ok := []Price{{Model: "gpt-5.5", InputPerMTok: 1}}
	if err := ValidatePrices(ok); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]Price{
		"negative":  {{Model: "m", InputPerMTok: -1}},
		"nan":       {{Model: "m", OutputPerMTok: math.NaN()}},
		"huge":      {{Model: "m", CacheReadPerMTok: MaxPricePerMTok + 1}},
		"empty":     {{Model: " "}},
		"twice":     {{Model: "m"}, {Model: "M"}},
		"two stars": {{Model: "a*b*"}},
		"control":   {{Model: "a\x01"}},
	} {
		if err := ValidatePrices(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := ValidatePrices(make([]Price, MaxCustomPrices+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many: %v", err)
	}
}
