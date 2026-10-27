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

// RunCost is what a run's usage cost: the CLI's own figure when it reported
// one, else an estimate from its tokens at the first priced model of
// u.Model, dispatchedModel and the agent's default. priced is false when
// none of those has a price.
func RunCost(t Table, agent, dispatchedModel string, u runnerwire.Usage) (cost float64, estimated, priced bool) {
	// TODO(green): implement.
	return 0, false, false
}
