package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Antigravity's gate. Headless, agy denies a tool that needs permission
// without asking, and a denial ends the whole turn: the run stops half done
// and reports success. A PreToolUse hook that says "deny" instead hands the
// agent an error it can work around (checked against agy 1.4.7), so in the
// modes that cannot run commands (plan, accept-edits) the run's
// customization root gets a hook, `nodevas runner agy-gate`, that turns the
// tools headless mode would refuse into denials with a reason: shell
// commands, MCP servers other than nodevas, the browser, the web, other
// agents and messages (antigravityDeniedTools), and files outside the
// workspace. Anything else gets "ask", the hook's way of having no
// opinion (an empty decision is a denial), which leaves it to agy's own
// permissions: accept-edits for the worktree, the owner's allow rules.
//
// agy runs a hook command through `cmd /c` on Windows and `sh -c` elsewhere,
// and on Windows it mangles a quoted program path; antigravityHookPath (per
// OS) gives a path that needs no quotes, or "" when there is none, and then
// the run goes without the gate.

// AntigravityGateArgs is the gate's command line after the nodevas binary.
const AntigravityGateArgs = "runner agy-gate"

// AntigravityGateReadOnly is the gate's flag for a read-only run (a review or
// a plan): it also denies the tools that write files. agy's own plan mode is
// not enough there: headless, agy 1.4.7 says "--mode plan has no effect
// while slash command expansion is disabled" and edits anyway.
const AntigravityGateReadOnly = "--read-only"

// AntigravityGateRelayFlag is the gate's flag for a run with permissionRelay:
// a shell command it would refuse for want of someone to approve it is asked
// about in Nodevas instead (AntigravityGateRelay).
const AntigravityGateRelayFlag = "--relay"

// The hook's timeout in seconds. With the relay it outlasts the hub's wait
// for a person (PermissionWait), so the gate's own answer, a denial with a
// reason when nobody answers, is what agy gets.
const (
	antigravityGateTimeout      = 10
	antigravityRelayGateTimeout = 360
)

// antigravityGateMatcher is the PreToolUse matcher: every tool the gate may
// deny. Other tools never reach the hook.
const antigravityGateMatcher = "run_command|send_command_input|call_mcp_tool|read_resource|list_resources|" +
	"browser_.*|open_browser_url|execute_browser_javascript|click_browser_pixel|capture_browser_.*|" +
	"read_browser_page|list_browser_pages|" + antigravityDeniedTools + "|" + antigravityFileTools

// antigravityDeniedTools reach beyond the worktree (the web, other agents,
// messages, schedules, plugins, agy's knowledge store): never in a gated run.
const antigravityDeniedTools = "read_url_content|search_web|notebook_execution|schedule|run_workflow|" +
	"define_subagent|invoke_subagent|manage_subagents|send_message|manage_inbox|manage_task|generate_image|" +
	"search_marketplace|list_plugin_accounts|delete_knowledge"

// deniedTool is antigravityDeniedTools as a set.
var deniedTool = func() map[string]bool {
	set := map[string]bool{}
	for _, name := range strings.Split(antigravityDeniedTools, "|") {
		set[name] = true
	}
	return set
}()

// antigravityWriteTools change files: denied in a read-only run.
var antigravityWriteTools = map[string]bool{
	"write_file": true, "write_to_file": true, "replace_file_content": true,
	"multi_replace_file_content": true, "sed_file": true, "notebook_edit": true,
}

// antigravityFileTools are the tools that take a path. agy's stream and its
// hooks do not always name a tool alike (read_file / view_file), so both.
const antigravityFileTools = "read_file|view_file|list_dir|find_by_name|grep_search|write_file|write_to_file|" +
	"replace_file_content|multi_replace_file_content|sed_file|notebook_edit"

// antigravityGated reports whether mode gets the gate.
func antigravityGated(mode string) bool {
	if mode == "" {
		mode = DefaultAntigravityMode
	}
	return mode == "plan" || mode == "accept-edits"
}

// antigravityGateHooks is the hooks.json of a run's customization root, or
// nil when the nodevas binary's path cannot go in a hook command. relay is
// ignored for a read-only run, which never asks.
func antigravityGateHooks(exe string, readOnly, relay bool) map[string]any {
	path := antigravityHookPath(exe)
	if path == "" {
		return nil
	}
	command := path + " " + AntigravityGateArgs
	timeout := antigravityGateTimeout
	if readOnly {
		command += " " + AntigravityGateReadOnly
	} else if relay {
		command += " " + AntigravityGateRelayFlag
		timeout = antigravityRelayGateTimeout
	}
	return map[string]any{
		"nodevas-run": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": antigravityGateMatcher,
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": command,
					"timeout": timeout,
				}},
			}},
		},
	}
}

// antigravityNoOpinion is the gate's answer when it has none.
const antigravityNoOpinion = `{"decision":"ask"}`

// antigravityAllow is the gate's answer for a call a person allowed.
const antigravityAllow = `{"decision":"allow"}`

