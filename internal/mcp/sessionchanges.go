package mcp

import (
	"context"
	"net/url"

	"nodevas/internal/runnerwire"
)

// ClaimedNode is the node the server said, at the last heartbeat, this
// session holds a live claim on; "" for none.
func (c *Client) ClaimedNode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claimedNode
}

// SessionChangesResult is the server's answer to RecordSessionChanges.
type SessionChangesResult struct {
	// Recorded is false when the session held no node to record against.
	Recorded bool   `json:"recorded"`
	NodeID   string `json:"nodeId,omitempty"`
}

// RecordSessionChanges reports, for an external session (W6), files its edit
// tools touched and/or a diff snapshot of its working tree. The server files
// them under the node the session holds when they arrive.
func (c *Client) RecordSessionChanges(ctx context.Context, files []string, snapshot *runnerwire.RunChanges) (SessionChangesResult, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	body := map[string]any{"actor": c.actor}
	if len(files) > 0 {
		body["files"] = files
	}
	if snapshot != nil {
		body["snapshot"] = snapshot
	}
	var out SessionChangesResult
	err := c.post(ctx, "/api/agents/sessions/"+url.PathEscape(sessionID)+"/changes", nil, body, &out)
	return out, err
}
