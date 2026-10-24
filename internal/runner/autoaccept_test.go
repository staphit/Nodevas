package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyAutoAcceptMigration(t *testing.T) {
	path := writeConfig(t, `{"hub":"https://hub.example","autoAccept":true,
		"repos":[{"id":"r1","name":"one","path":"/src/one"},{"id":"r2","name":"two","path":"/src/two"}]}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LegacyAutoAccept() {
		t.Fatal("legacy true not flagged for migration")
	}
	want := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: false,
		Repos: []string{"r1", "r2"}, Modes: []string{runnerwire.ModeHeadless}}
	if got := cfg.AutoAcceptPolicy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("before migration: %+v", got)
	}
	if warning := cfg.MigrateLegacyAutoAccept([]string{"claude", "codex"}); warning != LegacyAutoAcceptWarning ||
		!strings.Contains(warning, "step-up required") {
		t.Fatalf("warning %q", warning)
	}
	if warning := cfg.MigrateLegacyAutoAccept([]string{"claude"}); warning != "" || cfg.LegacyAutoAccept() {
		t.Fatalf("second migration warned again: %q", warning)
	}
	want.Agents = []string{"claude", "codex"}
	if got := cfg.AutoAcceptPolicy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after migration: %+v", got)
	}

	// The next save writes the new shape, which loads as itself.
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"requireStepUp": false`)) || bytes.Contains(raw, []byte(`"autoAccept": true`)) {
		t.Fatalf("saved:\n%s", raw)
	}
	again, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.LegacyAutoAccept() || !reflect.DeepEqual(again.AutoAcceptPolicy(), want) || again.Hub != "https://hub.example" ||
		len(again.Repos) != 2 {
		t.Fatalf("reloaded: %+v %+v", again, again.AutoAcceptPolicy())
	}

	// false and absent are off; garbage is an error.
	for _, body := range []string{`{"autoAccept":false}`, `{}`, `{"autoAccept":null}`} {
		cfg, err := LoadConfig(writeConfig(t, body))
		if err != nil || cfg.AutoAccept != nil || cfg.LegacyAutoAccept() || cfg.AutoAcceptOn() ||
			!cfg.AutoAcceptPolicy().RequireStepUp {
			t.Fatalf("%s: %+v %v", body, cfg, err)
		}
	}
	if _, err := LoadConfig(writeConfig(t, `{"autoAccept":3}`)); err == nil {
		t.Fatal("autoAccept 3 loaded")
	}
}

func TestAutoAcceptDecision(t *testing.T) {
	full := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
		Repos: []string{"r1"}, Agents: []string{"claude"}}
	offer := func(mut func(*runnerwire.Job)) Offer {
		job := testJob("run-1", "x")
		job.SteppedUp = true
		if mut != nil {
			mut(&job)
		}
		return Offer{Job: job, Repo: runnerwire.Repo{ID: job.RepoID, Name: "demo"}}
	}
	cases := []struct {
		name   string
		policy func(*runnerwire.AutoAcceptPolicy)
		job    func(*runnerwire.Job)
		ok     bool
		reason string
	}{
		{name: "covered", ok: true, reason: "dispatcher stepped up"},
		{name: "off", policy: func(p *runnerwire.AutoAcceptPolicy) { p.Enabled = false }, reason: ReasonAutoAcceptOff},
		{name: "no step-up", job: func(j *runnerwire.Job) { j.SteppedUp = false }, reason: ReasonNeedsStepUp},
		{name: "step-up optional", policy: func(p *runnerwire.AutoAcceptPolicy) { p.RequireStepUp = false },
			job: func(j *runnerwire.Job) { j.SteppedUp = false }, ok: true, reason: "step-up not required"},
		{name: "repo", job: func(j *runnerwire.Job) { j.RepoID = "r2" },
			reason: "repo demo is not on this runner's auto-accept list"},
		{name: "agent", job: func(j *runnerwire.Job) { j.Agent = "codex" },
			reason: "agent codex is not on this runner's auto-accept list"},
		{name: "interactive, modes empty", job: func(j *runnerwire.Job) { j.Mode = runnerwire.ModeInteractive },
			reason: "interactive runs are not on this runner's auto-accept list"},
		{name: "interactive listed", policy: func(p *runnerwire.AutoAcceptPolicy) {
			p.Modes = []string{runnerwire.ModeHeadless, runnerwire.ModeInteractive}
		}, job: func(j *runnerwire.Job) { j.Mode = runnerwire.ModeInteractive }, ok: true},
		{name: "interactive only", policy: func(p *runnerwire.AutoAcceptPolicy) {
			p.Modes = []string{runnerwire.ModeInteractive}
		}, reason: "headless runs are not on this runner's auto-accept list"},
		{name: "review kind", job: func(j *runnerwire.Job) { j.Kind = runnerwire.JobReview }, ok: true},
		{name: "revise kind, no step-up", job: func(j *runnerwire.Job) { j.Kind = runnerwire.JobRevise; j.SteppedUp = false },
			reason: ReasonNeedsStepUp},
		{name: "empty lists", policy: func(p *runnerwire.AutoAcceptPolicy) { p.Repos, p.Agents = nil, nil },
			reason: "repo demo is not"},
	}
	for _, c := range cases {
		p := full
		if c.policy != nil {
			c.policy(&p)
		}
		ok, reason := AutoAcceptDecision(p, offer(c.job))
		if ok != c.ok || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: = %v, %q; want %v, %q", c.name, ok, reason, c.ok, c.reason)
		}
	}
	// Every failing condition is named.
	ok, reason := AutoAcceptDecision(full, offer(func(j *runnerwire.Job) {
		j.SteppedUp, j.RepoID, j.Agent = false, "r9", "gemini"
	}))
	if ok || !strings.Contains(reason, ReasonNeedsStepUp) || !strings.Contains(reason, "repo demo") ||
		!strings.Contains(reason, "agent gemini") {
		t.Fatalf("all missing: %v %q", ok, reason)
	}
}

