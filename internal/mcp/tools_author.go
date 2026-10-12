package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// The tools for planning rather than executing: making nodes, editing their
// fields, wiring them up, and looking around.
//
// Deliberately absent: anything that deletes. Deleted nodes go to the trash and
// can be restored, so the damage is recoverable — but an agent that misreads a
// board and tidies it up costs a person their afternoon either way, and the
// expected value of handing out that capability is negative. Deleting is done
// by the person whose board it is.

type createNodeInput struct {
	Title       string   `json:"title" jsonschema:"what the task is, in a few words"`
	Body        string   `json:"body,omitempty" jsonschema:"the markdown ticket: what to do, and how to tell when it is done"`
	Kind        string   `json:"kind,omitempty" jsonschema:"task, scene, choice, gate, start or end (default task)"`
	Priority    string   `json:"priority,omitempty" jsonschema:"urgent, high, medium or low"`
	Assignee    string   `json:"assignee,omitempty" jsonschema:"who should do it; must already be a person on this board"`
	Deadline    string   `json:"deadline,omitempty" jsonschema:"YYYY-MM-DD, or YYYY-MM-DDTHH:mm"`
	Tags        []string `json:"tags,omitempty"`
	Requires    string   `json:"requires,omitempty" jsonschema:"a condition that must hold before this is actionable, e.g. \"design and flag(approved)\""`
	DependsOn   []string `json:"dependsOn,omitempty" jsonschema:"node ids that must finish before this one can start"`
	WriteAccess string   `json:"writeAccess,omitempty" jsonschema:"who may modify this node: all|worker|orchestrator|human-only"`
}

type createNodeOutput struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

type updateBodyInput struct {
	ID      string `json:"id" jsonschema:"the node id"`
	Content string `json:"content" jsonschema:"the complete new file, frontmatter included; this replaces what is there"`
	BaseRev string `json:"baseRev" jsonschema:"the rev get_node returned, so a change made in the meantime is caught rather than overwritten"`
}

type updateBodyOutput struct {
	ID   string `json:"id"`
	Rev  string `json:"rev"`
	Note string `json:"note"`
}

type updateMetaInput struct {
	ID          string   `json:"id" jsonschema:"the node id"`
	Title       *string  `json:"title,omitempty"`
	Kind        *string  `json:"kind,omitempty"`
	Priority    *string  `json:"priority,omitempty" jsonschema:"urgent, high, medium or low"`
	Assignee    *string  `json:"assignee,omitempty"`
	Deadline    *string  `json:"deadline,omitempty" jsonschema:"YYYY-MM-DD, or empty to clear it"`
	Tags        []string `json:"tags,omitempty" jsonschema:"replaces the whole tag list"`
	WriteAccess *string  `json:"writeAccess,omitempty" jsonschema:"who may modify this node: all|worker|orchestrator|human-only"`
}

type updateMetaOutput struct {
	ID      string   `json:"id"`
	Changed []string `json:"changed"`
	Note    string   `json:"note"`
}

type linkNodesInput struct {
	From   string `json:"from" jsonschema:"the node that must finish first"`
	To     string `json:"to" jsonschema:"the node that waits for it"`
	Remove bool   `json:"remove,omitempty" jsonschema:"remove this dependency instead of adding it"`
}

type linkNodesOutput struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"text to look for in titles and bodies"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many hits to return (default 20)"`
}

type searchOutput struct {
	Results []SearchHit `json:"results"`
	Total   int         `json:"total"`
	Note    string      `json:"note,omitempty"`
}

