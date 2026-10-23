package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

// writeRunnerConfig writes a runner.json with two repositories, the second
// at a real directory so it can be named by path.
func writeRunnerConfig(t *testing.T, autoAccept string) (path, repoDir string) {
	t.Helper()
	saved := runnerCommands
	runnerCommands = stubCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	repoDir = t.TempDir()
	repos, _ := json.Marshal([]runnerwire.Repo{
		{ID: "aaaa11112222", Name: "alpha", Path: "/src/alpha"},
		{ID: runner.RepoID(repoDir), Name: "beta", Path: repoDir},
	})
	body := `{"hub":"https://hub.example","repos":` + string(repos)
	if autoAccept != "" {
		body += `,"autoAccept":` + autoAccept
	}
	path = filepath.Join(t.TempDir(), "runner.json")
	if err := os.WriteFile(path, []byte(body+"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, repoDir
}

func TestRunnerLegacyAutoAcceptMigrates(t *testing.T) {
	cfgPath, repoDir := writeRunnerConfig(t, "true")
	code, stdout, stderr := runRunner(t, "", "status", "--config", cfgPath)
	if code != 0 || strings.Count(stderr, runner.LegacyAutoAcceptWarning) != 1 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"autoAccept             on", "stepUp                 OPTIONAL",
		"repos                  alpha (aaaa11112222), beta (" + runner.RepoID(repoDir) + ")",
		"agents                 claude", "modes                  headless"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}
	// Saved in the new shape: the warning was the one time.
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), `"requireStepUp": false`) {
		t.Fatalf("not migrated on disk:\n%s", raw)
	}
	if _, _, stderr = runRunner(t, "", "status", "--config", cfgPath); strings.Contains(stderr, "WARNING") {
		t.Fatalf("warned again: %q", stderr)
	}

	// Legacy false is just off, and nothing is rewritten.
	cfgPath, _ = writeRunnerConfig(t, "false")
	before, _ := os.ReadFile(cfgPath)
	code, stdout, stderr = runRunner(t, "", "autoaccept", "show", "--config", cfgPath)
	after, _ := os.ReadFile(cfgPath)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "autoAccept             off") || string(before) != string(after) {
		t.Fatalf("false: exit %d %q\n%s", code, stderr, stdout)
	}
}

func TestRunnerAutoAcceptCommands(t *testing.T) {
	cfgPath, repoDir := writeRunnerConfig(t, "")
	run := func(args ...string) (string, string) {
		t.Helper()
		code, stdout, stderr := runRunner(t, "", append(append([]string{"autoaccept"}, args...), "--config", cfgPath)...)
		if code != 0 {
			t.Fatalf("autoaccept %v: exit %d, stderr %q", args, code, stderr)
		}
		return stdout, stderr
	}
	policy := func() runnerwire.AutoAcceptPolicy {
		t.Helper()
		cfg, err := runner.LoadConfig(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.AutoAcceptPolicy()
	}

	stdout, _ := run("show")
	if !strings.Contains(stdout, "autoAccept             off") || !strings.Contains(stdout, "stepUp                 required") ||
		!strings.Contains(stdout, "repos                  (none)") {
		t.Fatalf("show:\n%s", stdout)
	}

	stdout, stderr := run("on")
	p := policy()
	if !p.Enabled || !p.RequireStepUp || !slices.Equal(p.Repos, []string{"aaaa11112222", runner.RepoID(repoDir)}) ||
		!slices.Equal(p.Agents, []string{"claude"}) || !slices.Equal(p.Modes, []string{""}) {
		t.Fatalf("on: %+v", p)
	}
	if !strings.Contains(stderr, runner.AutoAcceptWarning) || strings.Contains(stderr, runner.StepUpOptionalWarning) ||
		!strings.Contains(stdout, "autoAccept             on") {
		t.Fatalf("on output: %q\n%s", stderr, stdout)
	}

	if _, stderr = run("step-up", "optional"); !strings.Contains(stderr, runner.StepUpOptionalWarning) || policy().RequireStepUp {
		t.Fatalf("step-up optional: %q", stderr)
	}
	if _, stderr = run("step-up", "required"); strings.Contains(stderr, runner.StepUpOptionalWarning) || !policy().RequireStepUp {
		t.Fatalf("step-up required: %q", stderr)
	}

	run("repo", "remove", "alpha")
	run("repo", "remove", repoDir)
	if p := policy(); len(p.Repos) != 0 {
		t.Fatalf("repos left: %v", p.Repos)
	}
	if _, stderr = run("agent", "remove", "claude"); !strings.Contains(stderr, "nothing is auto-accepted") {
		t.Fatalf("empty lists not noted: %q", stderr)
	}
	run("agent", "add", "claude")
	run("repo", "add", "aaaa11112222")
	if stdout, _ = run("repo", "add", "BETA"); !strings.Contains(stdout, "auto-accept now covers beta") {
		t.Fatalf("repo add by name:\n%s", stdout)
	}

	if stdout, _ = run("agent", "add", "codex"); !strings.Contains(stdout, "not installed here yet") {
		t.Fatalf("agent add codex:\n%s", stdout)
	}
	run("agent", "remove", "claude")
	run("mode", "add", "interactive")
	run("mode", "remove", "headless")
	p = policy()
	if !slices.Equal(p.Agents, []string{"codex"}) || !slices.Equal(p.Modes, []string{runnerwire.ModeInteractive}) ||
		len(p.Repos) != 2 {
		t.Fatalf("after edits: %+v", p)
	}

	// Off keeps the lists; on brings them back as they were.
	run("off")
	if p := policy(); p.Enabled || !slices.Equal(p.Agents, []string{"codex"}) {
		t.Fatalf("off: %+v", p)
	}
	run("on")
	if p := policy(); !p.Enabled || !slices.Equal(p.Agents, []string{"codex"}) {
		t.Fatalf("on again: %+v", p)
	}

	// `set autoAccept` maps onto on (with the defaults) and off.
	if code, _, stderr := runRunner(t, "", "set", "autoAccept", "true", "--config", cfgPath); code != 0 {
		t.Fatalf("set true: %q", stderr)
	}
	if p := policy(); !p.Enabled || !p.RequireStepUp || !slices.Equal(p.Agents, []string{"claude"}) ||
		len(p.Repos) != 2 || !slices.Equal(p.Modes, []string{""}) {
		t.Fatalf("set true: %+v", p)
	}
	if code, _, _ := runRunner(t, "", "set", "autoAccept", "false", "--config", cfgPath); code != 0 || policy().Enabled {
		t.Fatal("set false did not switch off")
	}

	// `runner repo remove` drops the repository from the list too.
	if code, _, stderr := runRunner(t, "", "repo", "remove", "alpha", "--config", cfgPath); code != 0 {
		t.Fatalf("repo remove: %q", stderr)
	}
	if p := policy(); slices.Contains(p.Repos, "aaaa11112222") {
		t.Fatalf("removed repo still listed: %v", p.Repos)
	}

	for _, bad := range [][]string{{}, {"maybe"}, {"on", "extra"}, {"step-up"}, {"step-up", "sometimes"},
		{"repo", "add"}, {"repo", "add", "nope"}, {"repo", "list", "alpha"}, {"agent", "add", "bash"},
		{"mode", "add", "tmux"}, {"mode", "remove", "interactive"}, {"agent", "remove", "gemini"}} {
		if code, _, _ := runRunner(t, "", append(append([]string{"autoaccept"}, bad...), "--config", cfgPath)...); code == 0 {
			t.Errorf("autoaccept %v succeeded", bad)
		}
	}
}
