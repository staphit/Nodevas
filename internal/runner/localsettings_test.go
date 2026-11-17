package runner

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/runnerwire"
)

// Each agent's three levels map to its CLI's own modes, both ways; a mode
// that maps to none is custom, with the exact setting.
func TestPermissionLevels(t *testing.T) {
	for kind, modes := range permissionModes {
		for i, level := range permissionLevels {
			cfg := &Config{}
			if err := cfg.setPermissionLevel(kind, level); err != nil {
				t.Fatal(err)
			}
			got, detail := PermissionLevel(cfg, kind)
			if got != level || detail != permissionKeys[kind]+"="+modes[i] || cfg.PermissionModeFor(kind) != modes[i] {
				t.Errorf("%s %s: level %s, detail %s, mode %s", kind, level, got, detail, cfg.PermissionModeFor(kind))
			}
			// Full is exactly what the auto-accept policy calls shell access.
			job := testJob("run-1", "x")
			job.Agent = kind
			if shell := cfg.ShellAccess(job); (shell != "") != (level == runnerwire.PermFull) {
				t.Errorf("%s %s: shell access %q", kind, level, shell)
			}
		}
	}
	defaults := &Config{}
	for _, kind := range Kinds {
		if level, _ := PermissionLevel(defaults, kind); level != runnerwire.PermEdit {
			t.Errorf("%s's default is %s, not edit", kind, level)
		}
	}
	for name, c := range map[string]struct {
		cfg    *Config
		kind   string
		detail string
	}{
		"claude auto":      {&Config{PermissionMode: "auto"}, "claude", "permissionMode=auto"},
		"claude dontAsk":   {&Config{PermissionMode: "dontAsk"}, "claude", "permissionMode=dontAsk"},
		"claude Bash":      {&Config{AllowedTools: []string{"Edit", "Bash(go test *)"}}, "claude", "permissionMode=acceptEdits, allowedTools Bash(go test *)"},
		"antigravity auto": {&Config{AntigravityMode: "auto"}, "antigravity", "antigravityMode=auto"},
	} {
		if level, detail := PermissionLevel(c.cfg, c.kind); level != runnerwire.PermCustom || detail != c.detail {
			t.Errorf("%s: %s %q", name, level, detail)
		}
	}
	// Full keeps Bash; read and edit take it out, or the level would lie.
	cfg := &Config{AllowedTools: []string{"Edit", "Bash(go test *)"}}
	if err := cfg.setPermissionLevel("claude", runnerwire.PermFull); err != nil || len(cfg.AllowedTools) != 2 {
		t.Fatalf("full: %v %v", cfg.AllowedTools, err)
	}
	if err := cfg.setPermissionLevel("claude", runnerwire.PermEdit); err != nil || !slices.Equal(cfg.AllowedTools, []string{"Edit"}) {
		t.Fatalf("edit: %v %v", cfg.AllowedTools, err)
	}
	if err := cfg.setPermissionLevel("claude", runnerwire.PermCustom); err == nil {
		t.Fatal("custom is shown, never set")
	}
	if err := cfg.setPermissionLevel("nope", runnerwire.PermRead); err == nil {
		t.Fatal("an unknown kind was set")
	}
}

