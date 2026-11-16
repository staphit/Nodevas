package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// agentNamed is kind's row in agents.
func agentNamed(t *testing.T, agents []runnerwire.Agent, kind string) runnerwire.Agent {
	t.Helper()
	for _, agent := range agents {
		if agent.Kind == kind {
			return agent
		}
	}
	t.Fatalf("no %s in %+v", kind, agents)
	return runnerwire.Agent{}
}

// A program the owner added is found though nothing is on PATH, and asked
// for its version where it is; the hello never says where that is.
func TestDetectUsesAddedAgentPath(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	noAgentTempDir(t)
	added := writeAgentFile(t, filepath.Join(t.TempDir(), "tools"), "claude")
	cmds := &fakeCommands{paths: map[string]string{}, outputs: map[string]string{added + " --version": "2.1.0 (Claude Code)\n"}}
	cfg := &Config{AgentPaths: map[string]string{"claude": added}}
	info := BuildInfo(context.Background(), cmds, cfg)
	claude := agentNamed(t, info.Agents, "claude")
	if !claude.Installed || claude.Version != "2.1.0 (Claude Code)" || claude.Error != "" {
		t.Fatalf("claude = %+v", claude)
	}
	if cmds.ran(added+" --version") != 1 {
		t.Fatalf("calls %q", cmds.calls)
	}
	if codex := agentNamed(t, info.Agents, "codex"); codex.Installed || codex.Error != "" {
		t.Fatalf("codex, never added and not on PATH: %+v", codex)
	}
	raw, err := json.Marshal(info.Agents)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{added, filepath.Dir(added), filepath.ToSlash(filepath.Dir(added))} {
		if strings.Contains(string(raw), leak) || strings.Contains(string(raw), strings.ReplaceAll(leak, `\`, `\\`)) {
			t.Fatalf("the hello's agents name %q: %s", leak, raw)
		}
	}

	// It fails --version: the row says so, without the path.
	cmds.errs = map[string]error{added + " --version": &os.PathError{Op: "fork/exec", Path: added, Err: os.ErrPermission}}
	claude = agentNamed(t, Detect(context.Background(), cmds, cfg), "claude")
	if claude.Installed || claude.Error == "" || strings.Contains(claude.Error, filepath.Dir(added)) {
		t.Fatalf("failing claude = %+v", claude)
	}
}

// An added program that is gone, with none on PATH, is said to be gone in
// the words the app knows; one on PATH takes its place.
func TestDetectAddedAgentPathGone(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	gone := filepath.Join(t.TempDir(), agentFileName("claude"))
	cfg := &Config{AgentPaths: map[string]string{"claude": gone}}
	claude := agentNamed(t, Detect(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg), "claude")
	if claude.Installed || claude.Error != "claude: the program added for it is no longer there" {
		t.Fatalf("gone: %+v", claude)
	}
	onPath := &fakeCommands{paths: map[string]string{"claude": "/bin/claude"},
		outputs: map[string]string{"/bin/claude --version": "2.1.0 (Claude Code)\n"}}
	if claude := agentNamed(t, Detect(context.Background(), onPath, cfg), "claude"); !claude.Installed || claude.Error != "" {
		t.Fatalf("gone, on PATH: %+v", claude)
	}
}
