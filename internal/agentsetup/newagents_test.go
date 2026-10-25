package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAntigravityWiresMCPHooksAndPermissionAndRemoves(t *testing.T) {
	env, _ := testEnv(t, "linux")
	gem := filepath.Join(env.Home, ".gemini")
	mcpConfig := filepath.Join(gem, "config", "mcp_config.json")
	hooks := filepath.Join(gem, "config", "hooks.json")
	settings := filepath.Join(gem, "antigravity-cli", "settings.json")
	write(t, mcpConfig, `{"mcpServers": {"unityMCP": {"serverUrl": "http://127.0.0.1:8080/mcp", "type": "http"}}}`)
	write(t, hooks, `{"orca-status": {"Stop": [{"type": "command", "command": "C:/orca/antigravity-stop.cmd", "timeout": 10}]}}`)
	write(t, settings, "\xef\xbb\xbf"+`{"colorScheme": "dark", "trustedWorkspaces": ["/w"]}`)

	r := mustApply(t, env, opts(Antigravity))
	got := read(t, mcpConfig)
	for _, want := range []string{`"unityMCP"`, `"nodevas": {`, `"command": "/opt/nodevas/bin/nodevas"`,
		`"NODEVAS_AGENT": "antigravity"`, `"--actor"`, `"mcp:antigravity"`} {
		if !strings.Contains(got, want) {
			t.Errorf("mcp_config.json missing %s:\n%s", want, got)
		}
	}
	got = read(t, hooks)
	for _, want := range []string{`"orca-status"`, `"C:/orca/antigravity-stop.cmd"`, `"nodevas": {`,
		`"SessionStart"`, `"PreInvocation"`, `"matcher": "*"`,
		`"command": "/opt/nodevas/bin/nodevas hook antigravity user-prompt-submit --server http://127.0.0.1:5666 --actor mcp:antigravity"`,
		`"command": "/opt/nodevas/bin/nodevas hook antigravity stop --server http://127.0.0.1:5666 --actor mcp:antigravity"`,
		`"timeout": 5`} {
		if !strings.Contains(got, want) {
			t.Errorf("hooks.json missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "session-end") {
		t.Errorf("the Antigravity CLI has no session-end event:\n%s", got)
	}
	got = read(t, settings)
	for _, want := range []string{`"colorScheme": "dark"`, `"trustedWorkspaces"`, `"mcp(nodevas/*)"`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json missing %s:\n%s", want, got)
		}
	}
	if len(r.Notes) == 0 {
		t.Error("no note about calling nodevas tools without asking")
	}

	r = mustApply(t, env, opts(Antigravity))
	if !unchanged(r) {
		t.Errorf("second run changed: %v", r.Lines)
	}

	o := opts(Antigravity)
	o.Remove = true
	mustApply(t, env, o)
	for path, keep := range map[string][]string{
		mcpConfig: {`"unityMCP"`},
		hooks:     {`"orca-status"`, `"C:/orca/antigravity-stop.cmd"`},
		settings:  {`"colorScheme"`, `"trustedWorkspaces"`},
	} {
		got := read(t, path)
		if strings.Contains(got, "nodevas") {
			t.Errorf("remove left nodevas in %s:\n%s", path, got)
		}
		for _, want := range keep {
			if !strings.Contains(got, want) {
				t.Errorf("remove dropped %s from %s", want, path)
			}
		}
	}
	if got := read(t, settings); strings.Contains(got, "permissions") {
		t.Errorf("remove left an empty permissions object:\n%s", got)
	}
}

func TestAntigravityHookCommandOnWindows(t *testing.T) {
	env, _ := testEnv(t, "windows")
	mustApply(t, env, opts(Antigravity))
	got := read(t, filepath.Join(env.Home, ".gemini", "config", "hooks.json"))
	want := `"command": "C:/Tools/nodevas.exe hook antigravity session-start --server http://127.0.0.1:5666 --actor mcp:antigravity"`
	if !strings.Contains(got, want) {
		t.Errorf("hooks.json missing %s:\n%s", want, got)
	}
	if strings.Contains(got, `\\`) || strings.Contains(got, `\"`) {
		t.Errorf("a backslash or quote in a cmd /c hook command:\n%s", got)
	}

	// A path cmd.exe would need quoted cannot be a hook; the MCP server,
	// started with an argument list, still can.
	env, _ = testEnv(t, "windows")
	env.Exe = `C:\Program Files\nodevas\nodevas.exe`
	r := Apply(env, opts(Antigravity))[0]
	if r.Err == nil || !strings.Contains(r.Err.Error(), "Program Files") {
		t.Fatalf("Err = %v, want the path refused", r.Err)
	}
	if exists(filepath.Join(env.Home, ".gemini", "config", "hooks.json")) {
		t.Error("wrote hooks.json with a command cmd.exe cannot run")
	}
	if !strings.Contains(read(t, filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")), `Program Files`) {
		t.Error("did not wire the MCP server")
	}
}

func TestAntigravityCmd(t *testing.T) {
	args := []string{"hook", "antigravity", "stop", "--server", "http://127.0.0.1:5666"}
	cases := []struct {
		name, goos, exe string
		extra           []string
		want            string
		bad             bool
	}{
		{"windows", "windows", `C:\Tools\nodevas.exe`, nil, "C:/Tools/nodevas.exe hook antigravity stop --server http://127.0.0.1:5666", false},
		{"windows space", "windows", `C:\Program Files\nodevas.exe`, nil, "", true},
		{"windows project space", "windows", `C:\Tools\nodevas.exe`, []string{"--project", "My Project"}, "", true},
		{"windows percent", "windows", `C:\Tools\nodevas.exe`, []string{"--project", "100%"}, "", true},
		{"windows ampersand", "windows", `C:\Tools\nodevas.exe`, []string{"--project", "a&b"}, "", true},
		{"posix", "linux", "/opt/nodevas", []string{"--project", "My Project"},
			"/opt/nodevas hook antigravity stop --server http://127.0.0.1:5666 --project 'My Project'", false},
		{"posix apostrophe", "darwin", "/Users/o'neil/nodevas", nil, "", true},
	}
	for _, c := range cases {
		got, err := antigravityCmd(c.goos, c.exe, append(append([]string(nil), args...), c.extra...))
		if c.bad {
			if err == nil {
				t.Errorf("%s: accepted %q", c.name, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", c.name, got, err, c.want)
		}
	}
}

func TestOpenCodeMCPAndPluginAndRemove(t *testing.T) {
	env, _ := testEnv(t, "linux")
	dir := filepath.Join(env.Home, ".config", "opencode")
	config := filepath.Join(dir, "opencode.json")
	plugin := filepath.Join(dir, "plugins", "nodevas.js")
	orca := filepath.Join(dir, "plugins", "orca-opencode-status.js")
	write(t, config, `{"$schema": "https://opencode.ai/config.json", "provider": {"macmini": {"npm": "@ai-sdk/openai-compatible"}}, "model": "macmini/qwen", "mcp": {"unityMCP": {"type": "remote", "url": "http://127.0.0.1:8080/mcp", "enabled": true}}}`)
	write(t, orca, "// someone else's plugin\n")

	o := opts(OpenCode)
	o.Project = "board"
	mustApply(t, env, o)
	got := read(t, config)
	for _, want := range []string{`"provider"`, `"model": "macmini/qwen"`, `"unityMCP"`, `"nodevas": {`,
		`"type": "local"`, `"/opt/nodevas/bin/nodevas",`, `"enabled": true`, `"timeout": 660000`,
		`"NODEVAS_AGENT": "opencode"`, `"mcp:opencode"`} {
		if !strings.Contains(got, want) {
			t.Errorf("opencode.json missing %s:\n%s", want, got)
		}
	}
	js := read(t, plugin)
	for _, want := range []string{openCodePluginMarker, `const NODEVAS = "/opt/nodevas/bin/nodevas";`,
		`const FLAGS = ["--server","http://127.0.0.1:5666","--project","board","--actor","mcp:opencode"];`,
		"process.env.NODEVAS_RUN_ID", `"chat.message"`, `"tool.execute.after"`, `"session.idle"`} {
		if !strings.Contains(js, want) {
			t.Errorf("plugin missing %s", want)
		}
	}

	if r := mustApply(t, env, o); !unchanged(r) {
		t.Errorf("second run changed: %v", r.Lines)
	}

	o.Remove = true
	mustApply(t, env, o)
	if exists(plugin) {
		t.Error("remove left the plugin")
	}
	if read(t, orca) != "// someone else's plugin\n" {
		t.Error("remove touched another plugin")
	}
	got = read(t, config)
	if strings.Contains(got, "nodevas") {
		t.Errorf("remove left nodevas:\n%s", got)
	}
	for _, want := range []string{`"provider"`, `"unityMCP"`} {
		if !strings.Contains(got, want) {
			t.Errorf("remove dropped %s", want)
		}
	}
}

func TestOpenCodeLeavesAForeignPluginAlone(t *testing.T) {
	env, _ := testEnv(t, "linux")
	plugin := filepath.Join(env.Home, ".config", "opencode", "plugins", "nodevas.js")
	write(t, plugin, "export const Mine = async () => ({});\n")
	for _, remove := range []bool{false, true} {
		o := opts(OpenCode)
		o.Remove = remove
		if r := Apply(env, o)[0]; r.Err == nil {
			t.Errorf("remove=%v: replaced a plugin nodevas did not write", remove)
		}
		if read(t, plugin) != "export const Mine = async () => ({});\n" {
			t.Fatalf("remove=%v: touched the file", remove)
		}
	}
}

func TestOpenCodeNewConfigAndXDG(t *testing.T) {
	env, _ := testEnv(t, "linux")
	env.XDGConfigHome = filepath.Join(env.Home, "xdg")
	mustApply(t, env, opts(OpenCode))
	got := read(t, filepath.Join(env.XDGConfigHome, "opencode", "opencode.json"))
	if !strings.HasPrefix(got, "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {") {
		t.Errorf("new opencode.json:\n%s", got)
	}
	if !exists(filepath.Join(env.XDGConfigHome, "opencode", "plugins", "nodevas.js")) {
		t.Error("no plugin under XDG_CONFIG_HOME")
	}
	if exists(filepath.Join(env.Home, ".config")) {
		t.Error("wrote under ~/.config despite XDG_CONFIG_HOME")
	}
}

func TestDetectsOpenCodeAndAntigravity(t *testing.T) {
	env, _ := testEnv(t, "linux")
	env.LookPath = func(name string) (string, error) {
		if name == "agy" {
			return "/usr/bin/agy", nil
		}
		return "", errors.New("not found")
	}
	if got := strings.Join(Detect(env), ","); got != "antigravity" {
		t.Errorf("Detect with agy on PATH = %s", got)
	}
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	for _, dir := range []string{filepath.Join(env.Home, ".config", "opencode"), filepath.Join(env.Home, ".gemini", "antigravity-cli")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(Detect(env), ","); got != "opencode,antigravity" {
		t.Errorf("Detect by config dirs = %s", got)
	}
	if got, err := ParseAgents("OpenCode, antigravity"); err != nil || strings.Join(got, ",") != "opencode,antigravity" {
		t.Errorf("ParseAgents = %v, %v", got, err)
	}
	if Label(OpenCode) != "OpenCode" || Label(Antigravity) != "Antigravity CLI" || Binary(Antigravity) != "agy" {
		t.Error("labels or binary name")
	}
}

// TestOpenCodePluginRuns loads the plugin in Node, with a stand-in for
// nodevas that echoes the event it was given, and drives it the way OpenCode
// does. OpenCode runs plugins on Bun; the APIs the plugin uses are the same.
func TestOpenCodePluginRuns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	// `node hook opencode <event> …` runs ./hook as a script.
	write(t, filepath.Join(dir, "hook"), `let s = "";
process.stdin.on("data", (d) => (s += d));
process.stdin.on("end", () => {
  const p = JSON.parse(s);
  if (process.argv[3] === "user-prompt-submit") process.stdout.write("mail for " + p.session_id + " in " + p.cwd + " on " + p.model + "\n");
});
`)
	src, err := openCodePluginSource(node, []string{"--server", "http://127.0.0.1:5666"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "nodevas.mjs"), string(src))
	write(t, filepath.Join(dir, "drive.mjs"), `import { NodevasPlugin } from "./nodevas.mjs";
const hooks = await NodevasPlugin({ directory: "/repo" });
await hooks.event({ event: { type: "session.created", properties: { info: { id: "sub", parentID: "ses1" } } } });
const output = { message: { id: "msg1", sessionID: "ses1" }, parts: [{ id: "prt_a", type: "text", text: "hi" }] };
await hooks["chat.message"]({ sessionID: "ses1", model: { providerID: "opencode", modelID: "m1" } }, output);
const sub = { message: { id: "msg2", sessionID: "sub" }, parts: [{ id: "prt_b", type: "text", text: "hi" }] };
await hooks["chat.message"]({ sessionID: "sub" }, sub);
await hooks["tool.execute.after"]({ tool: "edit", sessionID: "ses1", args: { filePath: "/repo/a.go" } });
process.env.NODEVAS_RUN_ID = "run1";
const inRun = await NodevasPlugin({ directory: "/repo" });
console.log(JSON.stringify({ parts: output.parts, sub: sub.parts.length, inRun: Object.keys(inRun).length }));
`)
	cmd := exec.Command(node, "drive.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODEVAS_RUN_ID=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	for _, want := range []string{
		`{"id":"prt_anodevas","sessionID":"ses1","messageID":"msg1","type":"text","text":"mail for ses1 in /repo on opencode/m1","synthetic":true}`,
		`"sub":1`, `"inRun":0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plugin output missing %s:\n%s", want, got)
		}
	}
}

// The plugin counts assistant messages' tokens against their top session,
// subagents included, seeds a session it first sees mid-way from OpenCode's
// stored messages, and passes the totals to the stop hook.
func TestOpenCodePluginUsage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "hook"), `const fs = require("fs");
let s = "";
process.stdin.on("data", (d) => (s += d));
process.stdin.on("end", () => {
  if (process.argv[3] === "stop") fs.writeFileSync(__dirname + "/stop.json", s);
});
`)
	src, err := openCodePluginSource(node, []string{"--server", "http://127.0.0.1:5666"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "nodevas.mjs"), string(src))
	write(t, filepath.Join(dir, "drive.mjs"), `import { NodevasPlugin } from "./nodevas.mjs";
import fs from "node:fs";
const msg = (id, sessionID, modelID, input, output, reasoning, read, write, cost) => ({
  id, sessionID, role: "assistant", providerID: "opencode", modelID, cost,
  tokens: { input, output, reasoning, cache: { read, write } },
});
const client = { session: { messages: async ({ path }) => ({ data: path.id === "ses1" ? [
  { info: msg("old", "ses1", "m1", 100, 10, 0, 1000, 0, 0.5), parts: [] },
  { info: { id: "u1", sessionID: "ses1", role: "user" }, parts: [] },
] : [] }) } };
const hooks = await NodevasPlugin({ directory: "/repo", client });
await hooks.event({ event: { type: "session.created", properties: { info: { id: "sub", parentID: "ses1" } } } });
const updated = (info) => hooks.event({ event: { type: "message.updated", properties: { info } } });
await updated(msg("a1", "ses1", "m1", 5, 1, 0, 0, 0, 0));
await updated(msg("a1", "ses1", "m1", 7, 2, 1, 50, 3, 0.25));
await updated(msg("s1", "sub", "m2", 4, 4, 0, 0, 0, 0));
await updated({ id: "u2", sessionID: "ses1", role: "user" });
await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses1" } } });
for (let i = 0; i < 100 && !fs.existsSync("stop.json"); i++) await new Promise((r) => setTimeout(r, 50));
console.log(fs.readFileSync("stop.json", "utf8"));
`)
	cmd := exec.Command(node, "drive.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODEVAS_RUN_ID=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var payload struct {
		SessionID string `json:"session_id"`
		Usage     []struct {
			Model            string  `json:"model"`
			InputTokens      int64   `json:"inputTokens"`
			OutputTokens     int64   `json:"outputTokens"`
			CacheReadTokens  int64   `json:"cacheReadTokens"`
			CacheWriteTokens int64   `json:"cacheWriteTokens"`
			CostUSD          float64 `json:"costUsd"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("stop payload %q: %v", out, err)
	}
	byModel := map[string]string{}
	for _, u := range payload.Usage {
		byModel[u.Model] = fmt.Sprintf("%d/%d/%d/%d/%g", u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CostUSD)
	}
	want := map[string]string{"opencode/m1": "107/13/1050/3/0.75", "opencode/m2": "4/4/0/0/0"}
	if payload.SessionID != "ses1" || !reflect.DeepEqual(byModel, want) {
		t.Fatalf("stop payload = %s\nby model %v, want %v", out, byModel, want)
	}
}

// --runs-only touches only what runner runs need: Antigravity's allow rule,
// and nothing that joins the person's own sessions to a hub.
func TestRunsOnly(t *testing.T) {
	env, _ := testEnv(t, "linux")
	gem := filepath.Join(env.Home, ".gemini")
	mcpConfig := filepath.Join(gem, "config", "mcp_config.json")
	hooks := filepath.Join(gem, "config", "hooks.json")
	settings := filepath.Join(gem, "antigravity-cli", "settings.json")
	write(t, settings, `{"colorScheme": "dark"}`)

	o := opts(Antigravity, Claude)
	o.RunsOnly = true
	r := Apply(env, o)
	for _, rep := range r {
		if rep.Err != nil {
			t.Fatalf("%s: %v", rep.Agent, rep.Err)
		}
	}
	if got := read(t, settings); !strings.Contains(got, `"mcp(nodevas/*)"`) || !strings.Contains(got, `"colorScheme": "dark"`) {
		t.Fatalf("settings.json = %s", got)
	}
	for _, path := range []string{mcpConfig, hooks, filepath.Join(env.Home, ".claude", "settings.json")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s written by --runs-only", path)
		}
	}
	if len(r) != 2 || len(r[1].Lines) != 1 || !strings.Contains(r[1].Lines[0], "nothing to change") {
		t.Fatalf("reports = %+v", r)
	}

	o.Remove = true
	for _, rep := range Apply(env, o) {
		if rep.Err != nil {
			t.Fatalf("removing %s: %v", rep.Agent, rep.Err)
		}
	}
	if got := read(t, settings); strings.Contains(got, "nodevas") {
		t.Fatalf("settings.json after removal = %s", got)
	}
}
