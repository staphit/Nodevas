package main

import (
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
)

func TestRunnerCopilotKeyFromStdinNeverPrinted(t *testing.T) {
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	t.Setenv(runner.CopilotKeyEnv, "")

	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	if code, _, stderr := runRunner(t, cliTestToken+"\n", "login", "--hub", "https://hub.example", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	if code, _, stderr := runRunner(t, "", "set", "copilotProvider", "anthropic", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	if code, _, _ := runRunner(t, "has space\n", "copilot-key", "--config", cfgPath); code == 0 {
		t.Fatal("a key with a space was accepted")
	}
	code, stdout, stderr := runRunner(t, "sk-ant-very-secret\n", "copilot-key", "--config", cfgPath)
	if code != 0 || !strings.Contains(stdout, "copilot key saved") {
		t.Fatalf("copilot-key exit %d: %s %s", code, stdout, stderr)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil || cfg.CopilotAPIKey != "sk-ant-very-secret" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}

	code, stdout, stderr = runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "very-secret") {
		t.Fatalf("status printed the copilot key:\n%s", stdout)
	}
	for _, want := range []string{"copilotProvider        anthropic", "copilotKey             stored in this file", "on: your copilot's model calls run here"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}

	if code, _, stderr := runRunner(t, "", "copilot-key", "--clear", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	if cfg, _ = runner.LoadConfig(cfgPath); cfg.CopilotAPIKey != "" {
		t.Fatal("--clear kept the key")
	}
}
