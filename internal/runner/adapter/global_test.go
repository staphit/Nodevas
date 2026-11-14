package adapter

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func globalJob() runnerwire.Job {
	return runnerwire.Job{RunID: "run-g1", NodeTitle: "Tidy the README", Prompt: "# Tidy the README\n\nfix typos",
		Agent: "claude", Model: "sonnet", Global: true, Slug: "tidy-the-readme", NoPR: true}
}

// A global agent's env as the runner builds it: no MCP arguments, only the
// nodevas binary (agy's hooks run it), relay asked for by the owner's
// setting and to be ignored.
func globalEnv(t *testing.T, binary string) RunEnv {
	t.Helper()
	return RunEnv{
		Binary:          binary,
		Workdir:         t.TempDir(),
		Branch:          "agent/tidy-the-readme-run-g1",
		TempDir:         t.TempDir(),
		MCP:             MCPServer{Command: "/usr/bin/nodevas"},
		NoBoard:         true,
		PermissionRelay: true,
	}
}

// boardWords are what only a run with a board may be told or given.
var boardWords = []string{"claim_task", "set_node_status", "ask_user", "get_node", "approve_tool", "mcp__nodevas"}

func noBoardWords(t *testing.T, what string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		for _, word := range boardWords {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %s:\n%s", what, word, text)
			}
		}
	}
}

func TestGlobalPrompts(t *testing.T) {
	job := globalJob()
	headless := Prompt(job, "agent/tidy-the-readme-run-g1")
	if headless != GlobalPrompt(job, "agent/tidy-the-readme-run-g1") {
		t.Fatal("Prompt does not use the global preamble for a global job")
	}
	wantAll(t, "headless", headless, "global agent (no board, no project)", "agent/tidy-the-readme-run-g1",
		"Nobody can answer questions", "Do not push", "keeps your branch for its owner to review",
		"short summary", "The task:\n\n# Tidy the README\n\nfix typos\n")
	interactive := InteractivePrompt(job, "agent/tidy-the-readme-run-g1")
	if interactive != GlobalInteractivePrompt(job, "agent/tidy-the-readme-run-g1") {
		t.Fatal("InteractivePrompt does not use the global preamble for a global job")
	}
	wantAll(t, "interactive", interactive, "running interactively for Nodevas as a global agent",
		"may type to you", "the session ends when the person quits it", "fix typos")
	noBoardWords(t, "global prompt", headless, interactive)
	wantNone(t, "global prompt", headless+interactive, "node", "Nodevas node")

	// Asked for a pull request: the runner's to open.
	job.NoPR = false
	wantAll(t, "with a PR", Prompt(job, "b"), "the runner handles that after you finish")
	wantAll(t, "interactive with a PR", InteractivePrompt(job, "b"), "the runner handles that after the session ends")
	wantNone(t, "with a PR", Prompt(job, "b"), "keeps your branch")
}

func readJSON(t *testing.T, path string, v any) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(raw)
}

