package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func intp(n int) *int { return &n }

// The owner's maxConcurrent from the hub: applied at once and written to
// runner.json with nothing else changed, the file as it is now; refused for
// anyone else, with remoteSettings off, or past the ceiling.
func TestApplySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	cfg := &Config{Hub: "https://hub.example", Token: testToken, MaxConcurrent: 2,
		Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: "/src/demo"}}}
	if _, err := cfg.SetRepoTestCommand("r1", "go test ./...", 0); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	c := &Client{Config: cfg, ConfigPath: path}
	if c.MaxConcurrentRuns() != 2 {
		t.Fatalf("limit before = %d", c.MaxConcurrentRuns())
	}
	for name, req := range map[string]runnerwire.SettingsRequest{
		runnerwire.SettingsErrRefused: {ID: "s", AccountID: "u-other", MaxConcurrent: intp(3)},
		runnerwire.SettingsErrRange:   {ID: "s", AccountID: wsOwner, MaxConcurrent: intp(runnerwire.DefaultMaxConcurrentCeiling + 1)},
		"zero":                        {ID: "s", AccountID: wsOwner, MaxConcurrent: intp(0)},
	} {
		done := c.applySettings(wsOwner, req)
		want := name
		if name == "zero" {
			want = runnerwire.SettingsErrRange
		}
		if done.Code != want || done.Error == "" || done.MaxConcurrent != 2 || done.Persisted || done.ID != "s" {
			t.Errorf("%s: %+v", name, done)
		}
	}
	if done := c.applySettings("", runnerwire.SettingsRequest{ID: "s", MaxConcurrent: intp(3)}); done.Code != runnerwire.SettingsErrRefused {
		t.Errorf("no owner known: %+v", done)
	}
	if c.MaxConcurrentRuns() != 2 {
		t.Fatalf("a refused request changed the limit to %d", c.MaxConcurrentRuns())
	}

	// A CLI change made since the runner started stays.
	onDisk, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := onDisk.Set("prDraft", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(path, onDisk); err != nil {
		t.Fatal(err)
	}
	done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s2", AccountID: wsOwner, MaxConcurrent: intp(5)})
	if done.Code != "" || done.Error != "" || done.MaxConcurrent != 5 || !done.Persisted || done.ID != "s2" {
		t.Fatalf("applied: %+v", done)
	}
	if c.MaxConcurrentRuns() != 5 || cfg.MaxConcurrent != 2 {
		t.Fatalf("limit %d, config %d", c.MaxConcurrentRuns(), cfg.MaxConcurrent)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.MaxConcurrent != 5 || !saved.PRDraft || saved.Token != testToken || len(saved.Repos) != 1 {
		t.Fatalf("saved = %+v", saved)
	}
	if command, _ := saved.RepoTestCommand("r1"); !slices.Equal(command, []string{"go", "test", "./..."}) {
		t.Fatalf("the repo's test command was lost: %q", command)
	}

	// The owner's own ceiling, set on the machine only.
	cfg.MaxConcurrentCeiling = 3
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, MaxConcurrent: intp(4)}); done.Code != runnerwire.SettingsErrRange ||
		!strings.Contains(done.Error, "1 to 3") || done.MaxConcurrent != 5 {
		t.Fatalf("past the ceiling: %+v", done)
	}
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, MaxConcurrent: intp(1)}); done.Code != "" || done.MaxConcurrent != 1 {
		t.Fatalf("lowering: %+v", done)
	}

	// Turned off on the machine.
	off := false
	cfg.RemoteSettings = &off
	if done := c.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, MaxConcurrent: intp(2)}); done.Code != runnerwire.SettingsErrOff ||
		done.MaxConcurrent != 1 {
		t.Fatalf("remoteSettings off: %+v", done)
	}

	// No settings file: in effect until the runner stops.
	memory := &Client{Config: &Config{}}
	if done := memory.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, MaxConcurrent: intp(4)}); done.Code != "" ||
		done.Persisted || done.Error != "" || memory.MaxConcurrentRuns() != 4 {
		t.Fatalf("no file: %+v", done)
	}

	// A file that cannot be written: applied, not persisted, and said so.
	broken := &Client{Config: &Config{}, ConfigPath: t.TempDir()}
	if done := broken.applySettings(wsOwner, runnerwire.SettingsRequest{ID: "s", AccountID: wsOwner, MaxConcurrent: intp(4)}); done.Code != "" ||
		done.Persisted || !strings.Contains(done.Error, "not saved") || broken.MaxConcurrentRuns() != 4 {
		t.Fatalf("unwritable file: %+v", done)
	}
}

