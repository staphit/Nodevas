package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"

	"nodevas/internal/runnerwire"
)

// OpenCodeACP runs OpenCode as an ACP agent (acp.go) instead of through
// `opencode run`:
//
//	opencode acp --cwd <worktree>
//
// with the same OPENCODE_CONFIG_CONTENT as OpenCode (the nodevas MCP server,
// the permission rules for the owner's setting, the other MCP servers off)
// plus the job's model, and the prompt sent as the session's one turn.
// The nodevas MCP server stays in the config rather than in session/new,
// because only the config carries its timeout (ask_user waits for minutes).
// Checked against opencode 1.18.23 (testdata/opencode_acp_*.jsonl).
//
// A permission the rules leave at ask comes to the runner, which refuses it,
// and the run goes on; headless `opencode run` ends the run there instead.
// With permissionRelay (in edit, and not in a read-only run) the shell is
// left at ask and each request is asked about in Nodevas instead.
// The runner takes this adapter for OpenCode runs when its opencodeProtocol
// setting is acp.
type OpenCodeACP struct{}

// OpenCodeProtocols are the runner's opencodeProtocol settings: run, the
// `opencode run --format json` adapter (the default), or acp.
var OpenCodeProtocols = []string{"run", "acp"}

func (OpenCodeACP) Kind() string { return "opencode" }

// NewParser is for a stream read without talking back, which an ACP agent
// never gives; the runner uses NewConversation.
func (OpenCodeACP) NewParser() Parser { return newACPSession("opencode", "", "", nil) }

func (OpenCodeACP) NewConversation(job runnerwire.Job, env RunEnv) Conversation {
	s := newACPSession("opencode", env.Workdir, Prompt(job, env.Branch), nil)
	if openCodeACPRelay(job, env) {
		s.asker = env.Permissions
		if s.asker == nil {
			if hub, ok := HubAskerFromMCPArgs(env.MCP.Args); ok {
				hub.Agent, hub.NodeID = "opencode", job.NodeID
				s.asker = hub
			}
		}
	}
	return s
}

// openCodeACPRelay reports whether a run relays its permission requests:
// permissionRelay on, the owner's edit setting, and a run that may write.
func openCodeACPRelay(job runnerwire.Job, env RunEnv) bool {
	return env.PermissionRelay && !ReadOnly(job) && PermissionRelayApplies("opencode", env.PermissionMode, true)
}

func (OpenCodeACP) Classify(text string) string { return classify(openCodeFailures, text) }

// InteractiveCommand is OpenCode's TUI: an interactive run does not go
// through ACP.
func (OpenCodeACP) InteractiveCommand(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	return OpenCode{}.InteractiveCommand(job, env)
}

func (OpenCodeACP) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" {
		return nil, fmt.Errorf("opencode: binary and workdir are required")
	}
	config, err := openCodeConfig(env, false, ReadOnly(job))
	if err != nil {
		return nil, err
	}
	if openCodeACPRelay(job, env) {
		// The shell asks, and the runner relays the question; the web and
		// paths outside the worktree stay denied.
		if config, err = withShellAsk(config); err != nil {
			return nil, err
		}
	}
	if job.Model != "" && job.Model != "default" {
		// `opencode run -m`'s equivalent: the session starts on this model.
		if config, err = withConfigKey(config, "model", job.Model); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(env.Binary, "acp", "--cwd", env.Workdir)
	cmd.Dir = env.Workdir
	cmd.Env = jobEnv(job, openCodeEnv(job, env, config)...)
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}
	return cmd, nil
}

// withShellAsk turns the config's denied shell (bash, and OpenCode 2's
// execute) into ask.
func withShellAsk(config string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(config), &m); err != nil {
		return "", err
	}
	permission, _ := m["permission"].(map[string]any)
	for _, tool := range []string{"bash", "execute"} {
		if permission[tool] == "deny" {
			permission[tool] = "ask"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// withConfigKey sets one top-level key of a JSON config object.
func withConfigKey(config, key string, value any) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(config), &m); err != nil {
		return "", err
	}
	m[key] = value
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
