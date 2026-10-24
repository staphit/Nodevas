package adapter

import (
	"bytes"
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
// commands, MCP servers other than nodevas, the browser, and files outside
// the workspace. Anything else gets "ask", the hook's way of having no
// opinion (an empty decision is a denial), which leaves it to agy's own
// permissions: accept-edits for the worktree, the owner's allow rules.
//
// agy runs a hook command through `cmd /c` on Windows and `sh -c` elsewhere,
// and on Windows it mangles a quoted program path; antigravityHookPath (per
// OS) gives a path that needs no quotes, or "" when there is none, and then
// the run goes without the gate.

// AntigravityGateArgs is the gate's command line after the nodevas binary.
const AntigravityGateArgs = "runner agy-gate"

// antigravityGateMatcher is the PreToolUse matcher: every tool the gate may
// deny. Other tools never reach the hook.
const antigravityGateMatcher = "run_command|send_command_input|call_mcp_tool|browser_.*|open_browser_url|" +
	"execute_browser_javascript|click_browser_pixel|capture_browser_.*|read_browser_page|list_browser_pages|" +
	antigravityFileTools

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
// nil when the nodevas binary's path cannot go in a hook command.
func antigravityGateHooks(exe string) map[string]any {
	path := antigravityHookPath(exe)
	if path == "" {
		return nil
	}
	return map[string]any{
		"nodevas-run": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": antigravityGateMatcher,
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": path + " " + AntigravityGateArgs,
					"timeout": 10,
				}},
			}},
		},
	}
}

// antigravityNoOpinion is the gate's answer when it has none.
const antigravityNoOpinion = `{"decision":"ask"}`

// AntigravityGate answers one PreToolUse payload ({"toolCall": {"name": …,
// "args": {…}}, "workspacePaths": […], …}) with agy's hook output: a denial
// with a reason, or "ask" for no opinion.
func AntigravityGate(payload []byte) string {
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
		reason = "Shell commands are not available in this Nodevas run: nobody can approve them here. " +
			"Do the work with the file tools; the runner commits your changes when you finish."
	case name == "call_mcp_tool":
		server, _ := in.ToolCall.Args["ServerName"].(string)
		if strings.TrimSpace(server) == "nodevas" {
			return antigravityNoOpinion
		}
		reason = "Only the nodevas MCP server is available in this Nodevas run."
	case strings.Contains(name, "browser"):
		reason = "The browser is not available in this Nodevas run."
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
