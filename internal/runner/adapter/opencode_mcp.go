package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// there, plugins included. A read-only run (readOnly) does not trust that
// config, so it is asked about an empty directory instead, which only the
// owner's config reaches; the runner has removed the project config from the
// worktree as well, and it is switched off here as it is for the run.
//
// OpenCode 2 answers through the owner's background service, which keeps its
// own environment (so the variable does nothing there, and the service runs a
// project's plugins when asked about its directory), and prints the config's
// sources rather than the merged config: [{"type": …, "path": …, "info":
// {"mcp": {"servers": {…}}}}] (checked against opencode 2.1.4).
func OpenCodeMCPServers(ctx context.Context, bin, dir string, readOnly bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, openCodeConfigTimeout)
	defer cancel()
	if readOnly {
		// One directory for every run: OpenCode 2 records each directory it
		// is asked about as a project of the owner's.
		empty := filepath.Join(os.TempDir(), "nodevas-opencode-empty")
		if err := os.MkdirAll(empty, 0o700); err != nil {
			return nil, err
		}
		dir = empty
	}
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
	names, err := openCodeServerNames([]byte(stdout.String()))
	if err != nil {
		return nil, fmt.Errorf("opencode debug config: %w", err)
	}
	return names, nil
}

// openCodeServerNames reads `opencode debug config`'s output, either shape.
func openCodeServerNames(out []byte) ([]string, error) {
	type mcpConfig struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	seen := map[string]bool{}
	add := func(mcp map[string]json.RawMessage) {
		for name := range mcp {
			seen[name] = true
		}
	}
	out = bytes.TrimSpace(bytes.TrimPrefix(out, utf8BOM))
	if len(out) > 0 && out[0] == '[' {
		var sources []struct {
			Info mcpConfig `json:"info"`
		}
		if err := json.Unmarshal(out, &sources); err != nil {
			return nil, err
		}
		for _, source := range sources {
			// 2.x normalizes each source to mcp.servers.
			var servers map[string]json.RawMessage
			if raw, ok := source.Info.MCP["servers"]; ok && json.Unmarshal(raw, &servers) == nil {
				add(servers)
				continue
			}
			add(source.Info.MCP)
		}
	} else {
		var config mcpConfig
		if err := json.Unmarshal(out, &config); err != nil {
			return nil, err
		}
		add(config.MCP)
	}
	var names []string
	for name := range seen {
		if name != "nodevas" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}
