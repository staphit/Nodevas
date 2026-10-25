package mcp

import (
	"context"
	"net/url"
	"strings"
	"time"

	"nodevas/internal/agents"
)

// The calls below serve `nodevas agents who|send|listen`: the same server and
// the same loopback-only client as the MCP tools, used from a terminal.

// Roster returns the project's agent sessions, offline ones too with all.
func (c *Client) Roster(ctx context.Context, all bool) ([]agents.Session, error) {
	q := url.Values{}
	if all {
		q.Set("all", "true")
	}
	if c.actor != "" {
		q.Set("actor", c.actor)
	}
	var resp struct {
		Sessions []agents.Session `json:"sessions"`
	}
	if err := c.get(ctx, "/api/agents", q, &resp); err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// OutgoingMessage is a message a person sends from a terminal.
type OutgoingMessage struct {
	To     string
	NodeID string
	Body   string
	Notify bool
	Files  []agents.FileInput
}

// SendAsHuman sends m with no sending session: the server takes that, from a
// client that declares no agent role, as a person writing.
func (c *Client) SendAsHuman(ctx context.Context, m OutgoingMessage) (agents.SendResult, error) {
	body := map[string]any{
		"to":      strings.TrimSpace(m.To),
		"message": m.Body,
	}
	if c.actor != "" {
		body["actor"] = c.actor
	}
	if strings.TrimSpace(m.NodeID) != "" {
		body["nodeId"] = strings.TrimSpace(m.NodeID)
	}
	if m.Notify {
		body["notify"] = true
	}
	if len(m.Files) > 0 {
		body["files"] = m.Files
	}
	var resp agents.SendResult
	if err := c.post(ctx, "/api/agents/messages", nil, body, &resp); err != nil {
		return agents.SendResult{}, err
	}
	return resp, nil
}

// WaitInbox long-polls session's inbox for up to wait and returns what
// arrived, marking it read. actor must be the session's own: the server
// refuses anyone else its mail.
func (c *Client) WaitInbox(ctx context.Context, session, actor string, wait time.Duration) ([]agents.Message, error) {
	seconds := int(wait / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds > int(agents.MaxInboxWait/time.Second) {
		seconds = int(agents.MaxInboxWait / time.Second)
	}
	body := map[string]any{
		"session":     session,
		"actor":       actor,
		"waitSeconds": seconds,
	}
	var resp struct {
		Messages []agents.Message `json:"messages"`
	}
	if err := c.doLongPoll(ctx, "/api/agents/inbox", body, &resp, time.Duration(seconds+15)*time.Second); err != nil {
		return nil, err
	}
	return resp.Messages, nil
}
