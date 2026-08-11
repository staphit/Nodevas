package mcp

import (
	"context"
	"net/url"
	"strconv"

	"nodevas/internal/engine"
)

// The typed calls the tools are built from. Each one is a projection: the
// server answers with everything a browser needs, and almost none of that is
// worth an agent's context. Trimming happens here rather than in the tools, so
// there is one place to look for what an agent can and cannot see.

// ProjectInfo is one project in the workspace.
type ProjectInfo struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Depth    int    `json:"depth,omitempty"`
	Nodes    int    `json:"nodes"`
	IsFolder bool   `json:"isFolder,omitempty"`
}

// Projects lists what this server holds.
//
// The host path each project sits at is deliberately dropped. It is the one
// field in the catalog that says something about the operator's machine rather
// than about the work, and an agent has no use for it.
func (c *Client) Projects(ctx context.Context) ([]ProjectInfo, error) {
	var payload struct {
		Projects []struct {
			ProjectInfo
			Path string `json:"path"`
		} `json:"projects"`
		Active string `json:"active"`
	}
	if err := c.get(ctx, "/api/projects", nil, &payload); err != nil {
		return nil, err
	}
	projects := make([]ProjectInfo, 0, len(payload.Projects))
	for _, entry := range payload.Projects {
		projects = append(projects, entry.ProjectInfo)
	}
	return projects, nil
}

// ReadyResult is the queue plus enough context to read an empty one correctly.
type ReadyResult struct {
	Tasks   []engine.ReadyNode `json:"tasks"`
	Blocked []engine.ReadyNode `json:"blocked,omitempty"`
	Ready   int                `json:"ready"`
	Waiting int                `json:"waiting"`
	Busy    int                `json:"busy"`
	Cursor  string             `json:"cursor,omitempty"`
}

// ReadyQuery narrows the queue.
type ReadyQuery struct {
	Assignee       string
	Tag            string
	Limit          int
	Cursor         string
	IncludeBlocked bool
}

// Ready asks what can be worked on now.
func (c *Client) Ready(ctx context.Context, query ReadyQuery) (*ReadyResult, error) {
	values := url.Values{}
	if query.Assignee != "" {
		values.Set("assignee", query.Assignee)
	}
	if query.Tag != "" {
		values.Set("tag", query.Tag)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	if query.IncludeBlocked {
		values.Set("includeBlocked", "true")
	}
	result := &ReadyResult{}
	if err := c.get(ctx, "/api/ready", values, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Snapshot is the graph as the server currently holds it.
type Snapshot struct {
	Graph    *engine.Graph            `json:"graph"`
	Rev      string                   `json:"rev"`
	Statuses map[string]engine.Status `json:"statuses"`
	Issues   []engine.Issue           `json:"issues"`
}

// Graph fetches the whole graph.
//
// This is the fat call — a large board's UI state alone runs to tens of
// thousands of tokens — and it never reaches the agent. It is read here, inside
// a process with no context window, and only the handful of fields a tool
// promised are passed on.
func (c *Client) Graph(ctx context.Context) (*Snapshot, error) {
	snapshot := &Snapshot{}
	if err := c.get(ctx, "/api/graph", nil, snapshot); err != nil {
		return nil, err
	}
	if snapshot.Graph == nil {
		return nil, &APIError{
			Code:    CodeServerError,
			Message: "the server returned no graph for this project",
		}
	}
	return snapshot, nil
}

// NodeContent is one node's markdown file.
type NodeContent struct {
	Content string `json:"content"`
	Rev     string `json:"rev"`
}

// NodeContent reads the file behind a node.
func (c *Client) NodeContent(ctx context.Context, id string) (*NodeContent, error) {
	body := &NodeContent{}
	if err := c.get(ctx, "/api/nodes/"+url.PathEscape(id), nil, body); err != nil {
		return nil, err
	}
	return body, nil
}

// SearchHit is one match.
type SearchHit struct {
	Project string `json:"project"`
	NodeID  string `json:"nodeId,omitempty"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// Search looks through the workspace.
//
// The endpoint searches every project on the server, so the results are
// filtered back down to the one this process is pinned to. Letting them through
// would quietly hand the agent node ids it cannot then read.
func (c *Client) Search(ctx context.Context, term string) ([]SearchHit, error) {
	values := url.Values{}
	values.Set("q", term)
	var payload struct {
		Results []SearchHit `json:"results"`
	}
	if err := c.get(ctx, "/api/search", values, &payload); err != nil {
		return nil, err
	}
	if c.project == "" {
		return payload.Results, nil
	}
	kept := make([]SearchHit, 0, len(payload.Results))
	for _, hit := range payload.Results {
		if hit.Project == c.project {
			kept = append(kept, hit)
		}
	}
	return kept, nil
}

// Validate returns the graph's outstanding problems.
func (c *Client) Validate(ctx context.Context) ([]engine.Issue, error) {
	var payload struct {
		Issues []engine.Issue `json:"issues"`
	}
	if err := c.get(ctx, "/api/validate", nil, &payload); err != nil {
		return nil, err
	}
	return payload.Issues, nil
}
