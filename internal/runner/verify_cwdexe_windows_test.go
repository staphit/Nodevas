//go:build windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A verify job's worktree holds whatever the PR's head holds. When the
// repository's test command is an npm-style .cmd shim that runs a bare
// "node", cmd.exe must not pick up a node.cmd the branch put at the
// worktree's root in place of the real one: the test command runs outside
// every agent sandbox, as the owner, and the owner chose that command, not
// the branch's node.cmd.
func TestVerifyCommandDoesNotRunWorktreeNode(t *testing.T) {
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	os.Unsetenv("NoDefaultCurrentDirectoryInExePath")

	// "node" on PATH stands in for the real one and says it ran.
	onPath := t.TempDir()
	ranNode := filepath.Join(t.TempDir(), "node-ran")
	if err := os.WriteFile(filepath.Join(onPath, "node.cmd"), []byte("@echo node> \""+ranNode+"\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The owner's test command: a shim that runs a bare "node".
	shim := filepath.Join(t.TempDir(), "npm.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\nnode %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	planted := filepath.Join(t.TempDir(), "planted-ran")
	if err := os.WriteFile(filepath.Join(wt, "node.cmd"), []byte("@echo pwned> \""+planted+"\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	exit, out, timedOut, _ := runTestCommand(context.Background(), wt, []string{shim, "test"}, 30*time.Second)
	if _, err := os.Stat(planted); err == nil {
		t.Fatalf("the worktree's node.cmd ran as the runner owner (exit %d, timed out %v); output: %s", exit, timedOut, out)
	}
	if _, err := os.Stat(ranNode); err != nil {
		t.Fatalf("the test command never reached node: %v (exit %d); output: %s", err, exit, out)
	}
}

// scrubbedEnv sets NoDefaultCurrentDirectoryInExePath once, whatever the
// runner's own environment had under that name in any letter case.
func TestScrubbedEnvSkipsCurrentDirectory(t *testing.T) {
	t.Setenv("NODEFAULTCURRENTDIRECTORYINEXEPATH", "")
	var found []string
	for _, kv := range scrubbedEnv() {
		name, value, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "NoDefaultCurrentDirectoryInExePath") {
			found = append(found, value)
		}
	}
	if len(found) != 1 || found[0] == "" {
		t.Fatalf("NoDefaultCurrentDirectoryInExePath in the test command's environment: %q, want one non-empty value", found)
	}
}
