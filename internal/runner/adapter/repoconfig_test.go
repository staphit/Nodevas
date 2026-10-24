package adapter

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestReadOnly(t *testing.T) {
	for kind, want := range map[string]bool{runnerwire.JobReview: true, runnerwire.JobPlan: true,
		runnerwire.JobImplement: false, runnerwire.JobRevise: false} {
		if got := ReadOnly(runnerwire.Job{Kind: kind}); got != want {
			t.Errorf("%q: %v", kind, got)
		}
	}
}

func TestRepoConfigPaths(t *testing.T) {
	paths := RepoConfigPaths()
	for _, want := range []string{".opencode", "opencode.json", "opencode.jsonc", ".gemini", ".env", ".agents", "_agents",
		".agent", ".codex", ".claude/settings.json", ".mcp.json"} {
		if !slices.Contains(paths, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, path := range paths {
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.Contains(path, `\`) {
			t.Errorf("bad path %q", path)
		}
	}
	for _, kind := range []string{"claude", "codex", "gemini", "opencode", "antigravity"} {
		for _, path := range RunWrittenRepoConfig(kind) {
			if !slices.Contains(paths, path) {
				t.Errorf("%s writes %s, which the runner does not clear", kind, path)
			}
		}
	}
}

// A read-only OpenCode run skips the project's config and starts no LSP
// servers or formatters; an implement run keeps them.
func TestOpenCodeReadOnly(t *testing.T) {
	env := RunEnv{Binary: "/usr/bin/opencode", Workdir: t.TempDir(), TempDir: t.TempDir(),
		MCP: MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}}}
	for _, kind := range []string{runnerwire.JobReview, runnerwire.JobPlan, runnerwire.JobImplement} {
		job := runnerwire.Job{RunID: "r", NodeID: "n", Kind: kind}
		cmd, err := OpenCode{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		icmd, err := OpenCode{}.InteractiveCommand(job, env)
		if err != nil {
			t.Fatal(err)
		}
		readOnly := kind != runnerwire.JobImplement
		for _, e := range [][]string{cmd.Env, icmd.Env} {
			v, ok := envValue(e, "OPENCODE_DISABLE_PROJECT_CONFIG")
			if readOnly != (ok && v == "1") {
				t.Errorf("%s: OPENCODE_DISABLE_PROJECT_CONFIG=%q (%v)", kind, v, ok)
			}
			raw, _ := envValue(e, openCodeConfigEnv)
			var config map[string]any
			if err := json.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			if readOnly != (config["lsp"] == false && config["formatter"] == false) {
				t.Errorf("%s: config %s", kind, raw)
			}
		}
	}
}

// A read-only Gemini run has its nodevas server in the worktree's own
// .gemini/settings.json (replacing whatever is there), and every run trusts
// its worktree, without which headless Gemini CLI does not start.
func TestGeminiReadOnlyWorkspaceSettings(t *testing.T) {
	work := t.TempDir()
	env := RunEnv{Binary: "/usr/bin/gemini", Workdir: work, TempDir: t.TempDir(),
		MCP: MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp", "--actor", "run:gemini"}}}
	workspace := filepath.Join(work, ".gemini", "settings.json")

	cmd, err := Gemini{}.Command(runnerwire.Job{RunID: "r", NodeID: "n"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(cmd.Env, "GEMINI_CLI_TRUST_WORKSPACE"); v != "true" {
		t.Errorf("implement: trust = %q", v)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("an implement run wrote into its worktree: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(workspace), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace, []byte(`{"hooks":{"SessionStart":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{runnerwire.JobReview, runnerwire.JobPlan} {
		job := runnerwire.Job{RunID: "r", NodeID: "n", Kind: kind}
		cmds := map[string]func() (*exec.Cmd, error){
			"headless":    func() (*exec.Cmd, error) { return Gemini{}.Command(job, env) },
			"interactive": func() (*exec.Cmd, error) { return Gemini{}.InteractiveCommand(job, env) },
		}
		for name, build := range cmds {
			cmd, err := build()
			if err != nil {
				t.Fatal(err)
			}
			if v, _ := envValue(cmd.Env, "GEMINI_CLI_TRUST_WORKSPACE"); v != "true" {
				t.Errorf("%s %s: trust = %q", kind, name, v)
			}
			raw, err := os.ReadFile(workspace)
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]map[string]MCPServer
			if err := json.Unmarshal(raw, &settings); err != nil {
				t.Fatal(err)
			}
			if len(settings) != 1 || len(settings["mcpServers"]) != 1 ||
				settings["mcpServers"]["nodevas"].Command != "/usr/bin/nodevas" {
				t.Errorf("%s %s: workspace settings %s", kind, name, raw)
			}
		}
	}
}

// Claude Code's TUI, for a read-only run, gets the headless run's flags.
func TestClaudeInteractiveReadOnly(t *testing.T) {
	env := RunEnv{Binary: "/usr/bin/claude", Workdir: t.TempDir(), TempDir: t.TempDir(),
		MCP: MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp"}}}
	for kind, want := range map[string]bool{runnerwire.JobReview: true, runnerwire.JobImplement: false} {
		cmd, err := Claude{}.InteractiveCommand(runnerwire.Job{RunID: "r", NodeID: "n", Kind: kind}, env)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(cmd.Args, " ")
		got := strings.Contains(args, "--setting-sources user --disallowedTools "+ClaudeReadOnlyDeny+" --allowedTools")
		if got != want {
			t.Errorf("%s: %s", kind, args)
		}
	}
}

// Against the real CLI, when it is installed: listing a read-only run's MCP
// servers neither loads a project plugin nor lists the project's servers.
func TestOpenCodeMCPServersReadOnlyReal(t *testing.T) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
	dir, markers := t.TempDir(), t.TempDir()
	marker := filepath.ToSlash(filepath.Join(markers, "plugin-ran"))
	plugin := "import fs from \"fs\";\nfs.writeFileSync(" + strconvQuote(marker) + ", \"x\");\n" +
		"export const Marker = async () => ({});\n"
	if err := os.MkdirAll(filepath.Join(dir, ".opencode", "plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".opencode", "plugin", "marker.js"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}
	project := `{"mcp":{"repoEvil":{"type":"local","command":["echo","x"],"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := OpenCodeMCPServers(context.Background(), bin, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "repoEvil") {
		t.Errorf("servers %q", names)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the project's plugin ran")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
