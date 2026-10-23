package agentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude stands in for the `claude` CLI: it keeps one registered MCP
// server and records every call.
type fakeClaude struct {
	registered string // `claude mcp get` output; empty means none
	calls      []string
}

func (f *fakeClaude) run(_ string, args ...string) (string, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case strings.HasPrefix(call, "mcp get"):
		if f.registered == "" {
			return "", errors.New("No MCP server found")
		}
		return f.registered, nil
	case strings.HasPrefix(call, "mcp remove"):
		f.registered = ""
	case strings.HasPrefix(call, "mcp add"):
		_, cmd, _ := strings.Cut(call, " -- ")
		exe, rest, _ := strings.Cut(cmd, " ")
		f.registered = "nodevas:\n  Command: " + exe + "\n  Args: " + rest + "\n"
	}
	return "", nil
}

func (f *fakeClaude) called(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func testEnv(t *testing.T, goos string) (Env, *fakeClaude) {
	t.Helper()
	home := t.TempDir()
	fake := &fakeClaude{}
	exe := "/opt/nodevas/bin/nodevas"
	if goos == "windows" {
		exe = `C:\Tools\nodevas.exe`
	}
	return Env{
		Home: home,
		Exe:  exe,
		GOOS: goos,
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return "/usr/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		Run: fake.run,
	}, fake
}

func opts(agents ...string) Options {
	return Options{Agents: agents, Server: "http://127.0.0.1:5666", AgentRole: "worker"}
}

func mustApply(t *testing.T, env Env, o Options) Report {
	t.Helper()
	reports := Apply(env, o)
	if len(reports) != 1 {
		t.Fatalf("got %d reports", len(reports))
	}
	if reports[0].Err != nil {
		t.Fatalf("%s: %v", reports[0].Agent, reports[0].Err)
	}
	return reports[0]
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func unchanged(r Report) bool {
	for _, l := range r.Lines {
		if strings.HasPrefix(l, "changed") || strings.HasPrefix(l, "would change") ||
			strings.HasPrefix(l, "added") || strings.HasPrefix(l, "replaced") {
			return false
		}
	}
	return true
}

func TestShellCmd(t *testing.T) {
	args := []string{"hook", "claude", "stop", "--actor", "mcp:claude"}
	cases := []struct {
		name string
		sh   shell
		goos string
		exe  string
		want string
	}{
		{"posix plain", posixShell, "linux", "/usr/local/bin/nodevas", "/usr/local/bin/nodevas hook claude stop --actor mcp:claude"},
		{"posix spaces", posixShell, "darwin", "/Users/Al Smith/bin/nodevas", "'/Users/Al Smith/bin/nodevas' hook claude stop --actor mcp:claude"},
		{"posix quote", posixShell, "linux", "/home/o'neil/nodevas", `'/home/o'\''neil/nodevas' hook claude stop --actor mcp:claude`},
		{"bash on windows", posixShell, "windows", `C:\Tools\nodevas.exe`, "C:/Tools/nodevas.exe hook claude stop --actor mcp:claude"},
		{"bash on windows spaces", posixShell, "windows", `C:\Program Files\nodevas.exe`, "'C:/Program Files/nodevas.exe' hook claude stop --actor mcp:claude"},
		{"powershell plain", powerShell, "windows", `C:\Tools\nodevas.exe`, `C:\Tools\nodevas.exe hook claude stop --actor mcp:claude`},
		{"powershell spaces", powerShell, "windows", `C:\Users\Al O'Neil\nodevas.exe`, `& 'C:\Users\Al O''Neil\nodevas.exe' hook claude stop --actor mcp:claude`},
	}
	for _, c := range cases {
		if got := shellCmd(c.sh, c.goos, c.exe, args); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	if got := shellCmd(powerShell, "windows", `C:\x.exe`, []string{"--project", "My Project"}); got != `C:\x.exe --project 'My Project'` {
		t.Errorf("powershell arg quoting: %s", got)
	}
}

func TestOurHook(t *testing.T) {
	exe := "/opt/nodevas/bin/nodevas"
	for cmd, want := range map[string]bool{
		"/opt/nodevas/bin/nodevas hook claude stop --actor mcp:claude": true,
		"/old/place/nodevas hook claude stop":                          true, // moved binary
		`& 'C:\Program Files\nodevas.exe' hook claude stop`:            true,
		"/opt/nodevas/bin/nodevas hook codex stop":                     false, // other agent
		"/usr/bin/reason-network hook claude stop":                     false, // other tool
		"echo nodevas": false,
	} {
		if got := ourHook(cmd, "claude", exe); got != want {
			t.Errorf("ourHook(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestClaudeFreshIdempotentAndRemove(t *testing.T) {
	env, fake := testEnv(t, "linux")
	settings := filepath.Join(env.Home, ".claude", "settings.json")

	r := mustApply(t, env, opts(Claude))
	got := read(t, settings)
	for _, want := range []string{
		`"SessionStart"`, `"UserPromptSubmit"`, `"PostToolUse"`, `"Stop"`, `"SessionEnd"`,
		`/opt/nodevas/bin/nodevas hook claude session-start --server http://127.0.0.1:5666 --actor mcp:claude`,
		`"mcp__nodevas__ask_user"`, `"mcp__nodevas__send_message"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json missing %s:\n%s", want, got)
		}
	}
	if exists(settings + backupSuffix) {
		t.Error("backed up a file that did not exist")
	}
	if fake.called("mcp add") != 1 {
		t.Fatalf("mcp add calls = %d; calls %v", fake.called("mcp add"), fake.calls)
	}
	if !strings.Contains(fake.registered, "mcp --server http://127.0.0.1:5666 --actor mcp:claude --agent-role worker") {
		t.Errorf("registered %q", fake.registered)
	}
	_ = r

	r = mustApply(t, env, opts(Claude))
	if !unchanged(r) || read(t, settings) != got {
		t.Errorf("second run changed something: %v", r.Lines)
	}
	if fake.called("mcp add") != 1 || fake.called("mcp remove") != 0 {
		t.Errorf("second run re-registered: %v", fake.calls)
	}

	o := opts(Claude)
	o.Remove = true
	mustApply(t, env, o)
	if exists(settings) && strings.TrimSpace(read(t, settings)) != "{}" {
		t.Errorf("remove left: %s", read(t, settings))
	}
	if fake.registered != "" {
		t.Error("remove kept the MCP server")
	}
}

func TestClaudeKeepsPersonsSettings(t *testing.T) {
	env, _ := testEnv(t, "windows")
	settings := filepath.Join(env.Home, ".claude", "settings.json")
	original := "\xef\xbb\xbf" + `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(git status)"],
    "deny": ["Read(.env)"]
  },
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-send done && echo <ok>"}]}
    ],
    "PreToolUse": [
      {"matcher": "Grep", "hooks": [{"type": "command", "command": "C:/x/gate.exe"}]}
    ]
  },
  "zeta": 1.50
}`
	write(t, settings, original)

	mustApply(t, env, opts(Claude))
	got := read(t, settings)
	if read(t, settings+backupSuffix) != original {
		t.Error("backup is not the original")
	}
	for _, want := range []string{
		`notify-send done && echo <ok>`, // no HTML escaping
		`C:/x/gate.exe`, `"Read(.env)"`, `"Bash(git status)"`, `"zeta": 1.50`,
		`C:/Tools/nodevas.exe hook claude stop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	if strings.Index(got, `"model"`) > strings.Index(got, `"zeta"`) {
		t.Error("key order not kept")
	}

	// A second run backs up nothing more, even though the file changed.
	write(t, settings+backupSuffix, "sentinel")
	mustApply(t, env, opts(Claude))
	if read(t, settings+backupSuffix) != "sentinel" {
		t.Error("backup overwritten")
	}

	o := opts(Claude)
	o.Remove = true
	mustApply(t, env, o)
	got = read(t, settings)
	if strings.Contains(got, "nodevas") {
		t.Errorf("remove left nodevas behind:\n%s", got)
	}
	for _, want := range []string{`notify-send`, `C:/x/gate.exe`, `"Read(.env)"`, `"Bash(git status)"`, `"model"`} {
		if !strings.Contains(got, want) {
			t.Errorf("remove dropped %s:\n%s", want, got)
		}
	}
}

func TestClaudeReplacesForeignRegistration(t *testing.T) {
	env, fake := testEnv(t, "linux")
	fake.registered = "nodevas:\n  Command: /old/nodevas\n  Args: mcp\n"
	mustApply(t, env, opts(Claude))
	if fake.called("mcp remove") != 1 || fake.called("mcp add") != 1 {
		t.Fatalf("calls %v", fake.calls)
	}
}

func TestClaudeWithoutCLI(t *testing.T) {
	env, fake := testEnv(t, "linux")
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	r := mustApply(t, env, opts(Claude))
	if len(fake.calls) != 0 {
		t.Errorf("ran claude without it: %v", fake.calls)
	}
	if len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "claude mcp add") {
		t.Errorf("notes %v", r.Notes)
	}
}

func TestMovedBinaryReplacesOldHooks(t *testing.T) {
	env, _ := testEnv(t, "linux")
	mustApply(t, env, opts(Claude))
	env.Exe = "/new/home/nodevas"
	mustApply(t, env, opts(Claude))
	got := read(t, filepath.Join(env.Home, ".claude", "settings.json"))
	if strings.Contains(got, "/opt/nodevas/bin/nodevas") {
		t.Errorf("old hooks left behind:\n%s", got)
	}
	if strings.Count(got, "hook claude stop") != 1 {
		t.Errorf("stop hook count = %d", strings.Count(got, "hook claude stop"))
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	env, fake := testEnv(t, "linux")
	o := opts(Claude)
	o.DryRun = true
	r := Apply(env, o)[0]
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if exists(filepath.Join(env.Home, ".claude")) {
		t.Error("dry run created files")
	}
	if fake.called("mcp add") != 0 {
		t.Error("dry run registered the MCP server")
	}
	if !strings.HasPrefix(r.Lines[0], "would change") {
		t.Errorf("lines %v", r.Lines)
	}
	for _, a := range []string{Codex, Gemini} {
		if r := Apply(env, Options{Agents: []string{a}, DryRun: true, Server: "http://x"})[0]; r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	entries, _ := os.ReadDir(env.Home)
	if len(entries) != 0 {
		t.Errorf("dry run wrote %v", entries)
	}
}

const codexOriginal = `# my codex config
model = "gpt-5"

[mcp_servers.other]
command = "other"

[mcp_servers.nodevas]
command = "/old/nodevas"
args = ["mcp"]

[mcp_servers.nodevas.env]
FOO = "bar"

# profiles below
[profiles.fast]
model = "mini"
`

func TestCodexTOMLReplaceAndRemove(t *testing.T) {
	env, _ := testEnv(t, "windows")
	env.CodexHome = filepath.Join(env.Home, "codexhome")
	cfg := filepath.Join(env.CodexHome, "config.toml")
	write(t, cfg, codexOriginal)

	mustApply(t, env, opts(Codex))
	got := read(t, cfg)
	want := `# my codex config
model = "gpt-5"

[mcp_servers.other]
command = "other"

[mcp_servers.nodevas]
command = "C:\\Tools\\nodevas.exe"
args = ["mcp", "--server", "http://127.0.0.1:5666", "--actor", "mcp:codex", "--agent-role", "worker"]
env = { NODEVAS_AGENT = "codex" }
tool_timeout_sec = 660

# profiles below
[profiles.fast]
model = "mini"
`
	if got != want {
		t.Errorf("config.toml:\n%s\nwant:\n%s", got, want)
	}
	if read(t, cfg+backupSuffix) != codexOriginal {
		t.Error("backup is not the original")
	}
	hooks := read(t, filepath.Join(env.CodexHome, "hooks.json"))
	if !strings.Contains(hooks, `C:\\Tools\\nodevas.exe hook codex stop`) {
		t.Errorf("hooks.json:\n%s", hooks)
	}

	r := mustApply(t, env, opts(Codex))
	if !unchanged(r) || read(t, cfg) != want {
		t.Errorf("second run changed: %v", r.Lines)
	}

	o := opts(Codex)
	o.Remove = true
	mustApply(t, env, o)
	got = read(t, cfg)
	wantRemoved := `# my codex config
model = "gpt-5"

[mcp_servers.other]
command = "other"

# profiles below
[profiles.fast]
model = "mini"
`
	if got != wantRemoved {
		t.Errorf("after remove:\n%s\nwant:\n%s", got, wantRemoved)
	}
	if exists(filepath.Join(env.CodexHome, "hooks.json")) && strings.Contains(read(t, filepath.Join(env.CodexHome, "hooks.json")), "nodevas") {
		t.Error("hooks.json still has nodevas")
	}
}

func TestReplaceTOMLTable(t *testing.T) {
	block := "[mcp_servers.nodevas]\ncommand = \"n\"\n"
	cases := []struct{ name, in, block, want string }{
		{"append to empty", "", block, block},
		{"append", "a = 1\n", block, "a = 1\n\n" + block},
		{"append no newline", "a = 1", block, "a = 1\n\n" + block},
		{"remove absent", "a = 1\n", "", "a = 1\n"},
		{"remove at end", "a = 1\n\n[mcp_servers.nodevas]\ncommand = \"n\"\n\n", "", "a = 1\n"},
		{"quoted header", "[mcp_servers.\"nodevas\"]\nx = 1\n[b]\n", block, block + "[b]\n"},
		{"crlf", "a = 1\r\n\r\n[mcp_servers.nodevas]\r\nx = 1\r\n", block, "a = 1\r\n\r\n[mcp_servers.nodevas]\r\ncommand = \"n\"\r\n"},
		{"not a prefix match", "[mcp_servers.nodevas2]\nx = 1\n", "", "[mcp_servers.nodevas2]\nx = 1\n"},
		{"header in multiline string", "s = \"\"\"\n[mcp_servers.nodevas]\n\"\"\"\n", "", "s = \"\"\"\n[mcp_servers.nodevas]\n\"\"\"\n"},
	}
	for _, c := range cases {
		got, err := replaceTOMLTable(c.in, "mcp_servers.nodevas", c.block)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	for _, inline := range []string{
		"[mcp_servers]\nnodevas = { command = \"x\" }\n",
		"mcp_servers.nodevas.command = \"x\"\n",
	} {
		if _, err := replaceTOMLTable(inline, "mcp_servers.nodevas", block); err == nil {
			t.Errorf("inline definition accepted: %q", inline)
		}
	}
}

func TestGeminiKeepsOtherServers(t *testing.T) {
	env, _ := testEnv(t, "linux")
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	write(t, settings, `{"theme": "dark", "mcpServers": {"github": {"command": "gh-mcp"}}, "hooks": {"AfterAgent": [{"matcher": "*", "hooks": [{"type": "command", "command": "say done"}]}]}}`)

	mustApply(t, env, opts(Gemini))
	got := read(t, settings)
	for _, want := range []string{`"github"`, `"say done"`, `"theme": "dark"`, `"nodevas": {`, `"timeout": 660000`,
		`"name": "nodevas-stop"`, `"BeforeAgent"`, `"AfterTool"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	r := mustApply(t, env, opts(Gemini))
	if !unchanged(r) || read(t, settings) != got {
		t.Errorf("second run changed: %v", r.Lines)
	}

	o := opts(Gemini)
	o.Remove = true
	mustApply(t, env, o)
	got = read(t, settings)
	if strings.Contains(got, "nodevas") {
		t.Errorf("remove left nodevas:\n%s", got)
	}
	for _, want := range []string{`"github"`, `"say done"`, `"theme"`} {
		if !strings.Contains(got, want) {
			t.Errorf("remove dropped %s", want)
		}
	}
}

func TestRemoveFromNothingCreatesNothing(t *testing.T) {
	env, _ := testEnv(t, "linux")
	for _, a := range Agents {
		o := opts(a)
		o.Remove = true
		mustApply(t, env, o)
	}
	entries, _ := os.ReadDir(env.Home)
	if len(entries) != 0 {
		t.Errorf("remove created %v", entries)
	}
}

func TestRefusesNonObjectJSON(t *testing.T) {
	env, _ := testEnv(t, "linux")
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	write(t, settings, `[1, 2]`)
	r := Apply(env, opts(Gemini))[0]
	if r.Err == nil {
		t.Fatal("accepted a non-object settings file")
	}
	if read(t, settings) != `[1, 2]` {
		t.Error("touched the file")
	}
}

func TestDetectAndParseAgents(t *testing.T) {
	env, _ := testEnv(t, "linux")
	if err := os.MkdirAll(filepath.Join(env.Home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(Detect(env), ","); got != "claude,gemini" {
		t.Errorf("Detect = %s", got)
	}
	if got, err := ParseAgents(" Codex,claude,codex "); err != nil || strings.Join(got, ",") != "codex,claude" {
		t.Errorf("ParseAgents = %v, %v", got, err)
	}
	if _, err := ParseAgents("cursor"); err == nil {
		t.Error("accepted unknown agent")
	}
}