// The built-in runner takes the app's settings: checked whole first,
// applied to a clone swapped in whole, saved with the file read fresh; the
// config it started with is left alone. A classic runner refuses them.
func TestLocalSettingsApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	start := &Config{Hub: "http://127.0.0.1:1", Token: testToken, MaxConcurrent: 2}
	if err := SaveConfig(path, start); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	c := &Client{Config: start, ConfigPath: path, Log: &log}
	on, off := true, false
	req := runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner,
		Permission: map[string]string{"codex": runnerwire.PermFull, "claude": runnerwire.PermRead}, AskEveryRun: &on,
		ShellWithoutAsking: map[string]bool{"codex": true}, AutoPR: &off, PRDraft: &on}

	// Classic: refused, nothing changed; maxConcurrent alone still goes.
	if done := c.applySettings(wsOwner, req); done.Code != runnerwire.SettingsErrLocalOnly || done.Error == "" {
		t.Fatalf("classic: %+v", done)
	}
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "r", AccountID: wsOwner, Redetect: true}); done.Code != runnerwire.SettingsErrLocalOnly {
		t.Fatalf("classic redetect: %+v", done)
	}
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "m", AccountID: wsOwner, MaxConcurrent: intp(3)}); done.Code != "" || !done.Persisted {
		t.Fatalf("classic maxConcurrent: %+v", done)
	}
	if c.cfg() != start {
		t.Fatal("a refused request replaced the config")
	}

	c.builtIn.Store(true)
	for name, bad := range map[string]runnerwire.SettingsRequest{
		"kind":       {ID: "b", AccountID: wsOwner, Permission: map[string]string{"nope": runnerwire.PermRead}},
		"level":      {ID: "b", AccountID: wsOwner, Permission: map[string]string{"codex": runnerwire.PermCustom}},
		"shell kind": {ID: "b", AccountID: wsOwner, ShellWithoutAsking: map[string]bool{"nope": true}},
		"range":      {ID: "b", AccountID: wsOwner, AskEveryRun: &on, MaxConcurrent: intp(99)},
	} {
		if done := c.applySettings(wsOwner, bad); done.Code == "" || c.cfg() != start {
			t.Errorf("%s: %+v (config replaced: %v)", name, done, c.cfg() != start)
		}
	}
	if done := c.applySettings("u-other", req); done.Code != runnerwire.SettingsErrRefused {
		t.Fatalf("someone else: %+v", done)
	}

	done := c.applySettings(wsOwner, req)
	if done.Code != "" || done.Error != "" || !done.Persisted {
		t.Fatalf("applied: %+v", done)
	}
	now := c.cfg()
	if now == start || now.CodexSandbox != "danger-full-access" || now.PermissionMode != "plan" || !now.AskEveryRun ||
		!slices.Equal(now.ShellWithoutAsking, []string{"codex"}) || now.AutoPROn() || !now.PRDraft {
		t.Fatalf("in effect: %+v", now)
	}
	if start.CodexSandbox != "" || start.AskEveryRun || start.ShellWithoutAsking != nil || start.AutoPR != nil {
		t.Fatalf("the starting config changed: %+v", start)
	}
	saved, err := LoadConfig(path)
	if err != nil || saved.CodexSandbox != "danger-full-access" || saved.PermissionMode != "plan" || !saved.AskEveryRun ||
		!slices.Equal(saved.ShellWithoutAsking, []string{"codex"}) || saved.AutoPROn() || !saved.PRDraft ||
		saved.MaxConcurrent != 3 || saved.Token != testToken {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	local := localSettings(now, path)
	if local.Permission["codex"] != runnerwire.PermFull || local.Permission["claude"] != runnerwire.PermRead ||
		local.PermissionDetail["codex"] != "codexSandbox=danger-full-access" || !local.AskEveryRun || local.AutoPR ||
		!local.PRDraft || local.ConfigPath != path || !slices.Equal(local.ShellWithoutAsking, []string{"codex"}) {
		t.Fatalf("local = %+v", local)
	}
	// Turning it off again; nothing set means nothing persisted.
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s2", AccountID: wsOwner,
		ShellWithoutAsking: map[string]bool{"codex": false}, AskEveryRun: &off}); done.Code != "" || !done.Persisted {
		t.Fatalf("off: %+v", done)
	}
	if c.cfg().ShellWithoutAsking != nil || c.cfg().AskEveryRun {
		t.Fatalf("after off: %+v", c.cfg())
	}
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s3", AccountID: wsOwner}); done.Code != "" || done.Persisted {
		t.Fatalf("empty: %+v", done)
	}
	// No settings file: in effect until the runner stops, said so.
	memory := &Client{Config: &Config{}}
	memory.builtIn.Store(true)
	if done := memory.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, AskEveryRun: &on}); done.Code != "" ||
		done.Persisted || !memory.cfg().AskEveryRun {
		t.Fatalf("no file: %+v", done)
	}
}

