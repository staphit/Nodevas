package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/engine"
)

// The read-only tools.
//
// Every one of them answers a question an agent actually has, rather than
// wrapping an HTTP endpoint. There are about sixty endpoints on the server and
// most of them — drafts, trash, history, Drive backup, the file picker — mean
// nothing to an agent; exposing them would spend the model's context on a menu
// it will never order from.

// Body size limits for get_node. The default is generous enough for a real
// ticket and small enough that reading three of them does not fill a context
// window.
const (
	defaultBodyChars = 8_000
	maxBodyChars     = 60_000
)

// listProjectsInput has no fields: this is the one tool that is not scoped to
// the startup project, because its whole purpose is to say what exists.
type listProjectsInput struct{}

type listProjectsOutput struct {
	Projects []ProjectInfo `json:"projects"`
	Scope    string        `json:"scope"`
	Note     string        `json:"note,omitempty"`
}

type readyTasksInput struct {
	Assignee string `json:"assignee,omitempty" jsonschema:"only tasks assigned to this person"`
	Tag      string `json:"tag,omitempty" jsonschema:"only tasks carrying this tag"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many tasks to return (default 20, max 200)"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"continue after this task id, from a previous call's cursor"`
	Why      bool   `json:"why,omitempty" jsonschema:"also list the blocked tasks and what each is waiting on"`
}

type readyTasksOutput struct {
	Tasks   []engine.ReadyNode `json:"tasks"`
	Blocked []engine.ReadyNode `json:"blocked,omitempty"`
	Ready   int                `json:"ready"`
	Waiting int                `json:"waiting"`
	Busy    int                `json:"busy"`
	Cursor  string             `json:"cursor,omitempty"`
	Summary string             `json:"summary"`
}

type getNodeInput struct {
	ID           string `json:"id" jsonschema:"the node id, as returned by get_ready_tasks"`
	MaxBodyChars int    `json:"maxBodyChars,omitempty" jsonschema:"truncate the body to this many characters (default 8000)"`
	Offset       int    `json:"offset,omitempty" jsonschema:"start the body at this character, to continue a truncated read"`
}

