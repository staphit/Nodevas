package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PermissionRelayApplies reports whether the runner's permissionRelay setting
// changes a headless run of an agent kind under permission setting mode (""
// is the adapter's default); acp is OpenCode driven over ACP. Only where a
// tool would otherwise be refused for want of someone to ask, and never
// where the owner's mode is itself the limit:
//   - Claude Code, except in plan and dontAsk (never ask by the owner's say);
//   - the Antigravity CLI in accept-edits: its gate asks instead of denying
//     shell commands (plan stays the owner's limit; auto and full have no gate);
//   - OpenCode over ACP in edit: the shell asks instead of being denied.
//
// Codex, Gemini CLI and `opencode run` have no way to relay a prompt.
func PermissionRelayApplies(kind, mode string, acp bool) bool {
	switch kind {
	case "claude":
		return mode != "plan" && mode != "dontAsk"
	case "antigravity":
		return mode == "" || mode == "accept-edits"
	case "opencode":
		return acp && (mode == "" || mode == DefaultOpenCodePermission)
	}
	return false
}

// PermissionAskTimeout is how long the hub waits for a person to answer a
// permission ask before it expires (and the answer is no).
const PermissionAskTimeout = 5 * time.Minute

// PermissionWait is how long the runner's side waits for the hub's answer:
// the hub's own wait and some slack for the long poll.
const PermissionWait = PermissionAskTimeout + 30*time.Second

// maxToolInput caps a permission ask's toolInput, as the hub stores it.
const maxToolInput = 4096

// PermissionRequest is one tool call an agent wants to make.
type PermissionRequest struct {
	// Tool is the tool's name as the CLI reports it ("run_command", "bash").
	Tool string
	// Input is the call's input, JSON or text.
	Input string
}

// PermissionDecision is the answer to a PermissionRequest.
type PermissionDecision struct {
	Allow bool
	// ForRun: the person allowed this tool for the rest of the run.
	ForRun bool
	// Reason says why not, for the agent, when !Allow.
	Reason string
}

// PermissionAsker asks a person whether a tool call may go ahead. It fails
// closed: anything but an approval is a denial.
type PermissionAsker interface {
	AskPermission(ctx context.Context, req PermissionRequest) PermissionDecision
}

// HubAsker asks through the run's door in the runner's proxy, the way the
// run's `nodevas mcp` reaches the hub: POST <Server>/api/agents/asks with
// kind "permission", long-polled until a person answers or the ask expires.
type HubAsker struct {
	// Server is the run's proxy URL, http://127.0.0.1:<port>/r/<secret>.
	Server string
	// Project, AgentRole, Actor and SessionName are the run's `nodevas mcp`
	// --project, --agent-role, --actor and --session-name.
	Project, AgentRole, Actor, SessionName string
	// Agent is the agent kind; NodeID the run's node, when known.
	Agent, NodeID string
	// Client carries the request; nil is a client with no timeout of its own.
	Client *http.Client
	// Wait bounds the whole ask; zero is PermissionWait.
	Wait time.Duration
}

// HubAskerFromMCPArgs reads a run's `nodevas mcp` arguments (RunEnv.MCP.Args,
// or RunMCPEnv in an agent's hook). ok is false without a --server.
func HubAskerFromMCPArgs(args []string) (HubAsker, bool) {
	var h HubAsker
	for i := 0; i+1 < len(args); i++ {
		value := args[i+1]
		switch args[i] {
		case "--server":
			h.Server = value
		case "--project":
			h.Project = value
		case "--agent-role":
			h.AgentRole = value
		case "--actor":
			h.Actor = value
		case "--session-name":
			h.SessionName = value
		default:
			continue
		}
		i++
	}
	h.Agent = strings.TrimPrefix(h.Actor, "run:")
	return h, h.Server != ""
}

func (h HubAsker) AskPermission(ctx context.Context, req PermissionRequest) PermissionDecision {
	wait := h.Wait
	if wait <= 0 {
		wait = PermissionWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	fail := func(format string, args ...any) PermissionDecision {
		return PermissionDecision{Reason: fmt.Sprintf("Could not ask for permission in Nodevas ("+format+"), so it is denied.", args...)}
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "a tool"
	}
	input := Truncate(req.Input, maxToolInput)
	agent := h.Agent
	if agent == "" {
		agent = "The agent"
	}
	question := agent + " wants to run " + tool
	if preview := strings.TrimSpace(Truncate(req.Input, 500)); preview != "" {
		question += ": " + preview
	}
	body, err := json.Marshal(map[string]any{
		"sessionId":      h.SessionName,
		"sessionName":    h.SessionName,
		"actor":          h.Actor,
		"agent":          h.Agent,
		"question":       question,
		"nodeId":         h.NodeID,
		"kind":           "permission",
		"tool":           tool,
		"toolInput":      input,
		"timeoutSeconds": int(PermissionAskTimeout / time.Second),
	})
	if err != nil {
		return fail("%v", err)
	}
	target := strings.TrimRight(h.Server, "/") + "/api/agents/asks"
	if h.Project != "" {
		target += "?" + url.Values{"project": {h.Project}}.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fail("%v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if h.AgentRole != "" {
		httpReq.Header.Set("X-Nodevas-Agent-Role", h.AgentRole)
	}
	client := h.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return PermissionDecision{Reason: "Nobody answered the permission request in Nodevas in time, so it is denied."}
		}
		return fail("%v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fail("%v", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fail("the hub answered %d %s", resp.StatusCode, strings.TrimSpace(e.Error))
	}
	var out struct {
		Ask struct {
			Status    string `json:"status"`
			Decision  string `json:"decision"`
			Answer    string `json:"answer"`
			DecidedBy string `json:"decidedBy"`
			Note      string `json:"note"`
		} `json:"ask"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fail("unreadable answer: %v", err)
	}
	ask := out.Ask
	switch ask.Status {
	case "approved":
		return PermissionDecision{Allow: true, ForRun: ask.Decision == "approve_run" || ask.Answer == "approve_run"}
	case "expired":
		return PermissionDecision{Reason: "Nobody answered the permission request in Nodevas in time, so it is denied. " +
			"Do without it, or call ask_user if you are blocked."}
	}
	reason := "A person denied this in Nodevas"
	if by := strings.TrimSpace(ask.DecidedBy); by != "" {
		reason = by + " denied this in Nodevas"
	}
	if note := strings.TrimSpace(ask.Note); note != "" {
		reason += ": " + note
	}
	return PermissionDecision{Reason: reason + "."}
}
