package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A runner passes its run's proxy URL, which carries a secret, in a file:
// a command line is readable by every user on the machine.
func TestMCPServerFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-server")
	if err := os.WriteFile(path, []byte("http://127.0.0.1:41234/r/secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := func(args ...string) (string, error) {
		fs, f := newMCPFlags(flag.ContinueOnError)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return mcpServerURL(fs, f)
	}
	if got, err := parse("--server-file", path, "--project", "p"); err != nil || got != "http://127.0.0.1:41234/r/secret" {
		t.Errorf("--server-file: %q, %v", got, err)
	}
	if got, err := parse("--server", "http://127.0.0.1:5666"); err != nil || got != "http://127.0.0.1:5666" {
		t.Errorf("--server: %q, %v", got, err)
	}
	if _, err := parse("--server", "http://127.0.0.1:5666", "--server-file", path); err == nil {
		t.Error("--server with --server-file was taken")
	}
	if _, err := parse("--server-file", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing --server-file was taken")
	}
}

func TestMCPArgsForPrefersTheRunsCommandLine(t *testing.T) {
	own := []string{"--server", "http://127.0.0.1:5666", "--actor", "mcp:antigravity"}
	run := []string{"--server", "http://127.0.0.1:41234/r/secret", "--project", "p", "--agent-role", "worker",
		"--actor", "run:antigravity", "--session-name", "repo-antigravity-default-abc123", "--toolset", "run"}
	cases := []struct {
		name, env string
		want      []string
	}{
		{"unset", "", own},
		{"the run's", `["mcp","--server","http://127.0.0.1:41234/r/secret","--project","p","--agent-role","worker",` +
			`"--actor","run:antigravity","--session-name","repo-antigravity-default-abc123","--toolset","run"]`, run},
		{"no flags", `["mcp"]`, []string{}},
		{"not mcp", `["serve","--server","x"]`, own},
		{"not JSON", `--server x`, own},
		{"not strings", `["mcp", 1]`, own},
		{"an unknown flag", `["mcp","--frobnicate"]`, own},
		{"a stray argument", `["mcp","--server","x","extra"]`, own},
	}
	for _, tc := range cases {
		if got := mcpArgsFor(own, tc.env); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: mcpArgsFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}