// TestAskFallsBack drives jobs.ask: auto-accept first, the terminal next,
// and with nobody there a decline that names what auto-accept lacked.
func TestAskFallsBack(t *testing.T) {
	policy := &runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
		Repos: []string{"r1"}, Agents: []string{"claude"}}
	names := map[string]string{"r1": "demo", "r2": "other"}
	job := func(steppedUp bool, repoID string) *run {
		j := testJob("run-1", "x")
		j.SteppedUp, j.RepoID = steppedUp, repoID
		return &run{job: j, repo: runnerwire.Repo{ID: repoID, Name: names[repoID]}}
	}
	var log bytes.Buffer
	asked := 0
	yes := acceptFunc(func(context.Context, Offer) (bool, string) { asked++; return true, "" })
	noTerminal := &TerminalAccepter{In: strings.NewReader(""), Out: io.Discard}
	cases := []struct {
		name     string
		policy   *runnerwire.AutoAcceptPolicy
		accepter Accepter
		run      *run
		ok       bool
		asked    int
		reason   string
		logged   string
	}{
		{"auto", policy, yes, job(true, "r1"), true, 0, "", "auto-accepted: dispatcher stepped up"},
		{"no step-up, asked", policy, yes, job(false, "r1"), true, 1, "", "not auto-accepted: " + ReasonNeedsStepUp},
		{"no step-up, nobody", policy, noTerminal, job(false, "r1"), false, 0,
			ReasonNeedsStepUp + " (and nobody is at the runner's terminal to accept)", ""},
		{"repo, nobody", policy, noTerminal, job(true, "r2"), false, 0,
			"repo other is not on this runner's auto-accept list (and nobody", ""},
		{"repo, no accepter", policy, nil, job(true, "r2"), false, 0, "repo other is not", ""},
		{"off, nobody", nil, noTerminal, job(true, "r1"), false, 0, ReasonNoTerminal, "not auto-accepted: " + ReasonAutoAcceptOff},
		{"declined at terminal", policy, acceptFunc(func(context.Context, Offer) (bool, string) {
			return false, ReasonDeclined
		}), job(false, "r1"), false, 0, ReasonDeclined, ""},
	}
	for _, c := range cases {
		asked = 0
		log.Reset()
		j := &jobs{c: &Client{Config: &Config{AutoAccept: c.policy}, Accepter: c.accepter, Log: &log}}
		ok, reason := j.ask(context.Background(), c.run)
		if ok != c.ok || asked != c.asked || !strings.HasPrefix(reason, c.reason) || (c.reason == "" && reason != "") {
			t.Errorf("%s: = %v, %q (asked %d)", c.name, ok, reason, asked)
		}
		if !strings.Contains(log.String(), c.logged) {
			t.Errorf("%s: log %q, want %q", c.name, log.String(), c.logged)
		}
	}
}

