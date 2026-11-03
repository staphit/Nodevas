package usage

import (
	"testing"

	"nodevas/internal/runnerwire"
)

// B-02: CLIs that report $0 (Codex, Gemini, Antigravity) are costed from
// their tokens at an estimate price, flagged as an estimate.
func TestRunCostEstimatesUnreportedCLIs(t *testing.T) {
	table := NewTable(nil)
	if p, ok := table.Lookup("gpt-5.5"); !ok || p.Source != SourceEstimate || p.InputPerMTok != 1.25 || p.OutputPerMTok != 10 {
		t.Fatalf("gpt-5.5 estimate price = %+v, %v", p, ok)
	}
	for _, model := range []string{"gpt-5.4-mini", "gemini-2.5-pro", "gemini-2.5-flash"} {
		if p, ok := table.Lookup(model); !ok || p.Source != SourceEstimate {
			t.Errorf("%s: %+v, %v; want an estimate price", model, p, ok)
		}
	}

	// Codex on "default": gpt-5.5, 1M in at $1.25 + 100k out at $10.
	cost, estimated, priced := RunCost(table, "codex", "default", runnerwire.Usage{InputTokens: 1_000_000, OutputTokens: 100_000})
	if !near(cost, 2.25) || !estimated || !priced {
		t.Fatalf("codex default = %v estimated %v priced %v, want 2.25 true true", cost, estimated, priced)
	}
	// The model the CLI reported wins over the dispatched one.
	cost, estimated, _ = RunCost(table, "codex", "default", runnerwire.Usage{InputTokens: 1_000_000, Model: "gpt-5.4-mini"})
	if !near(cost, 0.25) || !estimated {
		t.Fatalf("codex reporting gpt-5.4-mini = %v estimated %v, want 0.25", cost, estimated)
	}
	// An unpriced dispatched model falls back to the agent's default.
	cost, _, priced = RunCost(table, "gemini", "gemini-9-experimental", runnerwire.Usage{InputTokens: 1_000_000})
	if !near(cost, 1.25) || !priced {
		t.Fatalf("gemini unpriced model = %v priced %v, want the default's 1.25", cost, priced)
	}

	// A custom price of the same name overrides the estimate.
	custom := NewTable([]Price{{Model: "gemini-2.5-flash", InputPerMTok: 1, OutputPerMTok: 1}})
	cost, estimated, priced = RunCost(custom, "gemini", "gemini-2.5-flash", runnerwire.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	if !near(cost, 2) || !estimated || !priced {
		t.Fatalf("custom gemini flash = %v estimated %v priced %v, want 2 true true", cost, estimated, priced)
	}

	// Nothing priced: no cost, not an estimate, not priced.
	cost, estimated, priced = RunCost(table, "mystery", "mystery-1", runnerwire.Usage{InputTokens: 1_000_000})
	if cost != 0 || estimated || priced {
		t.Fatalf("unpriced = %v %v %v, want 0 false false", cost, estimated, priced)
	}

	// A reported cost wins and is not an estimate.
	cost, estimated, priced = RunCost(table, "codex", "default", runnerwire.Usage{InputTokens: 1_000_000, CostUSD: 0.40})
	if !near(cost, 0.40) || estimated || !priced {
		t.Fatalf("reported = %v estimated %v priced %v, want 0.40 false true", cost, estimated, priced)
	}
}
