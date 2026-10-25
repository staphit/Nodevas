package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// openCodeConfigTimeout bounds `opencode debug config`.
const openCodeConfigTimeout = 30 * time.Second

// OpenCodeMCPServers is every MCP server OpenCode would load in dir but the
// run's own: the owner's, and any a project config in the worktree adds (a
// repository's opencode.json can name a local command as one). A run turns
// them all off (RunEnv.DisableMCP); inline config is deep-merged over every
// other, so `"enabled": false` wins (checked against opencode 1.18.23).
//
// It runs `opencode debug config` in dir, which loads the project config
// there, plugins included. For a read-only run (readOnly) the runner has
// removed that config from the worktree first, and the project config is
// switched off here as it is for the run.
func OpenCodeMCPServers(ctx context.Context, bin, dir string, readOnly bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, openCodeConfigTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "debug", "config")
	cmd.Dir = dir
	cmd.Env = append(childEnv(), "PWD="+dir)
	if readOnly {
		cmd.Env = append(cmd.Env, openCodeNoProjectConfig)
	}
	if err := wrapBatch(cmd); err != nil {
		return nil, err
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode debug config: %v: %s", err, Truncate(strings.TrimSpace(stderr.String()), 300))
	}
	var config struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &config); err != nil {
		return nil, fmt.Errorf("opencode debug config: %w", err)
	}
	var names []string
	for name := range config.MCP {
		if name != "nodevas" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}
