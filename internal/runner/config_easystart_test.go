package runner

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// absAgent is an absolute path to a file named like kind's program.
func absAgent(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func TestCloneDeepCopiesAgentPaths(t *testing.T) {
	cfg := &Config{AgentPaths: map[string]string{"claude": "/opt/claude"}}
	clone := cfg.Clone()
	clone.AgentPaths["claude"] = "/elsewhere/claude"
	clone.AgentPaths["codex"] = "/opt/codex"
	if cfg.AgentPaths["claude"] != "/opt/claude" || len(cfg.AgentPaths) != 1 {
		t.Fatalf("original changed through the clone: %v", cfg.AgentPaths)
	}
	if (&Config{}).Clone().AgentPaths != nil {
		t.Fatal("a nil map cloned to non-nil")
	}
}

func TestSetAgentPathStoresAndClears(t *testing.T) {
	path := absAgent(t, "claude")
	cfg := &Config{}
	if err := cfg.Set("agentPath", []string{"claude", path}); err != nil {
		t.Fatal(err)
	}
	if cfg.AgentPaths["claude"] != path {
		t.Fatalf("agentPaths = %v", cfg.AgentPaths)
	}
	if err := cfg.Set("agentPath", []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if cfg.AgentPaths != nil {
		t.Fatalf("[claude] did not clear: %v", cfg.AgentPaths)
	}
	if err := cfg.Set("agentpath", []string{"claude", path}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("agentPath", []string{"claude", "default"}); err != nil {
		t.Fatal(err)
	}
	if cfg.AgentPaths != nil {
		t.Fatalf("[claude default] did not clear: %v", cfg.AgentPaths)
	}
	// Clearing one kind keeps the others.
	cfg.AgentPaths = map[string]string{"claude": path, "codex": absAgent(t, "codex")}
	if err := cfg.Set("agentPath", []string{"claude"}); err != nil || len(cfg.AgentPaths) != 1 || cfg.AgentPaths["codex"] == "" {
		t.Fatalf("agentPaths = %v, %v", cfg.AgentPaths, err)
	}
	// Refusals change nothing.
	before := cfg.AgentPaths["codex"]
	for _, values := range [][]string{{"claude", "relative/claude"}, {"robot", path}, {"claude", path, "extra"},
		{"claude", filepath.Join(filepath.Dir(path), "codex")}} {
		if err := cfg.Set("agentPath", values); err == nil {
			t.Fatalf("Set(agentPath, %q) accepted", values)
		}
	}
	if len(cfg.AgentPaths) != 1 || cfg.AgentPaths["codex"] != before {
		t.Fatalf("refused Set changed agentPaths: %v", cfg.AgentPaths)
	}
	// "default" alone resets the setting, as for every other key.
	if err := cfg.Set("agentPath", []string{"default"}); err != nil || cfg.AgentPaths != nil {
		t.Fatalf("reset = %v, %v", cfg.AgentPaths, err)
	}
}

func TestCheckAgentPathShape(t *testing.T) {
	dir := t.TempDir()
	good := []struct{ kind, name string }{
		{"claude", "claude"}, {"claude", "claude.exe"}, {"codex", "codex.cmd"}, {"gemini", "gemini.bat"},
		{"opencode", "opencode"}, {"antigravity", "agy"}, {"antigravity", "agy.exe"},
	}
	for _, c := range good {
		if err := checkAgentPathShape(c.kind, filepath.Join(dir, c.name)); err != nil {
			t.Errorf("%s %s: %v", c.kind, c.name, err)
		}
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if err := checkAgentPathShape("claude", filepath.Join(dir, "Claude.EXE")); err != nil {
			t.Errorf("case-insensitive name refused: %v", err)
		}
	}
	bad := map[string]struct{ kind, path string }{
		"relative":      {"claude", filepath.Join("bin", "claude")},
		"nul":           {"claude", filepath.Join(dir, "a\x00b", "claude")},
		"too long":      {"claude", filepath.Join(dir, strings.Repeat("a", runnerwire.MaxAgentPathBytes), "claude")},
		"unknown kind":  {"robot", filepath.Join(dir, "robot")},
		"name mismatch": {"claude", filepath.Join(dir, "codex")},
		"near name":     {"claude", filepath.Join(dir, "claude-old")},
		"other ext":     {"claude", filepath.Join(dir, "claude.ps1")},
		"agy kind name": {"antigravity", filepath.Join(dir, "antigravity")},
		"empty":         {"claude", ""},
	}
	for name, c := range bad {
		if err := checkAgentPathShape(c.kind, c.path); err == nil {
			t.Errorf("%s: %q accepted", name, c.path)
		}
	}
}

func TestRunPreviewOn(t *testing.T) {
	off, on := false, true
	if !(&Config{}).RunPreviewOn() || (&Config{RunPreview: &off}).RunPreviewOn() || !(&Config{RunPreview: &on}).RunPreviewOn() {
		t.Fatal("RunPreviewOn: nil and true are on, false is off")
	}
	cfg := &Config{}
	if err := cfg.Set("runPreview", []string{"off"}); err != nil || cfg.RunPreviewOn() {
		t.Fatalf("set off = %v, %v", cfg.RunPreview, err)
	}
	clone := cfg.Clone()
	*clone.RunPreview = true
	if cfg.RunPreviewOn() {
		t.Fatal("Clone shares RunPreview")
	}
	if err := cfg.Set("runPreview", []string{"default"}); err != nil || cfg.RunPreview != nil {
		t.Fatalf("reset = %v, %v", cfg.RunPreview, err)
	}
}
