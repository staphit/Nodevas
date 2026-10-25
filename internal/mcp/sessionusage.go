package mcp

import (
	"context"
	"net/url"
)

// SessionModelUsage is what an external session used of one model, in total
// since it started: the hub replaces what it had with each report, so a
// report repeated or sent twice counts once.
type SessionModelUsage struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	// CostUSD is what the agent itself said the tokens cost, 0 when it did
	// not say (the hub estimates from its price table then).
	CostUSD float64 `json:"costUsd"`
}

// RecordSessionUsage reports an external session's cumulative token usage,
// per model, read from the agent's own transcript by `nodevas hook`. agent is
// the agent kind ("claude", "codex", …). Nothing is sent for no models.
func (c *Client) RecordSessionUsage(ctx context.Context, agent string, models []SessionModelUsage) error {
	if len(models) == 0 {
		return nil
	}
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	body := map[string]any{"actor": c.actor, "agent": agent, "models": models}
	return c.post(ctx, "/api/agents/sessions/"+url.PathEscape(sessionID)+"/usage", nil, body, nil)
}
