package main

import (
	"reflect"
	"testing"
)

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
