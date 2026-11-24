package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Every agent whose adapter implements adapter.Interactive reports
// interactive: true in the hello.
func TestHelloReportsAgentInteractive(t *testing.T) {
	origAgy := antigravitySettingsPath
	t.Cleanup(func() { antigravitySettingsPath = origAgy })
	agyFile := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(agyFile, []byte(`{"permissions":{"allow":["mcp(nodevas/*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	antigravitySettingsPath = func() (string, error) { return agyFile, nil }

	cmds := &fakeCommands{
		paths: map[string]string{
			"claude":   "/bin/claude",
			"codex":    "/bin/codex",
			"gemini":   "/bin/gemini",
			"opencode": "/bin/opencode",
			"agy":      "/bin/agy",
		},
		outputs: map[string]string{
			"/bin/claude --version":   "2.1.329 (Claude Code)\n",
			"/bin/codex --version":    "codex-cli 0.149.1\n",
			"/bin/gemini --version":   "0.1.0\n",
			"/bin/opencode --version": "1.18.20\n",
			"/bin/agy --version":      "1.5.13\n",
		},
	}
	info := BuildInfo(context.Background(), cmds, &Config{})
	for _, kind := range Kinds {
		agent := agentNamed(t, info.Agents, kind)
		if !agent.Interactive {
			t.Errorf("%s: Interactive = false, want true", kind)
		}
	}
	raw, err := json.Marshal(info.Agents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"interactive":true`) {
		t.Fatalf("hello agents JSON missing interactive flag: %s", raw)
	}
}

// agents[].untrusted reflects whether each installed CLI would show a folder
// trust prompt for a new git worktree of the configured repo(s), and detector
// notices trust changes on its re-check tick.
func TestHelloReportsUntrusted(t *testing.T) {
	origClaude := claudeConfigPath
	origCodex := codexConfigPath
	origAgy := antigravitySettingsPath
	t.Cleanup(func() {
		claudeConfigPath = origClaude
		codexConfigPath = origCodex
		antigravitySettingsPath = origAgy
	})

	dir := t.TempDir()
	repo1 := filepath.Join(dir, "repo1")
	repo2 := filepath.Join(dir, "repo2")
	if err := os.MkdirAll(repo1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo2, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeFile := filepath.Join(dir, "claude.json")
	codexFile := filepath.Join(dir, "codex.toml")
	agyFile := filepath.Join(dir, "agy-settings.json")
	if err := os.WriteFile(agyFile, []byte(`{"permissions":{"allow":["mcp(nodevas/*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	claudeConfigPath = func() (string, error) { return claudeFile, nil }
	codexConfigPath = func() (string, error) { return codexFile, nil }
	antigravitySettingsPath = func() (string, error) { return agyFile, nil }

	cmds := &fakeCommands{
		paths: map[string]string{
			"claude":   "/bin/claude",
			"codex":    "/bin/codex",
			"gemini":   "/bin/gemini",
			"opencode": "/bin/opencode",
			"agy":      "/bin/agy",
		},
		outputs: map[string]string{
			"/bin/claude --version":   "2.1.329 (Claude Code)\n",
			"/bin/codex --version":    "codex-cli 0.149.1\n",
			"/bin/gemini --version":   "0.1.0\n",
			"/bin/opencode --version": "1.18.20\n",
			"/bin/agy --version":      "1.5.13\n",
		},
	}

	// No trust files on disk: claude, codex, and antigravity are untrusted;
	// opencode (no trust screen) and gemini (no probe) are not.
	cfg := &Config{Repos: []runnerwire.Repo{
		{ID: "r1", Name: "repo1", Path: repo1},
		{ID: "r2", Name: "repo2", Path: repo2},
	}}
	info := BuildInfo(context.Background(), cmds, cfg)
	if a := agentNamed(t, info.Agents, "claude"); !a.Untrusted {
		t.Errorf("claude without .claude.json: Untrusted = false, want true")
	}
	if a := agentNamed(t, info.Agents, "codex"); !a.Untrusted {
		t.Errorf("codex without config.toml: Untrusted = false, want true")
	}
	if a := agentNamed(t, info.Agents, "antigravity"); !a.Untrusted {
		t.Errorf("antigravity: Untrusted = false, want true")
	}
	if a := agentNamed(t, info.Agents, "opencode"); a.Untrusted {
		t.Errorf("opencode: Untrusted = true, want false")
	}
	if a := agentNamed(t, info.Agents, "gemini"); a.Untrusted {
		t.Errorf("gemini: Untrusted = true, want false")
	}

	// Trust only repo1: still untrusted because repo2 is untrusted.
	writeClaude := func(paths ...string) {
		projects := map[string]map[string]any{}
		for _, p := range paths {
			projects[p] = map[string]any{"hasTrustDialogAccepted": true}
		}
		raw, err := json.Marshal(map[string]any{"projects": projects})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(claudeFile, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCodex := func(content string) {
		if err := os.WriteFile(codexFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeClaude(repo1)
	writeCodex(fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", repo1))
	info = BuildInfo(context.Background(), cmds, cfg)
	if a := agentNamed(t, info.Agents, "claude"); !a.Untrusted {
		t.Errorf("claude with 1/2 repos trusted: Untrusted = false, want true")
	}
	if a := agentNamed(t, info.Agents, "codex"); !a.Untrusted {
		t.Errorf("codex with 1/2 repos trusted: Untrusted = false, want true")
	}

	// Trust both repos (including a single-quoted literal key for codex).
	writeClaude(repo1, repo2)
	writeCodex(fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\" # accepted\n\n[projects.'%s']\ntrust_level = 'trusted'\n", repo1, repo2))
	info = BuildInfo(context.Background(), cmds, cfg)
	if a := agentNamed(t, info.Agents, "claude"); a.Untrusted {
		t.Errorf("claude with all repos trusted: Untrusted = true, want false")
	}
	if a := agentNamed(t, info.Agents, "codex"); a.Untrusted {
		t.Errorf("codex with all repos trusted: Untrusted = true, want false")
	}
	// Antigravity requires an exact worktree path so new worktrees stay untrusted.
	if a := agentNamed(t, info.Agents, "antigravity"); !a.Untrusted {
		t.Errorf("antigravity: Untrusted = false, want true")
	}

	// No configured repos: claude and codex are not marked untrusted.
	_ = os.Remove(claudeFile)
	_ = os.Remove(codexFile)
	info = BuildInfo(context.Background(), cmds, &Config{})
	if a := agentNamed(t, info.Agents, "claude"); a.Untrusted {
		t.Errorf("claude with no repos: Untrusted = true, want false")
	}
	if a := agentNamed(t, info.Agents, "codex"); a.Untrusted {
		t.Errorf("codex with no repos: Untrusted = true, want false")
	}

	// Live detector re-check: starting untrusted, then trusting the repo on disk
	// triggers a fresh hello with Untrusted = false on the recheck cadence.
	env := startJobRunner(t, func(c *Client) {
		c.RedetectInterval = time.Hour
		c.signInRecheck = 15 * time.Millisecond
	})
	first := env.hub.hello()
	if claude := agentNamed(t, first.Hello.Agents, "claude"); !claude.Untrusted {
		t.Fatalf("initial hello claude = %+v, want Untrusted=true", claude)
	}
	writeClaude(env.client.cfg().Repos[0].Path)
	updated := env.hub.hello()
	if claude := agentNamed(t, updated.Hello.Agents, "claude"); claude.Untrusted {
		t.Fatalf("updated hello claude = %+v, want Untrusted=false", claude)
	}
}