// blockingAccepter keeps every offer waiting for a yes, so offers stay in
// hand and count against the limit without starting anything.
func blockingAccepter(asked chan<- string) acceptFunc {
	return func(ctx context.Context, o Offer) (bool, string) {
		asked <- o.Job.RunID
		<-ctx.Done()
		return false, "stopped"
	}
}

// Over the socket: the hub asks, the runner sends a hello with the new limit
// first, then the answer; offers past the new limit are declined, and a lower
// limit stops nothing already in hand.
func TestSettingsOverTheSocket(t *testing.T) {
	asked := make(chan string, 16)
	path := filepath.Join(t.TempDir(), "runner.json")
	env := startJobRunner(t, func(c *Client) {
		c.Accepter = blockingAccepter(asked)
		c.ConfigPath = path
		if err := SaveConfig(path, c.Config); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.setOwner(wsOwner)
	if hello := env.hub.hello(); hello.Hello.MaxConcurrent != 1 || hello.Hello.MaxConcurrentCeiling != 8 {
		t.Fatalf("hello = %+v", hello.Hello)
	}
	ask := func(id string, n int, account string) runnerwire.SettingsDone {
		t.Helper()
		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest,
			Settings: &runnerwire.SettingsRequest{ID: id, AccountID: account, MaxConcurrent: intp(n)}})
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			for _, msg := range env.hub.messages("") {
				if msg.Type == runnerwire.TypeSettingsDone && msg.SettingsDone != nil && msg.SettingsDone.ID == id {
					return *msg.SettingsDone
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("no settings_done %s", id)
		return runnerwire.SettingsDone{}
	}
	// The hello came before the answer: the hub reads it first.
	helloBefore := func(t *testing.T, want int) {
		t.Helper()
		select {
		case hello := <-env.hub.hellos:
			if hello.Hello.MaxConcurrent != want {
				t.Fatalf("hello before the answer says %d, want %d", hello.Hello.MaxConcurrent, want)
			}
		default:
			t.Fatal("no hello before the answer")
		}
	}

	if done := ask("s-other", 3, "u-other"); done.Code != runnerwire.SettingsErrRefused || done.MaxConcurrent != 1 {
		t.Fatalf("someone else: %+v", done)
	}
	helloBefore(t, 1)
	done := ask("s-1", 3, wsOwner)
	if done.Code != "" || done.MaxConcurrent != 3 || !done.Persisted {
		t.Fatalf("owner: %+v", done)
	}
	helloBefore(t, 3)
	if saved, err := LoadConfig(path); err != nil || saved.MaxConcurrent != 3 {
		t.Fatalf("saved %+v, %v", saved, err)
	}

	for _, id := range []string{"run-c1", "run-c2", "run-c3"} {
		env.hub.offer(testJob(id, "ok:"))
		if got := <-asked; got != id {
			t.Fatalf("asked about %s, offered %s", got, id)
		}
	}
	env.hub.offer(testJob("run-c4", "ok:"))
	if reason := env.hub.wait("run-c4", runnerwire.TypeDecline).Reason; !strings.Contains(reason, "at most 3 at once") {
		t.Fatalf("fourth offer: %q", reason)
	}

	// Lower: what is in hand stays in hand.
	if done := ask("s-2", 1, wsOwner); done.Code != "" || done.MaxConcurrent != 1 {
		t.Fatalf("lowering: %+v", done)
	}
	helloBefore(t, 1)
	env.hub.offer(testJob("run-c5", "ok:"))
	if reason := env.hub.wait("run-c5", runnerwire.TypeDecline).Reason; !strings.Contains(reason, "at most 1 at once") {
		t.Fatalf("offer after lowering: %q", reason)
	}
	for _, id := range []string{"run-c1", "run-c2", "run-c3"} {
		for _, msg := range env.hub.messages(id) {
			if msg.Type == runnerwire.TypeDecline || msg.Type == runnerwire.TypeFinish {
				t.Fatalf("%s ended when the limit went down: %+v", id, msg)
			}
		}
	}
}

// The hello before a settings answer is the last one with the new limit, not
// a fresh look at every agent CLI: probing them all again (a --version and
// a model list each) can take longer than the hub waits for the answer.
func TestSettingsAnswerDoesNotProbeTheAgentsAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	env := startJobRunner(t, func(c *Client) {
		c.ConfigPath = path
		if err := SaveConfig(path, c.Config); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.setOwner(wsOwner)
	first := env.hub.hello()
	cmds := env.client.Commands.(*fakeCommands)
	probes := func() int {
		cmds.mu.Lock()
		defer cmds.mu.Unlock()
		n := 0
		for _, call := range cmds.calls {
			if strings.HasSuffix(call, " --version") {
				n++
			}
		}
		return n
	}
	before := probes()
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest,
		Settings: &runnerwire.SettingsRequest{ID: "s-1", AccountID: wsOwner, MaxConcurrent: intp(3)}})
	var hello runnerwire.Envelope
	select {
	case hello = <-env.hub.hellos:
	case <-time.After(20 * time.Second):
		t.Fatal("no hello after the settings change")
	}
	if hello.Hello.MaxConcurrent != 3 {
		t.Fatalf("hello says %d at once, want 3", hello.Hello.MaxConcurrent)
	}
	if after := probes(); after != before {
		t.Fatalf("the settings change probed the agent CLIs again: %d --version calls, %d before", after, before)
	}
	hello.Hello.MaxConcurrent = first.Hello.MaxConcurrent
	if !reflect.DeepEqual(hello.Hello, first.Hello) {
		t.Fatalf("the hello changed beyond the limit:\n%+v\n%+v", hello.Hello, first.Hello)
	}
}

func TestSettingsCapabilitiesFollowConfig(t *testing.T) {
	on, off := true, false
	cmds := &fakeCommands{paths: map[string]string{}}
	for _, c := range []struct {
		cfg              *Config
		globalOn, remote bool
		ceiling          int
	}{
		{&Config{}, true, true, 8},
		{&Config{MaxConcurrent: 12}, true, true, 12},
		{&Config{MaxConcurrentCeiling: 4}, true, true, 4},
		{&Config{GlobalRuns: &off, RemoteSettings: &on}, false, true, 8},
		{&Config{GlobalRuns: &on, RemoteSettings: &off, MaxConcurrentCeiling: 4}, true, false, 0},
	} {
		info := BuildInfo(context.Background(), cmds, c.cfg)
		caps := info.Capabilities
		if !slices.Contains(caps, runnerwire.CapGlobalRuns) || slices.Contains(caps, runnerwire.CapGlobalRunsOn) != c.globalOn ||
			slices.Contains(caps, runnerwire.CapRemoteSettings) != c.remote || info.MaxConcurrentCeiling != c.ceiling {
			t.Errorf("%+v: caps %q, ceiling %d", c.cfg, caps, info.MaxConcurrentCeiling)
		}
	}
}

// An agent path from the app: the built-in runner's owner only; the file is
// checked and must answer --version before it is kept, as the file it names,
// live and on disk; "" goes back to PATH.
func TestApplySettingsAgentPath(t *testing.T) {
	noAgentTempDir(t)
	t.Setenv("PATH", os.Getenv("PATH")) // whatever syncAgentPathDirs does is undone
	path := filepath.Join(t.TempDir(), "runner.json")
	start := &Config{Hub: "http://127.0.0.1:1", Token: testToken, MaxConcurrent: 2}
	if err := SaveConfig(path, start); err != nil {
		t.Fatal(err)
	}
	added := writeAgentFile(t, filepath.Join(t.TempDir(), "bin"), "claude")
	real := added // the path picked is what is kept and run
	var pathAtVersion []string
	cmds := &fakeCommands{paths: map[string]string{}, outputs: map[string]string{real + " --version": "2.1.0 (Claude Code)\n"},
		errs: map[string]error{}, onRun: func(key string) {
			if key == real+" --version" {
				pathAtVersion = filepath.SplitList(os.Getenv("PATH"))
			}
		}}
	c := &Client{Config: start, ConfigPath: path, Commands: cmds, Log: &bytes.Buffer{}}
	on := true
	set := func(kind, p string) runnerwire.SettingsRequest {
		return runnerwire.SettingsRequest{ID: "a", AccountID: wsOwner, AgentPath: map[string]string{kind: p}}
	}

	// A classic runner takes neither from the hub.
	for name, req := range map[string]runnerwire.SettingsRequest{
		"agentPath":       set("claude", added),
		"permissionRelay": {ID: "r", AccountID: wsOwner, PermissionRelay: &on},
	} {
		if done := c.applySettings(wsOwner, req); done.Code != runnerwire.SettingsErrLocalOnly || c.cfg() != start {
			t.Fatalf("classic %s: %+v", name, done)
		}
	}
	c.builtIn.Store(true)
	if done := c.applySettings("u-other", set("claude", added)); done.Code != runnerwire.SettingsErrRefused || c.cfg() != start {
		t.Fatalf("someone else: %+v", done)
	}
	two := runnerwire.SettingsRequest{ID: "b", AccountID: wsOwner, AgentPath: map[string]string{"claude": added, "codex": added}}
	if done := c.applySettings(wsOwner, two); done.Code != runnerwire.SettingsErrInvalid || done.Refused != runnerwire.AgentPathRefusedInvalid {
		t.Fatalf("two at once: %+v", done)
	}
	if done := c.applySettings(wsOwner, set("nope", added)); done.Code != runnerwire.SettingsErrInvalid || done.Refused != runnerwire.AgentPathRefusedInvalid {
		t.Fatalf("unknown kind: %+v", done)
	}
	if done := c.applySettings(wsOwner, set("codex", added)); done.Code != runnerwire.SettingsErrInvalid || done.Refused != runnerwire.AgentPathRefusedName {
		t.Fatalf("claude's file as codex: %+v", done)
	}
	cmds.mu.Lock()
	cmds.errs[real+" --version"] = errors.New("exit status 1")
	cmds.mu.Unlock()
	done := c.applySettings(wsOwner, set("claude", added))
	if done.Code != runnerwire.SettingsErrInvalid || done.Refused != runnerwire.AgentPathRefusedNoAnswer || c.cfg().AgentPaths != nil {
		t.Fatalf("no answer: %+v, %v", done, c.cfg().AgentPaths)
	}
	if strings.Contains(done.Error, filepath.Dir(real)) {
		t.Fatalf("the refusal names the folder: %q", done.Error)
	}
	// --version ran with the program's folder on PATH (node beside an npm
	// CLI, from a desktop launch), and a refused program's folder is taken
	// off again.
	if !slices.Contains(pathAtVersion, filepath.Dir(real)) {
		t.Fatalf("--version ran without the program's folder on PATH: %q", pathAtVersion)
	}
	if slices.Contains(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(real)) {
		t.Fatalf("a refused program's folder stayed on PATH: %q", os.Getenv("PATH"))
	}
	cmds.mu.Lock()
	delete(cmds.errs, real+" --version")
	cmds.mu.Unlock()

	done = c.applySettings(wsOwner, set("claude", added))
	if done.Code != "" || done.Refused != "" || !done.Persisted || c.cfg().AgentPaths["claude"] != real {
		t.Fatalf("added: %+v, %v", done, c.cfg().AgentPaths)
	}
	saved, err := LoadConfig(path)
	if err != nil || saved.AgentPaths["claude"] != real || saved.Token != testToken || saved.MaxConcurrent != 2 {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if local := localSettings(c.cfg(), path); local.AgentPaths["claude"] != real {
		t.Fatalf("local %+v", local.AgentPaths)
	}
	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(real)) {
		t.Fatalf("the added program's folder is not on PATH: %q", os.Getenv("PATH"))
	}
	if start.AgentPaths != nil {
		t.Fatal("the starting config changed")
	}

	if done := c.applySettings(wsOwner, set("claude", "")); done.Code != "" || c.cfg().AgentPaths != nil {
		t.Fatalf("cleared: %+v, %v", done, c.cfg().AgentPaths)
	}
	if saved, err := LoadConfig(path); err != nil || saved.AgentPaths != nil {
		t.Fatalf("cleared on disk: %+v, %v", saved.AgentPaths, err)
	}
	if slices.Contains(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(real)) {
		t.Fatalf("the folder stayed on PATH: %q", os.Getenv("PATH"))
	}
}

func TestSetGlobalAndRemoteSettings(t *testing.T) {
	var cfg Config
	if !cfg.GlobalRunsOn() || !cfg.RemoteSettingsOn() || cfg.MaxConcurrentCeilingOrDefault() != 8 {
		t.Fatal("defaults")
	}
	for _, step := range []struct {
		key, value string
		check      func() bool
	}{
		{"globalRuns", "off", func() bool { return !cfg.GlobalRunsOn() && cfg.GlobalRuns != nil }},
		{"globalruns", "on", func() bool { return cfg.GlobalRunsOn() && cfg.GlobalRuns != nil }},
		{"globalRuns", "default", func() bool { return cfg.GlobalRuns == nil }},
		{"remoteSettings", "false", func() bool { return !cfg.RemoteSettingsOn() }},
		{"remoteSettings", "default", func() bool { return cfg.RemoteSettings == nil && cfg.RemoteSettingsOn() }},
		{"maxConcurrentCeiling", "16", func() bool { return cfg.MaxConcurrentCeilingOrDefault() == 16 }},
		{"maxConcurrentCeiling", "1", func() bool { return cfg.MaxConcurrentCeilingOrDefault() == 1 }},
		{"maxConcurrentCeiling", "default", func() bool { return cfg.MaxConcurrentCeiling == 0 }},
	} {
		if err := cfg.Set(step.key, []string{step.value}); err != nil || !step.check() {
			t.Errorf("set %s %s: %v, %+v", step.key, step.value, err, cfg)
		}
	}
	for key, value := range map[string]string{"globalRuns": "maybe", "remoteSettings": "2",
		"maxConcurrentCeiling": "17"} {
		if err := cfg.Set(key, []string{value}); err == nil {
			t.Errorf("set %s %s: no error", key, value)
		}
	}
	for _, value := range []string{"0", "x"} {
		if err := cfg.Set("maxConcurrentCeiling", []string{value}); err == nil {
			t.Errorf("maxConcurrentCeiling %s: no error", value)
		}
	}
	// runner.json round trip.
	path := filepath.Join(t.TempDir(), "runner.json")
	no := false
	saved := &Config{GlobalRuns: &no, RemoteSettings: &no, MaxConcurrentCeiling: 6}
	if err := SaveConfig(path, saved); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	loaded, err := LoadConfig(path)
	if err != nil || loaded.GlobalRunsOn() || loaded.RemoteSettingsOn() || loaded.MaxConcurrentCeiling != 6 ||
		!strings.Contains(string(raw), `"globalRuns": false`) {
		t.Fatalf("loaded %+v, %v:\n%s", loaded, err, raw)
	}
}