// A settings change applies to the runs after it without a restart; a run
// already going keeps the settings it was offered under. Run with -race.
func TestSettingsLiveApplyKeepsEachRunsSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	env := startBuiltInRunner(t, func(c *Client) {
		c.ConfigPath = path
		if err := SaveConfig(path, c.Config); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.hello()
	first := testJob("run-first", "sleep:")
	first.Origin = runnerwire.OriginPerson
	env.hub.offer(first)
	env.hub.wait("run-first", runnerwire.TypeAccept)
	if args := env.recorded(t, "run-first", "args"); !strings.Contains(args, `"acceptEdits"`) {
		t.Fatalf("first run's args %s", args)
	}

	// Readers all along, as the runs and the connection are.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					cfg := env.client.cfg()
					_ = cfg.PermissionModeFor("claude")
					_ = cfg.AutoPROn()
					_ = env.client.MaxConcurrentRuns()
					_ = localSettings(cfg, path)
				}
			}
		}()
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest, Settings: &runnerwire.SettingsRequest{ID: "s-live",
		AccountID: wsOwner, Permission: map[string]string{"claude": runnerwire.PermFull}}})
	done := waitSettingsDone(t, env, "s-live")
	close(stop)
	readers.Wait()
	if done.Code != "" || !done.Persisted {
		t.Fatalf("settings_done %+v", done)
	}
	// The hello before the answer shows it.
	var hello runnerwire.Envelope
	for len(env.hub.hellos) > 0 {
		hello = <-env.hub.hellos
	}
	if hello.Hello == nil || hello.Hello.Local == nil || hello.Hello.Local.Permission["claude"] != runnerwire.PermFull {
		t.Fatalf("hello after the change %+v", hello.Hello)
	}

	second := testJob("run-second", "ok:")
	second.Origin = runnerwire.OriginPerson
	env.hub.offer(second)
	env.hub.wait("run-second", runnerwire.TypeFinish)
	if args := env.recorded(t, "run-second", "args"); !strings.Contains(args, `"bypassPermissions"`) {
		t.Fatalf("second run's args %s", args)
	}
	env.client.jobs.mu.Lock()
	r := env.client.jobs.runs["run-first"]
	var kept string
	if r != nil {
		kept = r.cfg.ClaudePermissionMode()
	}
	env.client.jobs.mu.Unlock()
	if kept != "acceptEdits" {
		t.Fatalf("the running run's settings changed to %q", kept)
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-first", Reason: "done"})
	env.hub.wait("run-first", runnerwire.TypeFinish)
}

