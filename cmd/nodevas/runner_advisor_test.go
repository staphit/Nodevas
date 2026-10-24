package main

import (
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
)

func TestRunnerAdvisorKeyFromStdinNeverPrinted(t *testing.T) {
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	t.Setenv(runner.CopilotKeyEnv, "")
	t.Setenv(runner.AdvisorKeyEnv, "")

	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	if code, _, stderr := runRunner(t, cliTestToken+"\n", "login", "--hub", "https://hub.example", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	for _, kv := range [][2]string{{"advisorProvider", "openai"}, {"advisorModel", "gpt-5.4-mini"}} {
		if code, _, stderr := runRunner(t, "", "set", kv[0], kv[1], "--config", cfgPath); code != 0 {
			t.Fatalf("set %s: %s", kv[0], stderr)
		}
	}
	code, stdout, _ := runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 || !strings.Contains(stdout, "off: no advisor key") {
		t.Fatalf("status without a key:\n%s", stdout)
	}
	code, stdout, stderr := runRunner(t, "sk-advisor-very-secret\n", "advisor-key", "--config", cfgPath)
	if code != 0 || !strings.Contains(stdout, "advisor key saved") {
		t.Fatalf("advisor-key exit %d: %s %s", code, stdout, stderr)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil || cfg.AdvisorAPIKey != "sk-advisor-very-secret" || cfg.CopilotAPIKey != "" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	code, stdout, stderr = runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "very-secret") {
		t.Fatalf("status printed the advisor key:\n%s", stdout)
	}
	for _, want := range []string{"autopilot advisor:", "advisorModel           gpt-5.4-mini",
		"advisorKey             stored in this file", "on: autopilot asks openai/gpt-5.4-mini"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}
	if code, _, stderr := runRunner(t, "", "advisor-key", "--clear", "--config", cfgPath); code != 0 {
		t.Fatal(stderr)
	}
	if cfg, _ = runner.LoadConfig(cfgPath); cfg.AdvisorAPIKey != "" {
		t.Fatal("--clear kept the key")
	}
}
