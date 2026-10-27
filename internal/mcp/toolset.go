package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/engine"
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
//   - create_node, create_subgraph, link_nodes: a plan run writes its tasks,
//     their requires edges and their reference notes to the board with these.
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
	"create_subgraph",
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
	return allowlistGuard(allowed, func(name string) string {
		return fmt.Sprintf("tool %q is not available here: this session serves the %s toolset "+
			"(nodevas mcp --toolset %s), which has only %s",
			name, toolset, toolset, strings.Join(allowed, ", "))
	})
}

// roleGuard is toolsetGuard for a role's allowlist. It shrinks what the
// model is shown and says why a call is refused; the hub enforces the role
// on every request regardless (internal/server/roleguard.go).
func roleGuard(role string, allowed []string) mcp.Middleware {
	return allowlistGuard(allowed, func(name string) string {
		return fmt.Sprintf("tool %q is not available here: role %s serves only %s",
			name, role, strings.Join(allowed, ", "))
	})
}

func allowlistGuard(allowed []string, refusal func(name string) string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" && req != nil {
				if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && !slices.Contains(allowed, params.Name) {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: refusal(params.Name)}},
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

// checkToolNames refuses a --tools list naming a tool this server does not
// have, so a typo fails at start instead of silently serving less.
func checkToolNames(tools []string) error {
	for _, name := range tools {
		if len(engine.ToolCapabilities[name]) == 0 {
			return fmt.Errorf("--tools names unknown tool %q", name)
		}
	}
	return nil
}

// roleToolset resolves the allowlist for --role/--tools: explicit tools win;
// a built-in role serves its default tools; a custom role (whose tools only
// the project knows) needs g. Nil means no role restriction.
func roleToolset(role string, tools []string, g *engine.Graph) ([]string, error) {
	if len(tools) > 0 {
		if err := checkToolNames(tools); err != nil {
			return nil, err
		}
		return slices.Clone(tools), nil
	}
	if role == "" {
		return nil, nil
	}
	if !engine.ValidRoleID(role) {
		return nil, fmt.Errorf("--role %q is not a role: use planner, qa, tester, executor, reviewer, researcher or custom-role-<id>", role)
	}
	resolved, ok := g.RoleOf(role)
	if !ok {
		return nil, fmt.Errorf("--role %q is not defined in this project", role)
	}
	return slices.Clone(resolved.Tools), nil
}

// compactToolList leaves each tool's output schema out of tools/list. A
// model pays for every listed definition on every call, and the output
// schemas were the larger half of the catalog; the results themselves are
// unchanged (the SDK still sends the structured output, and its JSON as
// text). The registered tools are copied, not edited, so the server's own
// validation keeps its schemas.
func compactToolList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil || method != "tools/list" {
			return res, err
		}
		if list, ok := res.(*mcp.ListToolsResult); ok && list != nil {
			compact := make([]*mcp.Tool, 0, len(list.Tools))
			for _, tool := range list.Tools {
				if tool == nil {
					continue
				}
				copied := *tool
				copied.OutputSchema = nil
				compact = append(compact, &copied)
			}
			list.Tools = compact
		}
		return res, nil
	}
}