func TestAutoAcceptEdits(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Repos: []runnerwire.Repo{
		{ID: "r1", Name: "one", Path: "/src/one"},
		{ID: RepoID(dir), Name: "Two", Path: dir},
		{ID: "r3", Name: "dup", Path: "/a/dup"},
		{ID: "r4", Name: "dup", Path: "/b/dup"},
	}}
	if cfg.AutoAcceptOn() {
		t.Fatal("on by default")
	}
	// On from nothing: the defaults.
	cfg.EnableAutoAccept([]string{"claude"})
	want := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
		Repos: []string{"r1", RepoID(dir), "r3", "r4"}, Agents: []string{"claude"}, Modes: []string{""}}
	if got := cfg.AutoAcceptPolicy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("on: %+v", got)
	}
	// By id, name (any case) and path; a shared name is refused.
	for _, arg := range []string{"r1", "two"} {
		if _, err := cfg.RemoveAutoAcceptRepo(arg); err != nil {
			t.Fatalf("remove %s: %v", arg, err)
		}
	}
	if _, err := cfg.RemoveAutoAcceptRepo("r1"); err == nil {
		t.Fatal("removed r1 twice")
	}
	if _, err := cfg.AddAutoAcceptRepo("dup"); err == nil {
		t.Fatal("ambiguous name accepted")
	}
	if _, err := cfg.AddAutoAcceptRepo("/not/a/repo"); err == nil {
		t.Fatal("unknown path accepted")
	}
	for i := 0; i < 2; i++ {
		if repo, err := cfg.AddAutoAcceptRepo(dir); err != nil || repo.Name != "Two" {
			t.Fatalf("add by path: %+v %v", repo, err)
		}
		if i == 0 {
			if _, err := cfg.RemoveAutoAcceptRepo(dir); err != nil {
				t.Fatalf("remove by path: %v", err)
			}
		}
	}
	// Off keeps the lists; on again keeps them too.
	cfg.SetAutoAccept(false, nil)
	cfg.EnableAutoAccept([]string{"codex"})
	if p := cfg.AutoAcceptPolicy(); !p.Enabled || !slices.Equal(p.Repos, []string{"r3", "r4", RepoID(dir)}) ||
		!slices.Equal(p.Agents, []string{"claude"}) {
		t.Fatalf("off/on: %+v", p)
	}
	// Removing a repository from the runner takes it off the list.
	if _, err := cfg.RemoveRepo("r3"); err != nil || slices.Contains(cfg.AutoAccept.Repos, "r3") {
		t.Fatalf("RemoveRepo kept it listed: %v %v", cfg.AutoAccept.Repos, err)
	}

	if kind, err := cfg.AddAutoAcceptAgent("CODEX"); err != nil || kind != "codex" {
		t.Fatalf("agent add: %q %v", kind, err)
	}
	if _, err := cfg.AddAutoAcceptAgent("bash"); err == nil {
		t.Fatal("unknown agent accepted")
	}
	if _, err := cfg.RemoveAutoAcceptAgent("claude"); err != nil || !slices.Equal(cfg.AutoAccept.Agents, []string{"codex"}) {
		t.Fatalf("agent remove: %v %v", cfg.AutoAccept.Agents, err)
	}

	if _, err := cfg.AddAutoAcceptMode("interactive"); err != nil ||
		!slices.Equal(AutoAcceptModeNames(cfg.AutoAcceptPolicy()), []string{"headless", "interactive"}) {
		t.Fatalf("mode add: %v %v", cfg.AutoAccept.Modes, err)
	}
	if _, err := cfg.RemoveAutoAcceptMode("headless"); err != nil ||
		!slices.Equal(cfg.AutoAccept.Modes, []string{runnerwire.ModeInteractive}) {
		t.Fatalf("mode remove: %v %v", cfg.AutoAccept.Modes, err)
	}
	if _, err := cfg.RemoveAutoAcceptMode("interactive"); err == nil {
		t.Fatal("removed the last mode")
	}
	if _, err := cfg.AddAutoAcceptMode("tmux"); err == nil {
		t.Fatal("unknown mode accepted")
	}

	cfg.SetStepUpRequired(false)
	if cfg.AutoAcceptPolicy().RequireStepUp {
		t.Fatal("step-up still required")
	}
	// `set autoAccept true` resets to the defaults.
	cfg.SetAutoAccept(true, []string{"claude"})
	if p := cfg.AutoAcceptPolicy(); !p.RequireStepUp || !slices.Equal(p.Agents, []string{"claude"}) ||
		len(p.Repos) != len(cfg.Repos) || !slices.Equal(p.Modes, []string{""}) {
		t.Fatalf("set true: %+v", p)
	}
	// The policy handed out is a copy.
	p := cfg.AutoAcceptPolicy()
	p.Repos[0] = "changed"
	if cfg.AutoAccept.Repos[0] == "changed" {
		t.Fatal("AutoAcceptPolicy shares its lists")
	}
}

