package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Toolsets are the tool selections `nodevas mcp --toolset` serves.
//
// A coding agent pays for every tool definition on every model call, so a
// runner's run gets only the tools its prompt (internal/runner/adapter) and
// its job need. Everyone else — a person's own agent, `nodevas setup-agent`,
// the copilot — gets the full set, exactly as before.
const (
	// ToolsetFull is every tool: the default.
	ToolsetFull = "full"
	// ToolsetRun is what a runner's implement, revise, review, plan and
	// interactive runs need.
	ToolsetRun = "run"
)

// Toolsets lists the valid --toolset values.
var Toolsets = []string{ToolsetFull, ToolsetRun}

// RunTools is ToolsetRun's tools, and why each is there:
//
//   - claim_task, get_node, set_node_status: the implement, revise and
//     interactive prompts name them; review reads the node with get_node.
//   - release_task: a run that cannot finish gives its task back with it;
//     set_node_status refuses anything else and says so.
//   - create_node, link_nodes: a plan run writes its tasks and their
//     requires edges to the board with these.
//   - ask_user: a headless run's one way to reach a person; the adapters'
//     tool timeouts are sized for it.
//   - check_inbox: a late ask_user answer arrives as a message, and every
//     tool result points at check_inbox when mail is waiting.
//   - send_message: runs working on one project tell each other what they
//     changed or ask each other, which the station shows.
var RunTools = []string{
	"claim_task",
	"get_node",
	"set_node_status",
	"release_task",
	"create_node",
	"link_nodes",
	"ask_user",
	"check_inbox",
	"send_message",
}

// ValidToolset reports whether name is a --toolset value; empty is full.
func ValidToolset(name string) bool {
	return name == "" || slices.Contains(Toolsets, name)
}

// toolsetTools is the allowlist for toolset, or nil for every tool.
func toolsetTools(toolset string) ([]string, error) {
	switch toolset {
	case "", ToolsetFull:
		return nil, nil
	case ToolsetRun:
		return RunTools, nil
	}
	return nil, fmt.Errorf("toolset %q is not one this server knows: use %s",
		toolset, strings.Join(Toolsets, " or "))
}

// toolsetGuard serves only allowed: tools/list leaves the rest out, and a
// call to one of them by name is refused with a reason, not the SDK's bare
// "unknown tool". The tools themselves stay registered as they are, so their
// definitions are byte for byte the full set's.
func toolsetGuard(toolset string, allowed []string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" && req != nil {
				if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && !slices.Contains(allowed, params.Name) {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
							"tool %q is not available here: this session serves the %s toolset "+
								"(nodevas mcp --toolset %s), which has only %s",
							params.Name, toolset, toolset, strings.Join(allowed, ", "))}},
					}, nil
				}
			}
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return res, err
			}
			if list, ok := res.(*mcp.ListToolsResult); ok && list != nil {
				list.Tools = slices.DeleteFunc(slices.Clone(list.Tools), func(tool *mcp.Tool) bool {
					return !slices.Contains(allowed, tool.Name)
				})
			}
			return res, nil
		}
	}
}