type outlineInput struct {
	Status   string `json:"status,omitempty" jsonschema:"only nodes in this status"`
	Assignee string `json:"assignee,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Limit    int    `json:"limit,omitempty" jsonschema:"nodes per page (default 25, max 200)"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"continue after this node id"`
}

type validateInput struct{}

type validateOutput struct {
	Issues []engine.Issue `json:"issues"`
	Note   string         `json:"note"`
}

func registerAuthoringTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_node",
		Description: "Create a node with actionable instructions in body. dependsOn lists prerequisites. writeAccess sets permission: human > orchestrator > worker.",
		Annotations: &mcp.ToolAnnotations{Title: "Create a node"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createNodeInput) (*mcp.CallToolResult, createNodeOutput, error) {
		if strings.TrimSpace(in.Title) == "" {
			return nil, createNodeOutput{}, &APIError{Code: CodeInvalidArgument, Message: "title is required"}
		}
		id, err := client.CreateNode(ctx, NewNode{
			Title:       in.Title,
			Kind:        in.Kind,
			Priority:    in.Priority,
			Assignee:    in.Assignee,
			Deadline:    in.Deadline,
			Tags:        in.Tags,
			Requires:    in.Requires,
			Body:        in.Body,
			WriteAccess: in.WriteAccess,
		})
		if err != nil {
			return nil, createNodeOutput{}, err
		}
		note := "Created."
		if len(in.DependsOn) > 0 {
			ops := make([]store.GraphOp, 0, len(in.DependsOn))
			for _, parent := range in.DependsOn {
				ops = append(ops, store.GraphOp{Kind: "add-edge", From: parent, To: id})
			}
			if err := client.ApplyOps(ctx, ops); err != nil {
				// The node exists; saying otherwise would have the agent make
				// it a second time.
				return nil, createNodeOutput{
					ID: id,
					Note: fmt.Sprintf(
						"Created as %s, but its dependencies were not wired up: %v. Add them with link_nodes.",
						id, err),
				}, nil
			}
			note = fmt.Sprintf("Created, waiting on %s.", strings.Join(in.DependsOn, ", "))
		}
		return nil, createNodeOutput{ID: id, Note: note}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_node_body",
		Description: "Replace the entire Markdown file, including frontmatter. Read every page first. baseRev from get_node prevents overwriting concurrent edits.",
		Annotations: &mcp.ToolAnnotations{Title: "Rewrite a node's body"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateBodyInput) (*mcp.CallToolResult, updateBodyOutput, error) {
		if strings.TrimSpace(in.ID) == "" {
			return nil, updateBodyOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
		}
		if in.BaseRev == "" {
			return nil, updateBodyOutput{}, &APIError{
				Code: CodeInvalidArgument,
				Message: "baseRev is required: without it this would overwrite whatever is there now, " +
					"including an edit somebody made while you were working. Call get_node for it",
			}
		}
		rev, err := client.WriteNodeBody(ctx, in.ID, in.Content, in.BaseRev)
		if err != nil {
			return nil, updateBodyOutput{}, err
		}
		return nil, updateBodyOutput{ID: in.ID, Rev: rev, Note: "Saved. Use this rev for your next write."}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_node_meta",
		Description: "Update supplied metadata fields only. writeAccess enforces human > orchestrator > worker. Use update_node_body for text.",
		Annotations: &mcp.ToolAnnotations{Title: "Edit a node's fields"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateMetaInput) (*mcp.CallToolResult, updateMetaOutput, error) {
		if strings.TrimSpace(in.ID) == "" {
			return nil, updateMetaOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
		}
		op := store.GraphOp{
			Kind:        "node-metadata",
			NodeID:      in.ID,
			Title:       in.Title,
			Kind_:       in.Kind,
			Priority:    in.Priority,
			Assignee:    in.Assignee,
			Deadline:    in.Deadline,
			WriteAccess: in.WriteAccess,
		}
		changed := namedFields(in)
		if in.Tags != nil {
			tags := in.Tags
			op.Tags = &tags
		}
		if len(changed) == 0 {
			return nil, updateMetaOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: "no fields to change were given",
			}
		}
		if err := client.ApplyOps(ctx, []store.GraphOp{op}); err != nil {
			return nil, updateMetaOutput{}, err
		}
		return nil, updateMetaOutput{ID: in.ID, Changed: changed, Note: "Updated."}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "link_nodes",
		Description: "Make to wait for from, or remove that required dependency. Optional/deprecated links require the editor.",
		Annotations: &mcp.ToolAnnotations{Title: "Link two nodes"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in linkNodesInput) (*mcp.CallToolResult, linkNodesOutput, error) {
		from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
		if from == "" || to == "" {
			return nil, linkNodesOutput{}, &APIError{Code: CodeInvalidArgument, Message: "from and to are required"}
		}
		if from == to {
			return nil, linkNodesOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: "a node cannot depend on itself",
			}
		}
		kind := "add-edge"
		note := fmt.Sprintf("%s now waits for %s.", to, from)
		if in.Remove {
			kind = "remove-edge"
			note = fmt.Sprintf("%s no longer waits for %s.", to, from)
		}
		if err := client.ApplyOps(ctx, []store.GraphOp{{Kind: kind, From: from, To: to}}); err != nil {
			return nil, linkNodesOutput{}, err
		}
		return nil, linkNodesOutput{From: from, To: to, Note: note}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_nodes",
		Description: "Find nodes by text in their titles and bodies. Searches only this project.",
		Annotations: readOnly("Search"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		term := strings.TrimSpace(in.Query)
		if len(term) < 2 {
			return nil, searchOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: "query must be at least two characters",
			}
		}
		hits, err := client.Search(ctx, term)
		if err != nil {
			return nil, searchOutput{}, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		out := searchOutput{Total: len(hits), Results: hits}
		if len(hits) > limit {
			out.Results = hits[:limit]
			out.Note = fmt.Sprintf("Showing %d of %d matches; narrow the query for the rest.", limit, len(hits))
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_graph_outline",
		Description: "Page through node summaries and edges within the page. Large boards require status, assignee or tag filters.",
		Annotations: readOnly("Board outline"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in outlineInput) (*mcp.CallToolResult, Outline, error) {
		outline, err := client.GraphOutline(ctx, OutlineFilter{
			Status:   in.Status,
			Assignee: in.Assignee,
			Tag:      in.Tag,
			Limit:    in.Limit,
			Cursor:   in.Cursor,
		})
		if err != nil {
			return nil, Outline{}, err
		}
		return nil, *outline, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "validate_graph",
		Description: "Check dependency cycles, missing nodes, duplicate IDs and invalid conditions. Run after changing dependencies.",
		Annotations: readOnly("Check the board"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ validateInput) (*mcp.CallToolResult, validateOutput, error) {
		issues, err := client.Validate(ctx)
		if err != nil {
			return nil, validateOutput{}, err
		}
		note := "No problems found."
		if len(issues) > 0 {
			note = fmt.Sprintf("%d problem(s). Fix them before relying on the ready queue: "+
				"a cycle or a dangling reference makes it wrong, not just untidy.", len(issues))
		}
		return nil, validateOutput{Issues: issues, Note: note}, nil
	})
}

// namedFields lists what the caller actually asked to change, which is what the
// answer reports back. A nil pointer means "leave alone", and reporting it as
// changed would be a lie the agent then repeats to a person.
func namedFields(in updateMetaInput) []string {
	var changed []string
	if in.Title != nil {
		changed = append(changed, "title")
	}
	if in.Kind != nil {
		changed = append(changed, "kind")
	}
	if in.Priority != nil {
		changed = append(changed, "priority")
	}
	if in.Assignee != nil {
		changed = append(changed, "assignee")
	}
	if in.Deadline != nil {
		changed = append(changed, "deadline")
	}
	if in.Tags != nil {
		changed = append(changed, "tags")
	}
	if in.WriteAccess != nil {
		changed = append(changed, "writeAccess")
	}
	return changed
}