// Claude Code with no board: an empty, strict MCP config, no relay, and
// only the owner's own tools allowed.
func TestClaudeNoBoard(t *testing.T) {
	job := globalJob()
	env := globalEnv(t, plainBinary())
	cmd, err := Claude{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(env.TempDir, "mcp.json")
	want := []string{plainBinary(), "-p", "--output-format", "stream-json", "--verbose",
		"--permission-mode", DefaultClaudePermissionMode, "--mcp-config", mcpPath, "--strict-mcp-config", "--model", "sonnet"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q\nwant %q", cmd.Args, want)
	}
	if _, set := envValue(cmd.Env, ClaudeMCPTimeoutEnv); set && os.Getenv(ClaudeMCPTimeoutEnv) == "" {
		t.Error("a run that relays nothing got the relay's MCP timeout")
	}
	var config struct {
		MCPServers map[string]MCPServer `json:"mcpServers"`
	}
	if raw := readJSON(t, mcpPath, &config); config.MCPServers == nil || len(config.MCPServers) != 0 {
		t.Errorf("mcp config = %s", raw)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	noBoardWords(t, "claude prompt", string(stdin))
	if ClaudeRelays(env) || RelayMCPTools("claude", env) != nil {
		t.Error("a run with no board relays permission prompts")
	}

	// The owner's allowedTools still apply.
	env.AllowedTools = []string{"Bash(go test *)"}
	if cmd, err = (Claude{}).Command(job, env); err != nil {
		t.Fatal(err)
	}
	if got := cmd.Args[len(cmd.Args)-2:]; !slices.Equal(got, []string{"--allowedTools", "Bash(go test *)"}) {
		t.Errorf("allowedTools = %q", cmd.Args)
	}

	// Interactive: the same, the prompt first.
	job.Mode = runnerwire.ModeInteractive
	env.AllowedTools = nil
	cmd, err = Claude{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	noBoardWords(t, "claude interactive", strings.Join(args, " "))
	if !slices.Contains(args, "--strict-mcp-config") || slices.Contains(args, "--allowedTools") ||
		!strings.Contains(args[0], "as a global agent") {
		t.Errorf("interactive args = %q", args)
	}
	if raw := readJSON(t, mcpPath, &config); len(config.MCPServers) != 0 {
		t.Errorf("interactive mcp config = %s", raw)
	}
}

func TestCodexNoBoard(t *testing.T) {
	job := globalJob()
	job.Agent = "codex"
	env := globalEnv(t, "/usr/bin/codex")
	headless, err := Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	job.Mode = runnerwire.ModeInteractive
	interactive, err := Codex{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{"headless": headless.Args, "interactive": interactive.Args} {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "mcp_servers") {
			t.Errorf("%s codex got an MCP server: %q", name, args)
		}
		noBoardWords(t, name+" codex", joined)
	}
	if !slices.Contains(headless.Args, "approval_policy='never'") || headless.Args[len(headless.Args)-1] != "-" {
		t.Errorf("headless args = %q", headless.Args)
	}
}

func TestGeminiNoBoard(t *testing.T) {
	job := globalJob()
	job.Agent = "gemini"
	env := globalEnv(t, "/usr/bin/gemini")
	headless, err := Gemini{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	job.Mode = runnerwire.ModeInteractive
	interactive, err := Gemini{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string][]string{"headless": headless.Args, "interactive": interactive.Args} {
		i := slices.Index(cmd, "--allowed-mcp-server-names")
		if i < 0 || cmd[i+1] != geminiNoServer || slices.Contains(cmd, "nodevas") {
			t.Errorf("%s gemini may load an MCP server: %q", name, cmd)
		}
		noBoardWords(t, name+" gemini", strings.Join(cmd, " "))
	}
	path, ok := envValue(headless.Env, geminiSystemSettingsEnv)
	if !ok {
		t.Fatal("no settings override")
	}
	var settings struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if raw := readJSON(t, path, &settings); settings.MCPServers == nil || len(settings.MCPServers) != 0 {
		t.Errorf("settings = %s", raw)
	}
}

// OpenCode with no board: no nodevas server of the run's, the owner's own
// "nodevas" switched off with the rest, no nodevas tools allowed, and over
// ACP nobody to relay a permission request to.
func TestOpenCodeNoBoard(t *testing.T) {
	job := globalJob()
	job.Agent = "opencode"
	type config struct {
		MCP        map[string]json.RawMessage `json:"mcp"`
		Permission map[string]string          `json:"permission"`
	}
	read := func(t *testing.T, env []string) (config, string) {
		t.Helper()
		raw, ok := envValue(env, openCodeConfigEnv)
		if !ok {
			t.Fatal("no inline config")
		}
		var c config
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if _, ok := envValue(env, RunSessionEnv); ok {
			t.Errorf("a run with no board pins a sidecar session: %v", env)
		}
		return c, raw
	}
	for _, version := range []string{"opencode 1.18.23", "opencode v2.1.4"} {
		env := globalEnv(t, "/usr/bin/opencode")
		env.Version = version
		env.DisableMCP = []string{"nodevas", "owners"}
		cmd, err := OpenCode{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		c, raw := read(t, cmd.Env)
		if OpenCodeV2(version) {
			var v2 struct {
				MCP struct {
					Servers map[string]map[string]any `json:"servers"`
				} `json:"mcp"`
			}
			_ = json.Unmarshal([]byte(raw), &v2)
			servers := v2.MCP.Servers
			if len(servers) != 2 || servers["nodevas"]["disabled"] != true || servers["owners"]["disabled"] != true {
				t.Errorf("%s: servers = %s", version, raw)
			}
			if _, ok := c.Permission["nodevas_*"]; ok {
				t.Errorf("%s: nodevas tools allowed: %s", version, raw)
			}
		} else {
			var nodevas, owners map[string]any
			_ = json.Unmarshal(c.MCP["nodevas"], &nodevas)
			_ = json.Unmarshal(c.MCP["owners"], &owners)
			if len(c.MCP) != 2 || len(nodevas) != 1 || nodevas["enabled"] != false || owners["enabled"] != false {
				t.Errorf("%s: servers = %s", version, raw)
			}
		}
		stdin, _ := io.ReadAll(cmd.Stdin)
		noBoardWords(t, version+" prompt", string(stdin))

		// The owner has no nodevas server: nothing names one at all.
		env.DisableMCP = []string{"owners"}
		cmd, err = OpenCode{}.Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if _, raw := read(t, cmd.Env); strings.Contains(raw, `"nodevas"`) {
			t.Errorf("%s: an owner without a nodevas server gets one named: %s", version, raw)
		}
	}

	// Over ACP, with the owner's relay on in edit: nobody to ask.
	env := globalEnv(t, "/usr/bin/opencode")
	env.Version = "opencode 1.18.23"
	if openCodeACPRelay(job, env) {
		t.Error("a run with no board relays over ACP")
	}
	cmd, err := OpenCodeACP{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if c, raw := read(t, cmd.Env); c.Permission["bash"] != "deny" || strings.Contains(raw, `"nodevas"`) {
		t.Errorf("acp config = %s", raw)
	}
	conv := OpenCodeACP{}.NewConversation(job, env).(*acpSession)
	if conv.asker != nil {
		t.Error("an ACP session with no board has an asker")
	}
	noBoardWords(t, "acp prompt", conv.prompt)
}

// agy with no board: an empty customization root's MCP config, the plain
// gate still in it, no run MCP arguments or node for `nodevas mcp` or the
// gate to find, and no relay.
func TestAntigravityNoBoard(t *testing.T) {
	job := globalJob()
	job.Agent = "antigravity"
	env := globalEnv(t, "/usr/bin/agy")
	cmd, relays, err := Antigravity{}.CommandRelay(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if relays {
		t.Error("a run with no board relays shell commands")
	}
	for _, name := range []string{RunMCPEnv, RunNodeEnv, RunSessionEnv} {
		if _, ok := envValue(cmd.Env, name); ok {
			t.Errorf("%s set for a run with no board", name)
		}
	}
	if _, ok := agyArg(cmd.Args, "--settings"); ok {
		t.Errorf("relay settings for a run with no board: %q", cmd.Args)
	}
	root := filepath.Join(env.TempDir, antigravityRootDir, ".agents")
	var config struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if raw := readJSON(t, filepath.Join(root, "mcp_config.json"), &config); config.MCPServers == nil || len(config.MCPServers) != 0 {
		t.Errorf("mcp config = %s", raw)
	}
	if hooks, err := os.ReadFile(filepath.Join(root, "hooks.json")); err == nil &&
		strings.Contains(string(hooks), AntigravityGateRelayFlag) {
		t.Errorf("the gate asks Nodevas: %s", hooks)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	noBoardWords(t, "agy prompt", string(stdin))

	job.Mode = runnerwire.ModeInteractive
	cmd, err = Antigravity{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envValue(cmd.Env, RunMCPEnv); ok {
		t.Error("interactive: RunMCPEnv set for a run with no board")
	}
	noBoardWords(t, "agy interactive", strings.Join(cmd.Args, " "))
}
