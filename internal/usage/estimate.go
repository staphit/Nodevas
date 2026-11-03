package usage

import "nodevas/internal/runnerwire"

// SourceEstimate marks a price that is Nodevas's own estimate for a model
// whose CLI reports no cost (Codex, Gemini, Antigravity): a list price on
// PricesAsOf that the owner can override with a custom price of the same
// model name.
const SourceEstimate = "estimate"

// AgentDefaultModel is the model a CLI runs when a run asks for "default"
// (or names no model, or one with no price): what its cost is estimated at.
var AgentDefaultModel = map[string]string{
	"codex":       "gpt-5.5",
	"gemini":      "gemini-2.5-pro",
	"antigravity": "gemini-2.5-pro",
}

func estimatePrice(model string, in, out, read, write float64) Price {
	p := price(model, in, out, read, write)
	p.Source = SourceEstimate
	return p
}

// RunCost is what a run's usage cost: the CLI's own figure when it reported
// one, else its tokens at the first priced model of u.Model,
// dispatchedModel and the agent's default. estimated marks a cost priced at
// an estimate price or for a CLI that reports none (Codex, Gemini,
// Antigravity). priced is false when none of those models has a price.
func RunCost(t Table, agent, dispatchedModel string, u runnerwire.Usage) (cost float64, estimated, priced bool) {
	if u.CostUSD > 0 {
		return u.CostUSD, false, true
	}
	tokens := Tokens{Input: int64(u.InputTokens), Output: int64(u.OutputTokens),
		CacheRead: int64(u.CacheReadTokens), CacheWrite: int64(u.CacheWriteTokens)}
	for _, model := range []string{u.Model, dispatchedModel, AgentDefaultModel[agent]} {
		if model == "" || normalizeModel(model) == "default" {
			continue
		}
		if p, ok := t.Lookup(model); ok {
			// An estimate: Nodevas's own price, or a CLI that never says
			// what it cost. A first-party list price for an agent that
			// does report is not.
			_, silent := AgentDefaultModel[agent]
			return p.Cost(tokens), silent || p.Source == SourceEstimate, true
		}
	}
	return 0, false, false
}