func TestBuildInfoReportsPolicy(t *testing.T) {
	cmds := &fakeCommands{}
	if info := BuildInfo(context.Background(), cmds, &Config{}); info.AutoAccept == nil ||
		info.AutoAccept.Enabled || !info.AutoAccept.RequireStepUp {
		t.Fatalf("default policy = %+v", info.AutoAccept)
	}
	policy := runnerwire.AutoAcceptPolicy{Enabled: true, Repos: []string{"r1"}, Agents: []string{"codex"},
		Modes: []string{runnerwire.ModeInteractive}}
	if info := BuildInfo(context.Background(), cmds, &Config{AutoAccept: &policy}); info.AutoAccept == nil ||
		!reflect.DeepEqual(*info.AutoAccept, policy) {
		t.Fatalf("policy = %+v", info.AutoAccept)
	}
}

func TestShellAccess(t *testing.T) {
	job := func(agent, kind string) runnerwire.Job {
		j := testJob("run-1", "x")
		j.Agent, j.Kind = agent, kind
		return j
	}
	cases := []struct {
		name string
		cfg  Config
		job  runnerwire.Job
		want string
	}{
		{"claude default", Config{}, job("claude", ""), ""},
		{"claude bypass", Config{PermissionMode: "bypassPermissions"}, job("claude", ""), "bypassPermissions"},
		{"claude Bash", Config{AllowedTools: []string{"Edit", "Bash"}}, job("claude", ""), "allowedTools Bash"},
		{"claude Bash pattern", Config{AllowedTools: []string{"Bash(go test *)"}}, job("claude", ""), "Bash(go test *)"},
		{"claude BashOutput is not Bash", Config{AllowedTools: []string{"BashOutput"}}, job("claude", ""), ""},
		{"claude review, Bash allowed", Config{AllowedTools: []string{"Bash"}}, job("claude", runnerwire.JobReview), ""},
		{"codex default", Config{}, job("codex", ""), ""},
		{"codex full", Config{CodexSandbox: "danger-full-access"}, job("codex", ""), "danger-full-access"},
		{"codex full review", Config{CodexSandbox: "danger-full-access"}, job("codex", runnerwire.JobReview), ""},
		{"gemini yolo", Config{GeminiApprovalMode: "yolo"}, job("gemini", ""), "yolo"},
		{"opencode full", Config{OpenCodePermission: "full"}, job("opencode", ""), "full"},
		{"opencode full plan", Config{OpenCodePermission: "full"}, job("opencode", runnerwire.JobPlan), ""},
		{"agy full", Config{AntigravityMode: "full"}, job("antigravity", ""), "full"},
		{"agy auto", Config{AntigravityMode: "auto"}, job("antigravity", ""), "auto"},
		{"agy default", Config{}, job("antigravity", ""), ""},
	}
	for _, c := range cases {
		got := c.cfg.ShellAccess(c.job)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: ShellAccess = %q, want %q", c.name, got, c.want)
		}
	}

	policy := runnerwire.AutoAcceptPolicy{Enabled: true, Repos: []string{"r1"}, Agents: []string{"codex"}}
	offer := Offer{Job: job("codex", ""), Repo: runnerwire.Repo{ID: "r1", Name: "demo"}, Shell: "codexSandbox danger-full-access"}
	offer.Job.RepoID = "r1"
	if ok, reason := AutoAcceptDecision(policy, offer); ok || !strings.Contains(reason, "allow-shell on") {
		t.Fatalf("shell-capable offer: %v %q", ok, reason)
	}
	policy.AllowShell = true
	if ok, reason := AutoAcceptDecision(policy, offer); !ok {
		t.Fatalf("allowShell on: %q", reason)
	}
}

// An auto-accepted interactive run is view only unless the policy allows
// remote typing.
func TestAskRemoteTyping(t *testing.T) {
	for _, allow := range []bool{false, true} {
		policy := &runnerwire.AutoAcceptPolicy{Enabled: true, Repos: []string{"r1"}, Agents: []string{"claude"},
			Modes: []string{runnerwire.ModeInteractive}, AllowRemoteTyping: allow}
		job := testJob("run-1", "x")
		job.RepoID, job.Mode = "r1", runnerwire.ModeInteractive
		r := &run{job: job, repo: runnerwire.Repo{ID: "r1", Name: "demo"}}
		var log bytes.Buffer
		j := &jobs{c: &Client{Config: &Config{AutoAccept: policy}, Log: &log}}
		if ok, reason := j.ask(context.Background(), r); !ok {
			t.Fatalf("not auto-accepted: %q", reason)
		}
		if r.remoteTyping != allow {
			t.Fatalf("allowRemoteTyping %t: run typing %t", allow, r.remoteTyping)
		}
	}
}
