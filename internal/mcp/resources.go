package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Resources and the prompt.
//
// A tool is something the model decides to call; a resource is something a
// person can attach. Both exist here for the outline, and that is not
// duplication: dropping the board into the conversation before asking a
// question is a different act from the model going and fetching it mid-task.

// resourceScope is the project name that appears in every URI this server
// serves. It is fixed at startup, and a request naming anything else is refused
// rather than quietly redirected: a URI that looks like it addresses another
// project and silently does not is worse than one that fails.
func resourceScope(client *Client) string {
	if name := client.Project(); name != "" {
		return name
	}
	return "active"
}

func registerResources(server *mcp.Server, client *Client) {
	scope := resourceScope(client)
	outlineURI := fmt.Sprintf("nodevas://project/%s/outline", scope)

	server.AddResource(&mcp.Resource{
		URI:         outlineURI,
		Name:        "board outline",
		Description: "First page of node summaries and dependencies. Continue with get_graph_outline and the returned cursor.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		outline, err := client.GraphOutline(ctx, OutlineFilter{})
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(outline)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      request.Params.URI,
			MIMEType: "application/json",
			Text:     string(encoded),
		}}}, nil
	})

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: fmt.Sprintf("nodevas://project/%s/node/{id}", scope),
		Name:        "node",
		Description: "One node's full markdown file.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, err := nodeIDFromURI(request.Params.URI, scope)
		if err != nil {
			return nil, err
		}
		content, err := client.NodeContent(ctx, id)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      request.Params.URI,
			MIMEType: "text/markdown",
			Text:     content.Content,
		}}}, nil
	})
}

// nodeIDFromURI pulls the id out of a node URI, checking the project along the
// way.
//
// The check is the point. Without it the template would accept
// nodevas://project/somewhere-else/node/x and serve this project's node x,
// which is a URI that lies about what it addresses.
func nodeIDFromURI(uri, scope string) (string, error) {
	const prefix = "nodevas://project/"
	if !strings.HasPrefix(uri, prefix) {
		return "", mcp.ResourceNotFoundError(uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	project, remainder, found := strings.Cut(rest, "/")
	if !found || project != scope {
		return "", fmt.Errorf(
			"this server only serves the %q project; %q names another one", scope, uri)
	}
	id, found := strings.CutPrefix(remainder, "node/")
	if !found || id == "" {
		return "", mcp.ResourceNotFoundError(uri)
	}
	return id, nil
}

// registerPrompt installs the one prompt: the loop, written out.
//
// The tool descriptions each say what one call does. What they cannot say is
// when to stop, or what to do with a queue that empties while work remains --
// and those are exactly where an unattended agent goes wrong.
func registerPrompt(server *mcp.Server, client *Client) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "work_the_queue",
		Description: "Work through the board's ready queue until nothing is left that you can do.",
		Arguments: []*mcp.PromptArgument{
			{Name: "assignee", Description: "only take tasks assigned to this person"},
			{Name: "tag", Description: "only take tasks carrying this tag"},
		},
	}, func(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		filter := ""
		if assignee := request.Params.Arguments["assignee"]; assignee != "" {
			filter += fmt.Sprintf("\nOnly take tasks assigned to %q.", assignee)
		}
		if tag := request.Params.Arguments["tag"]; tag != "" {
			filter += fmt.Sprintf("\nOnly take tasks tagged %q.", tag)
		}
		return &mcp.GetPromptResult{
			Description: "Work the ready queue on " + resourceScope(client),
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: workTheQueuePrompt + filter},
			}},
		}, nil
	})
}

const workTheQueuePrompt = `Work the ready queue:
1. get_ready_tasks; stop if empty.
2. claim_task; if already claimed, try another ready task.
3. get_node; read all pages before working or replacing the file.
4. Do the work; set_node_status to done, failed or skipped with an accurate note.
5. release_task if unfinished.
When nothing is ready, report waiting and busy counts. If tasks still wait, people are the blockers: name them. Do not start blocked work or invent tasks. Ask for clarification when the ticket is insufficient.`
