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
	Kind        string   `json:"kind,omitempty" jsonschema:"type id: task (default), goal, scene, event, gate (review, a person finishes it), choice (decision), start, end (complete automatically), note (reference material, no status); or a custom-type-* id listed in get_graph_outline nodeTypes"`
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
	Kind        *string  `json:"kind,omitempty" jsonschema:"type id as in create_node; agents cannot move a node into or out of a people-only/automatic/no-status type"`
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
	Remove bool   `json:"remove,omitempty" jsonschema:"remove this dependency (or reference) instead of adding it"`
	// Relation picks what the link means. "reference" is stored on to, the
	// node that uses from as context, and never blocks it.
	Relation string `json:"relation,omitempty" jsonschema:"\"\" (default): to waits for from; \"reference\": to uses from as context (e.g. a Note); never blocks"`
}

type linkNodesOutput struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note"`
}

type subgraphNodeInput struct {
	Key         string   `json:"key" jsonschema:"your name for it in this call: [A-Za-z0-9_.-]{1,64}"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind,omitempty" jsonschema:"as create_node"`
	Body        string   `json:"body,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Deadline    string   `json:"deadline,omitempty"`
	WriteAccess string   `json:"writeAccess,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty" jsonschema:"keys or node ids to finish first"`
	Refs        []string `json:"refs,omitempty" jsonschema:"keys or node ids of Notes it uses as context"`
}

type subgraphEdgeInput struct {
	From string `json:"from" jsonschema:"key or node id"`
	To   string `json:"to" jsonschema:"existing node id that waits for from"`
}

type createSubgraphInput struct {
	Nodes []subgraphNodeInput `json:"nodes"`
	Edges []subgraphEdgeInput `json:"edges,omitempty"`
}

type createSubgraphOutput struct {
	IDs     map[string]string       `json:"ids"`
	Created []store.SubgraphCreated `json:"created"`
	Note    string                  `json:"note"`
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

// maxSearchLimit is the most hits search_nodes returns (the server's cap).
const maxSearchLimit = 100

type validateInput struct{}

type validateOutput struct {
	Issues []engine.Issue `json:"issues"`
	Note   string         `json:"note"`
}

func registerAuthoringTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_node",
		Description: "Create a node with actionable instructions in body. dependsOn lists prerequisites. writeAccess sets permission: human > orchestrator > worker. Notes hold reference material (character sheets, setting) and never block; link them as references. For several nodes use create_subgraph.",
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
			return nil, createNodeOutput{}, guide(err)
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
		Description: "Update supplied metadata fields only. writeAccess enforces human > orchestrator > worker. kind takes a type id from get_graph_outline nodeTypes. Use update_node_body for text.",
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
			return nil, updateMetaOutput{}, guide(err)
		}
		return nil, updateMetaOutput{ID: in.ID, Changed: changed, Note: "Updated."}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "link_nodes",
		Description: "Make to wait for from, or remove that required dependency. relation \"reference\" instead makes to use from as context (a Note: character sheet, setting) without blocking. Optional/deprecated links require the editor.",
		Annotations: &mcp.ToolAnnotations{Title: "Link two nodes"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in linkNodesInput) (*mcp.CallToolResult, linkNodesOutput, error) {
		from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
		if from == "" || to == "" {
			return nil, linkNodesOutput{}, &APIError{Code: CodeInvalidArgument, Message: "from and to are required"}
		}
		relation := strings.TrimSpace(in.Relation)
		if relation != "" && relation != "reference" {
			return nil, linkNodesOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: `relation must be "" or "reference"; optional/deprecated need the editor`,
			}
		}
		if from == to {
			message := "a node cannot depend on itself"
			if relation == "reference" {
				message = "a node cannot reference itself"
			}
			return nil, linkNodesOutput{}, &APIError{Code: CodeInvalidArgument, Message: message}
		}
		var op store.GraphOp
		var note string
		switch {
		case relation == "reference" && in.Remove:
			op = store.GraphOp{Kind: "remove-ref", NodeID: to, Ref: from}
			note = fmt.Sprintf("%s no longer references %s.", to, from)
		case relation == "reference":
			op = store.GraphOp{Kind: "add-ref", NodeID: to, Ref: from}
			note = fmt.Sprintf("%s now uses %s as a reference.", to, from)
		case in.Remove:
			op = store.GraphOp{Kind: "remove-edge", From: from, To: to}
			note = fmt.Sprintf("%s no longer waits for %s.", to, from)
		default:
			op = store.GraphOp{Kind: "add-edge", From: from, To: to}
			note = fmt.Sprintf("%s now waits for %s.", to, from)
		}
		if err := client.ApplyOps(ctx, []store.GraphOp{op}); err != nil {
			return nil, linkNodesOutput{}, guide(err)
		}
		return nil, linkNodesOutput{From: from, To: to, Note: note}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_subgraph",
		Description: "Create up to 100 nodes and their wiring in one all-or-nothing call. Give each node a key; " +
			"dependsOn and refs may name keys or existing node ids. refs makes the node use a Note (character sheet, " +
			"setting) as context without blocking it. Returns the new ids by key. Not idempotent: after a transport " +
			"error, check get_graph_outline before retrying.",
		Annotations: &mcp.ToolAnnotations{Title: "Create several nodes at once"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createSubgraphInput) (*mcp.CallToolResult, createSubgraphOutput, error) {
		if len(in.Nodes) == 0 {
			return nil, createSubgraphOutput{}, &APIError{Code: CodeInvalidArgument, Message: "nodes is required"}
		}
		spec := store.SubgraphSpec{
			Nodes: make([]store.SubgraphNode, 0, len(in.Nodes)),
			Edges: make([]store.SubgraphEdge, 0, len(in.Edges)),
		}
		for _, node := range in.Nodes {
			spec.Nodes = append(spec.Nodes, store.SubgraphNode{
				Key: node.Key, Title: node.Title, Kind: node.Kind, Body: node.Body, Tags: node.Tags,
				Priority: node.Priority, Assignee: node.Assignee, Deadline: node.Deadline,
				WriteAccess: node.WriteAccess, DependsOn: node.DependsOn, Refs: node.Refs,
			})
		}
		for _, edge := range in.Edges {
			spec.Edges = append(spec.Edges, store.SubgraphEdge{From: edge.From, To: edge.To})
		}
		result, err := client.CreateSubgraph(ctx, spec)
		if err != nil {
			return nil, createSubgraphOutput{}, guide(err)
		}
		return nil, createSubgraphOutput{
			IDs:     result.IDs,
			Created: result.Created,
			Note: fmt.Sprintf("Created %d nodes, %d dependencies and %d references.",
				len(result.Created), result.Dependencies, result.References),
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_nodes",
		Description: "Find nodes by keywords or a whole question, Chinese or English. Ranked: exact title, then exact phrase, then shared terms (BM25). snippet is the best-matching excerpt. Searches only this project.",
		Annotations: readOnly("Search"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		term := strings.TrimSpace(in.Query)
		if len(term) < 2 {
			return nil, searchOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: "query must be at least two characters",
			}
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		limit = min(limit, maxSearchLimit)
		hits, err := client.Search(ctx, term, limit)
		if err != nil {
			return nil, searchOutput{}, err
		}
		out := searchOutput{Total: len(hits), Results: hits}
		if len(hits) > limit {
			out.Results = hits[:limit]
			out.Note = fmt.Sprintf("Showing %d of %d matches; narrow the query for the rest.", limit, len(hits))
		}
		if len(out.Results) > 0 && out.Results[0].Match == "terms" {
			out.Note = strings.TrimSpace(out.Note + " No exact phrase match; ranked by shared terms. Check with get_node.")
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_graph_outline",
		Description: "Page through node summaries and edges within the page. Large boards require status, assignee or tag filters. nodeTypes lists the types (built-in and this project's custom-type-*), with agentMode and completion; refs are a node's reference notes.",
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
		// Hints (severity info) are returned but are not problems.
		problems, hints := 0, 0
		for _, issue := range issues {
			if issue.Severity == "info" {
				hints++
			} else {
				problems++
			}
		}
		note := "No problems found."
		if problems > 0 {
			note = fmt.Sprintf("%d problem(s). Fix them before relying on the ready queue: "+
				"a cycle or a dangling reference makes it wrong, not just untidy.", problems)
		}
		if hints > 0 {
			note += fmt.Sprintf(" (%d hint(s))", hints)
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
