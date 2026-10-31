package mcp

import (
	"context"
	"net/url"
	"strconv"

	"nodevas/internal/engine"
	"nodevas/internal/rag"
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
	// ForPeople counts ready nodes withheld from an agent because their type
	// is not worked by agents (reviews, decisions, goals).
	ForPeople int `json:"forPeople,omitempty"`
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
	// Layout and NodeTypes come with the outline view only.
	Layout    string            `json:"layout,omitempty"`
	NodeTypes []engine.NodeType `json:"nodeTypes,omitempty"`
}

// Graph fetches the whole graph for callers that explicitly need a snapshot.
func (c *Client) Graph(ctx context.Context) (*Snapshot, error) {
	return c.graph(ctx, nil)
}

func (c *Client) graph(ctx context.Context, query url.Values) (*Snapshot, error) {
	snapshot := &Snapshot{}
	if err := c.get(ctx, "/api/graph", query, snapshot); err != nil {
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

type NodeContext struct {
	Node *engine.Node `json:"node"`
	NodeContent
	Status     string   `json:"status"`
	Upstream   []string `json:"upstream"`
	Downstream []string `json:"downstream"`
	// NodeType is the node's resolved type; a server older than node types
	// sends none.
	NodeType *engine.NodeType `json:"nodeType,omitempty"`
	// References are the notes this node uses as context, summarized;
	// ReferencedBy the nodes that use this one.
	References          []ReferenceSummary `json:"references,omitempty"`
	ReferencesTruncated bool               `json:"referencesTruncated,omitempty"`
	ReferencedBy        []string           `json:"referencedBy,omitempty"`
}

// ReferenceSummary is one reference note as the server summarizes it
// (store.ReferenceSummary, mirrored so this package reads the wire format).
type ReferenceSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	TypeLabel string `json:"typeLabel,omitempty"`
	Summary   string `json:"summary"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (c *Client) NodeContext(ctx context.Context, id string) (*NodeContext, error) {
	out := &NodeContext{}
	if err := c.get(ctx, "/api/nodes/"+url.PathEscape(id)+"/context", nil, out); err != nil {
		return nil, err
	}
	if out.Node == nil || out.Node.ID != id {
		return nil, &APIError{Code: CodeServerError, Message: "server returned no matching node context"}
	}
	return out, nil
}

func (c *Client) DocumentSources(ctx context.Context) ([]rag.Source, error) {
	var out struct {
		Documents []rag.Source `json:"documents"`
	}
	if err := c.get(ctx, "/api/documents", nil, &out); err != nil {
		return nil, err
	}
	return out.Documents, nil
}

// SearchHit is one match. Score (higher is better) and Match ("title",
// "phrase" or "terms") come from the ranked search; Snippet is the
// best-matching excerpt.
type SearchHit struct {
	Project string  `json:"project"`
	NodeID  string  `json:"nodeId,omitempty"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet,omitempty"`
	Kind    string  `json:"kind,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Match   string  `json:"match,omitempty"`
}

// Search ranks the project's nodes against a query: keywords or a whole
// question, in any language.
//
// The endpoint can search every project on the server, so the query names
// this process's project, and the results are still filtered back down to it.
// Letting others through would quietly hand the agent node ids it cannot then
// read.
func (c *Client) Search(ctx context.Context, term string, limit int) ([]SearchHit, error) {
	values := url.Values{}
	values.Set("q", term)
	values.Set("mode", "ranked")
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if c.project != "" {
		values.Set("project", c.project)
	}
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
