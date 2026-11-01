//go:build windows

package adapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// npmShim is npm's cmd-shim for a package's bin (cmd-shim/lib/index.js):
// node.exe next to the shim when there is one, else plain "node", which
// cmd.exe looks up in the current directory before PATH unless
// NoDefaultCurrentDirectoryInExePath is set.
const npmShim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
	"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
	"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\agent\\cli.js\" %*\r\n"

// A run's worktree holds whatever its branch holds. A node.cmd at its root
// must not run in place of node when the agent CLI is an npm shim: cmd.exe
// would run it as the owner, before the CLI and outside its sandbox.
func TestAgentShimDoesNotRunWorktreeNode(t *testing.T) {
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	os.Unsetenv("NoDefaultCurrentDirectoryInExePath")

	// "node" on PATH stands in for the real one and says it ran.
	onPath := t.TempDir()
	ranNode := filepath.Join(t.TempDir(), "node-ran")
	if err := os.WriteFile(filepath.Join(onPath, "node.cmd"), []byte("@echo node> \""+ranNode+"\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, ad := range []Adapter{Codex{}, Claude{}, Gemini{}, OpenCode{}} {
		t.Run(ad.Kind(), func(t *testing.T) {
			_ = os.Remove(ranNode)
			npm := t.TempDir()
			shim := filepath.Join(npm, Binary(ad.Kind())+".cmd")
			if err := os.WriteFile(shim, []byte(npmShim), 0o600); err != nil {
				t.Fatal(err)
			}
			wt := t.TempDir()
			planted := filepath.Join(t.TempDir(), "planted-ran")
			if err := os.WriteFile(filepath.Join(wt, "node.cmd"), []byte("@echo pwned> \""+planted+"\"\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			job := runnerwire.Job{RunID: "run-1", NodeID: "n-1", Kind: runnerwire.JobReview, Agent: ad.Kind(), Prompt: "review it"}
			cmd, err := ad.Command(job, RunEnv{Binary: shim, Workdir: wt, TempDir: t.TempDir(),
				MCP: MCPServer{Command: `C:\nodevas\nodevas.exe`, Args: []string{"mcp"}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, cmd.Path)
			run.Args, run.Dir, run.Env, run.Stdin, run.SysProcAttr = cmd.Args, cmd.Dir, cmd.Env, cmd.Stdin, cmd.SysProcAttr
			out, _ := run.CombinedOutput()
			if _, err := os.Stat(planted); err == nil {
				t.Fatalf("the worktree's node.cmd ran as the runner owner; output: %s", out)
			}
			if _, err := os.Stat(ranNode); err != nil {
				t.Fatalf("the shim never reached node: %v; output: %s", err, out)
			}
		})
	}
}

// The interactive commands, which a terminal starts rather than cmd.exe /c,
// carry the same setting to cmd.exe running the shim.
func TestInteractiveShimEnvSkipsCurrentDirectory(t *testing.T) {
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	os.Unsetenv("NoDefaultCurrentDirectoryInExePath")
	for _, ad := range []Adapter{Claude{}, Codex{}, Gemini{}, OpenCode{}, Antigravity{}} {
		ia, ok := ad.(Interactive)
		if !ok {
			continue
		}
		job := runnerwire.Job{RunID: "run-1", NodeID: "n-1", Agent: ad.Kind(), Mode: runnerwire.ModeInteractive}
		cmd, err := ia.InteractiveCommand(job, RunEnv{Binary: `C:\npm\` + Binary(ad.Kind()) + ".cmd", Workdir: t.TempDir(),
			TempDir: t.TempDir(), MCP: MCPServer{Command: `C:\nodevas\nodevas.exe`, Args: []string{"mcp"}}})
		if err != nil {
			t.Fatalf("%s: %v", ad.Kind(), err)
		}
		if v, ok := envValue(cmd.Env, "NoDefaultCurrentDirectoryInExePath"); !ok || v == "" {
			t.Errorf("%s interactive: NoDefaultCurrentDirectoryInExePath = %q, %v", ad.Kind(), v, ok)
		}
	}
}
