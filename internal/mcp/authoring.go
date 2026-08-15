package mcp

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// Planning calls: making nodes, editing their fields, wiring them together and
// reading the shape of the board.

// NewNode is what create_node sends.
type NewNode struct {
	Title    string   `json:"title,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	Deadline string   `json:"deadline,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Requires string   `json:"requires,omitempty"`
	Body     string   `json:"body,omitempty"`
	// WriteAccess is who may modify the node once it exists: "" (everyone),
	// "worker", "orchestrator" or "human-only". The server accepts "all" as a
	// spelled-out synonym for "".
	WriteAccess string `json:"writeAccess,omitempty"`
}

// CreateNode adds a node and returns the id the server gave it.
func (c *Client) CreateNode(ctx context.Context, node NewNode) (string, error) {
	var response struct {
		ID string `json:"id"`
	}
	if err := c.post(ctx, "/api/nodes", nil, node, &response); err != nil {
		return "", err
	}
	return response.ID, nil
}

// WriteNodeBody replaces a node's markdown, refusing if somebody changed it
// since baseRev.
func (c *Client) WriteNodeBody(ctx context.Context, id, content, baseRev string) (string, error) {
	var response struct {
		Rev string `json:"rev"`
	}
	body := map[string]any{"content": content, "baseRev": baseRev}
	err := c.put(ctx, "/api/nodes/"+url.PathEscape(id), nil, body, &response)
	if err != nil {
		return "", err
	}
	return response.Rev, nil
}

// ApplyOps sends a batch of fine-grained graph changes.
//
// Fine-grained rather than read-modify-write of the whole graph: a nil field
// means "leave alone", so an agent editing a node's priority and a person
// editing its title at the same moment do not overwrite each other. Replacing
// the whole document would make every concurrent edit a conflict.
func (c *Client) ApplyOps(ctx context.Context, ops []store.GraphOp) error {
	return c.post(ctx, "/api/graph/ops", nil, map[string]any{"ops": ops}, nil)
}

// OutlineNode is one node in the board's shape.
type OutlineNode struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	Assignee string `json:"assignee,omitempty"`
}

// OutlineEdge is one dependency.
type OutlineEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Relation is "" for a required edge, matching the stored form.
	Relation string `json:"relation,omitempty"`
}

// Outline is the graph with everything visual removed.
type Outline struct {
	Nodes []OutlineNode `json:"nodes"`
	Edges []OutlineEdge `json:"edges"`
	Total int           `json:"total"`
	// Cursor continues the node listing; edges are only included on the last
	// page, where the whole node set is known.
	Cursor  string `json:"cursor,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// outlineThreshold is where a board stops being something to read whole.
//
// Past it the outline answers with counts and a way to narrow, not with every
// node: a thousand-node graph pasted into a context window is not
// understanding, it is the appearance of it, and it crowds out the actual work.
const outlineThreshold = 200

// GraphOutline reads the board's shape.
//
// Everything in UIState — positions, wire bend points, node colours, groups,
// annotations, saved views — is dropped. It is most of the file by weight on a
// large board and none of it means anything to a caller that cannot see.
func (c *Client) GraphOutline(ctx context.Context, filter OutlineFilter) (*Outline, error) {
	snapshot, err := c.Graph(ctx)
	if err != nil {
		return nil, err
	}
	outline := &Outline{}
	known := map[string]bool{}
	for _, node := range snapshot.Graph.Nodes {
		status := string(statusOrReady(snapshot.Statuses, node.ID))
		if !filter.keeps(node, status) {
			continue
		}
		known[node.ID] = true
		outline.Nodes = append(outline.Nodes, OutlineNode{
			ID:       node.ID,
			Title:    node.Title,
			Kind:     node.Kind,
			Status:   status,
			Priority: node.Priority,
			Assignee: node.Assignee,
		})
	}
	sort.Slice(outline.Nodes, func(a, b int) bool {
		return outline.Nodes[a].ID < outline.Nodes[b].ID
	})
	outline.Total = len(outline.Nodes)

	if outline.Total > outlineThreshold && !filter.narrowed() {
		outline.Summary = fmt.Sprintf(
			"This board has %d nodes, too many to read at once. Narrow it with status, assignee or tag, "+
				"or use get_ready_tasks for what is actionable and search_nodes to find something specific.",
			outline.Total)
		outline.Nodes = nil
		return outline, nil
	}

	page, next := pageOutline(outline.Nodes, filter.Cursor, filter.Limit)
	outline.Nodes = page
	outline.Cursor = next
	// Edges are only meaningful against the nodes in hand; sending the ones
	// that point at nodes on another page would read as dangling.
	inPage := make(map[string]bool, len(page))
	for _, node := range page {
		inPage[node.ID] = true
	}
	for _, edge := range snapshot.Graph.Edges {
		if edge == nil {
			continue
		}
		if inPage[edge.From] && inPage[edge.To] {
			outline.Edges = append(outline.Edges, OutlineEdge{
				From: edge.From, To: edge.To, Relation: edge.Relation,
			})
		}
	}
	return outline, nil
}

// OutlineFilter narrows what the outline covers.
type OutlineFilter struct {
	Status   string
	Assignee string
	Tag      string
	Limit    int
	Cursor   string
}

func (f OutlineFilter) narrowed() bool {
	return f.Status != "" || f.Assignee != "" || f.Tag != ""
}

func (f OutlineFilter) keeps(node *engine.Node, status string) bool {
	if f.Status != "" && !strings.EqualFold(status, f.Status) {
		return false
	}
	if f.Assignee != "" && !strings.EqualFold(node.Assignee, f.Assignee) {
		return false
	}
	if f.Tag != "" {
		found := false
		for _, tag := range node.Tags {
			if strings.EqualFold(tag, f.Tag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// pageOutline walks the node list from the id after the cursor.
func pageOutline(nodes []OutlineNode, cursor string, limit int) ([]OutlineNode, string) {
	if limit <= 0 {
		limit = outlineThreshold
	}
	start := 0
	if cursor != "" {
		for index, node := range nodes {
			if node.ID == cursor {
				start = index + 1
				break
			}
		}
	}
	if start >= len(nodes) {
		return nil, ""
	}
	end := start + limit
	if end >= len(nodes) {
		return nodes[start:], ""
	}
	page := nodes[start:end]
	return page, page[len(page)-1].ID
}