// AntigravityGateMain is `nodevas runner agy-gate [--read-only] [--relay]`
// on one payload. With --relay it asks through the run's door in the
// runner's proxy, read from RunMCPEnv (the run's `nodevas mcp` arguments,
// which agy hands its hooks in their environment); without them it is the
// plain gate.
func AntigravityGateMain(args []string, payload []byte) string {
	readOnly, relay := false, false
	for _, arg := range args {
		switch arg {
		case AntigravityGateReadOnly:
			readOnly = true
		case AntigravityGateRelayFlag:
			relay = true
		}
	}
	var asker PermissionAsker
	if relay && !readOnly {
		var mcpArgs []string
		if json.Unmarshal([]byte(os.Getenv(RunMCPEnv)), &mcpArgs) == nil {
			if hub, ok := HubAskerFromMCPArgs(mcpArgs); ok {
				hub.Agent = "antigravity"
				asker = hub
			}
		}
	}
	return AntigravityGateRelay(context.Background(), payload, readOnly, asker)
}

// AntigravityGate answers one PreToolUse payload ({"toolCall": {"name": …,
// "args": {…}}, "workspacePaths": […], …}) with agy's hook output: a denial
// with a reason, or "ask" for no opinion. readOnly also denies file writes.
func AntigravityGate(payload []byte, readOnly bool) string {
	return AntigravityGateRelay(context.Background(), payload, readOnly, nil)
}

// AntigravityGateRelay is AntigravityGate for a run with permissionRelay: a
// shell command, which the plain gate refuses because nobody can approve it,
// is asked about through asker and allowed or denied with the person's
// reason. Every other denial comes first and is never asked about, and a
// read-only run never asks. A nil asker is the plain gate.
func AntigravityGateRelay(ctx context.Context, payload []byte, readOnly bool, asker PermissionAsker) string {
	var in struct {
		ToolCall struct {
			Name string         `json:"name"`
			Args map[string]any `json:"args"`
		} `json:"toolCall"`
		WorkspacePaths []string `json:"workspacePaths"`
	}
	payload = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(payload), utf8BOM))
	if json.Unmarshal(payload, &in) != nil {
		return antigravityNoOpinion
	}
	name := strings.ToLower(strings.TrimSpace(in.ToolCall.Name))
	reason := ""
	switch {
	case name == "run_command" || name == "send_command_input":
		if asker != nil && !readOnly {
			input, _ := json.Marshal(in.ToolCall.Args)
			decision := asker.AskPermission(ctx, PermissionRequest{Tool: name, Input: string(input)})
			if decision.Allow {
				return antigravityAllow
			}
			reason = decision.Reason
			if reason == "" {
				reason = "The command was not allowed in Nodevas."
			}
			break
		}
		reason = "Shell commands are not available in this Nodevas run: nobody can approve them here. " +
			"Do the work with the file tools; the runner commits your changes when you finish."
	case name == "call_mcp_tool" || name == "read_resource" || name == "list_resources":
		if mcpServerArg(in.ToolCall.Args) == "nodevas" {
			return antigravityNoOpinion
		}
		reason = "Only the nodevas MCP server is available in this Nodevas run."
	case strings.Contains(name, "browser"):
		reason = "The browser is not available in this Nodevas run."
	case readOnly && antigravityWriteTools[name]:
		reason = "This Nodevas run is read-only (a review or a plan): it may not change files. " +
			"Report what you found instead."
	case deniedTool[name]:
		reason = "The " + name + " tool is not available in this Nodevas run: it works only with the files " +
			"in its worktree and the nodevas MCP server."
	default:
		if outside := outsidePath(in.ToolCall.Args, in.WorkspacePaths); outside != "" {
			reason = "This Nodevas run may only use files inside its worktree (the current directory); " +
				outside + " is outside it."
			break
		}
		return antigravityNoOpinion
	}
	out, err := json.Marshal(map[string]string{"decision": "deny", "reason": reason})
	if err != nil {
		return antigravityNoOpinion
	}
	return string(out)
}

// mcpServerArg is the MCP server a tool call names, lowercased and trimmed:
// agy calls the argument ServerName; any spelling of it counts.
func mcpServerArg(args map[string]any) string {
	for key, v := range args {
		k := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if k == "servername" || k == "server" {
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	return ""
}

// outsidePath is the first absolute path among a file tool's arguments that
// lies outside every workspace path and outside agy's own ~/.gemini (where it
// keeps the MCP tools' schemas and the conversation's artifacts), or "".
// With no workspace paths to compare with, nothing is outside.
func outsidePath(args map[string]any, workspaces []string) string {
	if len(workspaces) == 0 {
		return ""
	}
	roots := append([]string(nil), workspaces...)
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".gemini"))
	}
	for key, v := range args {
		path, ok := v.(string)
		k := strings.ToLower(key)
		if !ok || !(strings.Contains(k, "path") || strings.Contains(k, "file") || strings.Contains(k, "dir")) {
			continue
		}
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(filepath.FromSlash(path)) {
			continue
		}
		inside := false
		for _, root := range roots {
			if within(root, path) {
				inside = true
				break
			}
		}
		if !inside {
			return path
		}
	}
	return ""
}

// within reports whether path is root or below it; on Windows, ignoring case.
func within(root, path string) bool {
	root = filepath.Clean(filepath.FromSlash(strings.TrimSpace(root)))
	path = filepath.Clean(filepath.FromSlash(path))
	if root == "." || root == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