// The first start as the built-in runner carries autoAccept.allowShell over
// to shellWithoutAsking for the agents installed here that it covered, once,
// and says what changed.
func TestMigrateBuiltIn(t *testing.T) {
	agents := []runnerwire.Agent{{Kind: "claude", Installed: true}, {Kind: "codex", Installed: true}, {Kind: "gemini"}}
	path := filepath.Join(t.TempDir(), "runner.json")
	cfg := &Config{Hub: "http://127.0.0.1:1", AutoAccept: &runnerwire.AutoAcceptPolicy{Enabled: true, AllowShell: true, Repos: []string{"r1"},
		Agents: []string{"claude", "codex", "gemini", "opencode"}}}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	c := &Client{Config: cfg, ConfigPath: path, Log: &log}
	c.migrateBuiltIn(agents)
	now := c.cfg()
	if !slices.Equal(now.ShellWithoutAsking, []string{"claude", "codex"}) || !now.BuiltInMigrated || !now.AutoAccept.AllowShell {
		t.Fatalf("migrated: %+v", now)
	}
	saved, err := LoadConfig(path)
	if err != nil || !slices.Equal(saved.ShellWithoutAsking, []string{"claude", "codex"}) || !saved.BuiltInMigrated ||
		saved.AutoAccept == nil || !saved.AutoAccept.AllowShell || !slices.Equal(saved.AutoAccept.Repos, []string{"r1"}) {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if !strings.Contains(log.String(), "begin right away") || !strings.Contains(log.String(), "claude, codex") ||
		!strings.Contains(log.String(), "每個工作開始前都先問我") {
		t.Fatalf("log %q", log.String())
	}
	// Once: the owner's later choice stays.
	c.settingsMu.Lock()
	if _, err := c.updateConfig(func(cfg *Config) error { cfg.ShellWithoutAsking = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	c.settingsMu.Unlock()
	log.Reset()
	c.migrateBuiltIn(agents)
	if c.cfg().ShellWithoutAsking != nil || log.Len() != 0 {
		t.Fatalf("migrated twice: %+v %q", c.cfg().ShellWithoutAsking, log.String())
	}
	// Without allowShell nothing is carried over; nor for an agent it never
	// covered (not on auto-accept's list, or auto-accept off).
	plain := &Client{Config: &Config{}, Log: &log}
	plain.migrateBuiltIn(agents)
	if plain.cfg().ShellWithoutAsking != nil || !plain.cfg().BuiltInMigrated {
		t.Fatalf("plain: %+v", plain.cfg())
	}
	listed := &Client{Config: &Config{AutoAccept: &runnerwire.AutoAcceptPolicy{Enabled: true, AllowShell: true, Agents: []string{"claude"}}}, Log: &log}
	listed.migrateBuiltIn(agents)
	if !slices.Equal(listed.cfg().ShellWithoutAsking, []string{"claude"}) {
		t.Fatalf("only claude was on the list: %+v", listed.cfg().ShellWithoutAsking)
	}
	off := &Client{Config: &Config{AutoAccept: &runnerwire.AutoAcceptPolicy{AllowShell: true, Agents: []string{"claude", "codex"}}}, Log: &log}
	off.migrateBuiltIn(agents)
	if off.cfg().ShellWithoutAsking != nil {
		t.Fatalf("auto-accept was off: %+v", off.cfg().ShellWithoutAsking)
	}
}

// Clone shares nothing a change could reach.
func TestConfigClone(t *testing.T) {
	on := true
	labels := []string{"a"}
	cfg := &Config{Repos: []runnerwire.Repo{{ID: "r1"}}, AllowedTools: []string{"Edit"}, ShellWithoutAsking: []string{"codex"},
		AutoAccept: &runnerwire.AutoAcceptPolicy{Repos: []string{"r1"}}, AutoPR: &on, PRLabels: &labels,
		Models: map[string][]runnerwire.Model{"claude": {{ID: "m"}}}}
	if _, err := cfg.SetRepoTestCommand("r1", "go test ./...", 5); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(cfg)
	clone := cfg.Clone()
	clone.Repos[0].ID = "x"
	clone.AllowedTools[0] = "x"
	clone.ShellWithoutAsking[0] = "x"
	clone.AutoAccept.Repos[0] = "x"
	*clone.AutoPR = false
	(*clone.PRLabels)[0] = "x"
	clone.Models["claude"][0].ID = "x"
	clone.repoTests["r1"].Command[0] = "x"
	if after, _ := json.Marshal(cfg); string(after) != string(before) {
		t.Fatalf("changing the clone changed the original:\n%s\n%s", before, after)
	}
	if again, _ := json.Marshal(cfg.Clone()); string(again) != string(before) {
		t.Fatalf("clone differs:\n%s\n%s", before, again)
	}
}

// "Ask me each time an agent wants to run a command" from the app: in effect
// at once, saved with the rest of the file kept, and in the next hello.
func TestLocalSettingsPermissionRelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	start := &Config{Hub: "http://127.0.0.1:1", Token: testToken, MaxConcurrent: 2, PRDraft: true}
	if err := SaveConfig(path, start); err != nil {
		t.Fatal(err)
	}
	c := &Client{Config: start, ConfigPath: path, Log: &bytes.Buffer{}}
	c.builtIn.Store(true)
	if c.refreshInfo(runnerwire.Info{}).PermissionRelay {
		t.Fatal("relay on before it was turned on")
	}
	on, off := true, false
	done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, PermissionRelay: &on})
	if done.Code != "" || !done.Persisted || !c.cfg().PermissionRelay {
		t.Fatalf("on: %+v, in effect %v", done, c.cfg().PermissionRelay)
	}
	if !c.refreshInfo(runnerwire.Info{}).PermissionRelay {
		t.Fatal("the hello does not follow the change")
	}
	saved, err := LoadConfig(path)
	if err != nil || !saved.PermissionRelay || saved.Token != testToken || saved.MaxConcurrent != 2 || !saved.PRDraft {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s2", AccountID: wsOwner, PermissionRelay: &off}); done.Code != "" ||
		c.cfg().PermissionRelay || c.refreshInfo(runnerwire.Info{PermissionRelay: true}).PermissionRelay {
		t.Fatalf("off: %+v", done)
	}
	if saved, _ := LoadConfig(path); saved.PermissionRelay {
		t.Fatal("off was not saved")
	}
	if start.PermissionRelay {
		t.Fatal("the starting config changed")
	}
}

// editLocal gives the same config however often it runs, and an emptied
// agentPaths is no agentPaths.
func TestEditLocalIdempotent(t *testing.T) {
	on := true
	req := runnerwire.SettingsRequest{PermissionRelay: &on, AskEveryRun: &on,
		Permission: map[string]string{"codex": runnerwire.PermFull}, ShellWithoutAsking: map[string]bool{"codex": true},
		AgentPath: map[string]string{"claude": "/opt/claude/bin/claude"}}
	once := &Config{AgentPaths: map[string]string{"codex": "/x/codex"}}
	if err := editLocal(req)(once); err != nil {
		t.Fatal(err)
	}
	twice := once.Clone()
	if err := editLocal(req)(twice); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(once)
	b, _ := json.Marshal(twice)
	if string(a) != string(b) || once.AgentPaths["claude"] != "/opt/claude/bin/claude" || once.AgentPaths["codex"] != "/x/codex" ||
		!once.PermissionRelay {
		t.Fatalf("once %s\ntwice %s", a, b)
	}
	clear := runnerwire.SettingsRequest{AgentPath: map[string]string{"claude": ""}}
	cfg := &Config{AgentPaths: map[string]string{"claude": "/opt/claude/bin/claude"}}
	for range 2 {
		if err := editLocal(clear)(cfg); err != nil || cfg.AgentPaths != nil {
			t.Fatalf("cleared: %+v, %v", cfg.AgentPaths, err)
		}
	}
	local := localSettings(once, "")
	if local.AgentPaths["claude"] != "/opt/claude/bin/claude" {
		t.Fatalf("local %+v", local.AgentPaths)
	}
	if local.AgentPaths["claude"] = "x"; once.AgentPaths["claude"] == "x" {
		t.Fatal("Info.Local shares the config's map")
	}
}

// The result preview is the built-in runner's alone: offered while
// runPreview is on, and never taken from a hello a classic runner starts
// from.
func TestRunPreviewCapability(t *testing.T) {
	forged := runnerwire.Info{Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapRunPreview, runnerwire.CapAppAccept}}
	classic := &Client{Config: &Config{}}
	if caps := classic.refreshInfo(forged).Capabilities; slices.Contains(caps, runnerwire.CapRunPreview) ||
		slices.Contains(caps, runnerwire.CapAppAccept) || !slices.Contains(caps, runnerwire.CapHeadless) {
		t.Fatalf("classic: %q", caps)
	}
	builtIn := &Client{Config: &Config{}}
	builtIn.builtIn.Store(true)
	caps := builtIn.refreshInfo(runnerwire.Info{Capabilities: []string{runnerwire.CapHeadless}}).Capabilities
	if !slices.Contains(caps, runnerwire.CapRunPreview) {
		t.Fatalf("built-in: %q", caps)
	}
	again := builtIn.refreshInfo(forged).Capabilities
	if n := len(slices.DeleteFunc(slices.Clone(again), func(cp string) bool { return cp != runnerwire.CapRunPreview })); n != 1 {
		t.Fatalf("run-preview %d times: %q", n, again)
	}
	off := false
	builtIn.Config.RunPreview = &off
	if caps := builtIn.refreshInfo(forged).Capabilities; slices.Contains(caps, runnerwire.CapRunPreview) {
		t.Fatalf("runPreview off: %q", caps)
	}
}
