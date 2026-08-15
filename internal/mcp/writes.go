package mcp

import (
	"context"
	"net/url"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// The calls that change something.
//
// Every one of them sends the actor as the claim owner, taken from --actor and
// never from the tool arguments. An agent cannot name itself as somebody else's
// claim holder, because it never gets to say who the holder is.

// ClaimResponse is the answer to taking a node.
type ClaimResponse struct {
	Claim *store.Claim      `json:"claim"`
	Task  *engine.ReadyNode `json:"task,omitempty"`
}

// Claim takes a node for this agent and moves it to in_progress.
func (c *Client) Claim(ctx context.Context, nodeID string, lease time.Duration, requestID string) (*ClaimResponse, error) {
	body := map[string]any{"owner": c.actor}
	if lease > 0 {
		body["leaseSeconds"] = int(lease / time.Second)
	}
	if requestID != "" {
		body["requestId"] = requestID
	}
	response := &ClaimResponse{}
	if err := c.post(ctx, "/api/nodes/"+url.PathEscape(nodeID)+"/claim", nil, body, response); err != nil {
		return nil, err
	}
	return response, nil
}

// Release gives a node back unfinished.
func (c *Client) Release(ctx context.Context, nodeID string) error {
	return c.post(ctx, "/api/nodes/"+url.PathEscape(nodeID)+"/release", nil,
		map[string]any{"owner": c.actor}, nil)
}

// SetStatus reports what happened to a node this agent holds.
func (c *Client) SetStatus(ctx context.Context, nodeID, status, note, requestID string) error {
	body := map[string]any{
		"status": status,
		"by":     c.actor,
		"owner":  c.actor,
		"note":   note,
	}
	if requestID != "" {
		body["requestId"] = requestID
	}
	return c.post(ctx, "/api/nodes/"+url.PathEscape(nodeID)+"/status", nil, body, nil)
}
