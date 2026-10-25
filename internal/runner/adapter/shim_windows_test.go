//go:build windows

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

const shimHelperEnv = "NODEVAS_ADAPTER_SHIM_HELPER"

// TestShimHelper stands in for node behind an npm .cmd shim: it records the
// arguments it was given.
func TestShimHelper(t *testing.T) {
	out := os.Getenv(shimHelperEnv)
	if out == "" {
		return
	}
	// os.Args: the test binary, -test.run=…, then what the shim passed on.
	raw, _ := json.Marshal(os.Args[2:])
	_ = os.WriteFile(out, raw, 0o600)
	os.Exit(0)
}

// A real .cmd shim, in a directory with spaces and parentheses (as under
// "Program Files (x86)" or a user name with a space), forwarding %* the way
// npm's shims do: every argument must arrive unchanged.
func TestBatchShimPassesArgumentsThrough(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "npm prefix (x86)")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "codex.cmd")
	script := "@ECHO off\r\nSETLOCAL\r\n\"" + os.Args[0] + "\" \"-test.run=^TestShimHelper$\" %*\r\n"
	if err := os.WriteFile(shim, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "args.json")

	job := runnerwire.Job{RunID: "run-1", NodeID: "n-1", Prompt: "100% \"quoted\" & piped | prompt", Model: "gpt-5.5"}
	env := RunEnv{
		Binary:  shim,
		Workdir: t.TempDir(),
		TempDir: t.TempDir(),
		MCP: MCPServer{Command: `C:\Program Files (x86)\Nodevas Tools\nodevas.exe`,
			Args: []string{"mcp", "--server", "http://127.0.0.1:5/r/ab", "--project", "team board/sub dir", "--actor", "run:codex",
				"--session-name", "demo-codex-gpt-5-5-run1"}},
	}
	cmd, err := Codex{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") || cmd.SysProcAttr == nil ||
		!strings.Contains(cmd.SysProcAttr.CmdLine, " /d /v:off /s /c \"") {
		t.Fatalf("not run through cmd.exe: %q %+v", cmd.Path, cmd.SysProcAttr)
	}
	cmd.Env = append(cmd.Env, shimHelperEnv+"="+record)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shim: %v: %s", err, out)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	// The same command for a binary that is not a shim: its arguments as built.
	plain := env
	plain.Binary = `C:\bin\codex.exe`
	direct, err := Codex{}.Command(job, plain)
	if err != nil {
		t.Fatal(err)
	}
	if want := direct.Args[1:]; !slices.Equal(got, want) {
		t.Fatalf("arguments through the shim:\n got %q\nwant %q", got, want)
	}

	// A path cmd.exe would act on is refused, not passed on mangled.
	env.MCP.Command = `C:\100%\nodevas.exe`
	if _, err := (Codex{}).Command(job, env); err == nil || !strings.Contains(err.Error(), ".cmd launcher") {
		t.Errorf("unsafe argument: err = %v", err)
	}
	if _, err := (Codex{}).Command(job, RunEnv{Binary: `C:\bin\codex.exe`, Workdir: env.Workdir, MCP: env.MCP}); err != nil {
		t.Errorf("the same argument for a real .exe: %v", err)
	}
}