type getNodeOutput struct {
	ID         string   `json:"id"`
	Title      string   `json:"title,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Status     string   `json:"status"`
	Priority   string   `json:"priority,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	Deadline   string   `json:"deadline,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Requires   string   `json:"requires,omitempty"`
	Upstream   []string `json:"upstream,omitempty"`
	Downstream []string `json:"downstream,omitempty"`
	Body       string   `json:"body"`
	// Rev is what a later write must present as its baseRev. Without it the
	// write cannot tell a fresh document from one somebody edited in between.
	Rev        string `json:"rev,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	NextOffset int    `json:"nextOffset,omitempty"`
}

func registerReadTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_projects",
		Description: "List the projects on this Nodevas server. Every other tool acts on the one project this server was started with; this is only to see what exists.",
		Annotations: readOnly("List projects"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listProjectsInput) (*mcp.CallToolResult, listProjectsOutput, error) {
		projects, err := client.Projects(ctx)
		if err != nil {
			return nil, listProjectsOutput{}, err
		}
		out := listProjectsOutput{Projects: projects, Scope: client.Project()}
		if client.Project() == "" {
			out.Scope = "(the server's active project)"
		}
		out.Note = "Tools cannot be pointed at another project. Restart `nodevas mcp` with --project to change it."
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_ready_tasks",
		Description: "What can be worked on right now. " +
			"A task appears only when nobody has started it, its prerequisites are finished, and its stated condition holds — so this is a safe list to pick from. " +
			"Bodies are not included; read one with get_node before starting. " +
			"If the list is empty, look at `waiting`: tasks still blocked mean somebody else has to move first, and inventing work is the wrong response.",
		Annotations: readOnly("Ready tasks"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readyTasksInput) (*mcp.CallToolResult, readyTasksOutput, error) {
		result, err := client.Ready(ctx, ReadyQuery{
			Assignee:       in.Assignee,
			Tag:            in.Tag,
			Limit:          in.Limit,
			Cursor:         in.Cursor,
			IncludeBlocked: in.Why,
		})
		if err != nil {
			return nil, readyTasksOutput{}, err
		}
		return nil, readyTasksOutput{
			Tasks:   result.Tasks,
			Blocked: result.Blocked,
			Ready:   result.Ready,
			Waiting: result.Waiting,
			Busy:    result.Busy,
			Cursor:  result.Cursor,
			Summary: summarize(result),
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_node",
		Description: "Read one node in full: its metadata, its markdown body, and which nodes lead into and out of it. " +
			"Returns a `rev`; keep it, because writing the body back requires it.",
		Annotations: readOnly("Read a node"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getNodeInput) (*mcp.CallToolResult, getNodeOutput, error) {
		out, err := readNode(ctx, client, in)
		if err != nil {
			return nil, getNodeOutput{}, err
		}
		return nil, out, nil
	})
}

// summarize states the queue in one sentence.
//
// The counts alone are ambiguous in the case that matters: zero ready with
// twelve waiting is a very different situation from zero of everything, and a
// model skimming a JSON blob is exactly the reader that will miss the
// difference.
func summarize(result *ReadyResult) string {
	switch {
	case result.Ready > 0:
		return fmt.Sprintf("%d task(s) ready, %d blocked by prerequisites or conditions, %d already under way.",
			result.Ready, result.Waiting, result.Busy)
	case result.Waiting > 0:
		return fmt.Sprintf(
			"Nothing is actionable: all %d remaining task(s) are blocked by prerequisites or conditions. "+
				"Report the blockers rather than starting blocked work.",
			result.Waiting)
	case result.Busy > 0:
		return "Nothing is actionable and nothing is waiting: every task has already been started or finished."
	default:
		return "This project has no tasks."
	}
}

func readNode(ctx context.Context, client *Client, in getNodeInput) (getNodeOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return getNodeOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
	}
	snapshot, err := client.Graph(ctx)
	if err != nil {
		return getNodeOutput{}, err
	}
	node := snapshot.Graph.NodeByID(id)
	if node == nil {
		return getNodeOutput{}, &APIError{
			Code:    CodeNotFound,
			Message: fmt.Sprintf("no node %q in this project", id),
		}
	}
	content, err := client.NodeContent(ctx, id)
	if err != nil {
		return getNodeOutput{}, err
	}

	out := getNodeOutput{
		ID:       node.ID,
		Title:    node.Title,
		Kind:     node.Kind,
		Priority: node.Priority,
		Assignee: node.Assignee,
		Deadline: node.Deadline,
		Tags:     node.Tags,
		Requires: node.Requires,
		Rev:      content.Rev,
		Status:   string(statusOrReady(snapshot.Statuses, node.ID)),
	}
	for _, edge := range snapshot.Graph.Edges {
		if !edge.IsPrerequisite() {
			continue
		}
		if edge.To == node.ID {
			out.Upstream = append(out.Upstream, edge.From)
		}
		if edge.From == node.ID {
			out.Downstream = append(out.Downstream, edge.To)
		}
	}
	out.Body, out.Truncated, out.NextOffset = sliceBody(content.Content, in.Offset, in.MaxBodyChars)
	return out, nil
}

func statusOrReady(statuses map[string]engine.Status, id string) engine.Status {
	if status, ok := statuses[id]; ok {
		return status
	}
	return engine.StatusReady
}

// sliceBody cuts a body down to the requested window and says whether there is
// more.
//
// It counts runes, not bytes, so a limit never lands in the middle of a
// character and hands back a body with a replacement glyph where a word was.
func sliceBody(body string, offset, limit int) (string, bool, int) {
	if limit <= 0 {
		limit = defaultBodyChars
	}
	if limit > maxBodyChars {
		limit = maxBodyChars
	}
	if offset < 0 {
		offset = 0
	}
	runes := []rune(body)
	if offset >= len(runes) {
		return "", false, 0
	}
	end := offset + limit
	if end >= len(runes) {
		return string(runes[offset:]), false, 0
	}
	return string(runes[offset:end]), true, end
}
