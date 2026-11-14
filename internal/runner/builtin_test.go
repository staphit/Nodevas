package runner

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// A runner is the built-in one only when serve started it for a
// single-user hub, its hub is plain http on loopback, and the hub's welcome
// says so; any one missing leaves it classic, and the hub's word alone
// never makes it built-in.
func TestBuiltInDetection(t *testing.T) {
	confirm := &runnerwire.Welcome{Edition: runnerwire.EditionSingle, BuiltIn: true}
	cases := []struct {
		name    string
		serve   bool
		edition string
		hub     string
		welcome *runnerwire.Welcome
		want    bool
		logged  string
	}{
		{"all three", true, runnerwire.EditionSingle, "http://127.0.0.1:8080", confirm, true, "acting as this computer's built-in runner"},
		{"localhost", true, runnerwire.EditionSingle, "http://localhost:8080", confirm, true, ""},
		{"not started by serve", false, runnerwire.EditionSingle, "http://127.0.0.1:8080", confirm, false, "nodevas serve did not start it"},
		{"no edition", true, "", "http://127.0.0.1:8080", confirm, false, "did not start it for a single-user hub"},
		{"team edition env", true, runnerwire.EditionTeam, "http://127.0.0.1:8080", confirm, false, "single-user hub"},
		{"https hub", true, runnerwire.EditionSingle, "https://hub.example", confirm, false, "not plain http on this machine"},
		{"https loopback", true, runnerwire.EditionSingle, "https://127.0.0.1:8080", confirm, false, "not plain http"},
		{"hub did not confirm", true, runnerwire.EditionSingle, "http://127.0.0.1:8080",
			&runnerwire.Welcome{Edition: runnerwire.EditionSingle}, false, "the hub did not confirm"},
		{"team hub", true, runnerwire.EditionSingle, "http://127.0.0.1:8080",
			&runnerwire.Welcome{Edition: runnerwire.EditionTeam, BuiltIn: true}, false, "the hub did not confirm (edition team"},
		{"older hub", true, runnerwire.EditionSingle, "http://127.0.0.1:8080", &runnerwire.Welcome{}, false, "no edition"},
		{"no welcome body", true, runnerwire.EditionSingle, "http://127.0.0.1:8080", nil, false, "the hub did not confirm"},
	}
	for _, c := range cases {
		var log bytes.Buffer
		client := &Client{Config: &Config{Hub: c.hub}, ServeManaged: c.serve, Edition: c.edition, Log: &log}
		if got := client.decideBuiltIn(c.welcome); got != c.want || client.BuiltIn() != c.want {
			t.Errorf("%s: built-in %v, want %v", c.name, got, c.want)
		}
		if c.want {
			if c.logged != "" && !strings.Contains(log.String(), c.logged) {
				t.Errorf("%s: log %q", c.name, log.String())
			}
			continue
		}
		if c.welcome != nil && c.welcome.BuiltIn && !strings.Contains(log.String(), c.logged) {
			// The hub claims it, the runner disagrees, and says why.
			t.Errorf("%s: log %q, want %q", c.name, log.String(), c.logged)
		}
		if c.name == "hub did not confirm" && !strings.Contains(log.String(), c.logged) {
			t.Errorf("%s: log %q", c.name, log.String())
		}
	}
	// A reconnect with the same answer says nothing again.
	var log bytes.Buffer
	client := &Client{Config: &Config{Hub: "http://127.0.0.1:1"}, ServeManaged: true, Edition: runnerwire.EditionSingle, Log: &log}
	client.decideBuiltIn(confirm)
	client.decideBuiltIn(confirm)
	if n := strings.Count(log.String(), "built-in runner"); n != 1 {
		t.Fatalf("said %d times: %q", n, log.String())
	}
}

// What the built-in runner starts right away and what it asks about first.
func TestBuiltInDecision(t *testing.T) {
	full := &Config{CodexSandbox: "danger-full-access"}
	job := func(agent, origin string, edit func(*runnerwire.Job)) runnerwire.Job {
		j := testJob("run-1", "x")
		j.Agent, j.Origin = agent, origin
		if edit != nil {
			edit(&j)
		}
		return j
	}
	cases := []struct {
		name    string
		cfg     *Config
		job     runnerwire.Job
		start   bool
		reasons []string
	}{
		{"defaults, by hand", &Config{}, job("codex", runnerwire.OriginPerson, nil), true, nil},
		{"defaults, autopilot", &Config{}, job("codex", runnerwire.OriginAutopilot, nil), true, nil},
		{"full access, by hand", full, job("codex", runnerwire.OriginPerson, nil), true, nil},
		{"full access, autopilot", full, job("codex", runnerwire.OriginAutopilot, nil), false, []string{runnerwire.AskShellAgent}},
		{"full access, review round", full, job("codex", runnerwire.OriginPipeline, nil), false, []string{runnerwire.AskShellAgent}},
		{"full access, continuation", full, job("codex", runnerwire.OriginHandoff, nil), false, []string{runnerwire.AskShellAgent}},
		{"full access, origin unknown", full, job("codex", "", nil), false, []string{runnerwire.AskShellAgent}},
		{"full access, other agent", full, job("claude", runnerwire.OriginAutopilot, nil), true, nil},
		{"don't ask again", &Config{CodexSandbox: "danger-full-access", ShellWithoutAsking: []string{"codex"}},
			job("codex", runnerwire.OriginAutopilot, nil), true, nil},
		{"claude with Bash", &Config{AllowedTools: []string{"Bash(go test *)"}}, job("claude", runnerwire.OriginAutopilot, nil),
			false, []string{runnerwire.AskShellAgent}},
		{"ask every run", &Config{AskEveryRun: true}, job("codex", runnerwire.OriginPerson, nil), false, []string{runnerwire.AskEveryRun}},
		{"both", &Config{AskEveryRun: true, CodexSandbox: "danger-full-access"}, job("codex", runnerwire.OriginAutopilot, nil),
			false, []string{runnerwire.AskEveryRun, runnerwire.AskShellAgent}},
		{"interactive", &Config{}, job("claude", runnerwire.OriginPerson, func(j *runnerwire.Job) { j.Mode = runnerwire.ModeInteractive }), true, nil},
		{"verify with a test command", &Config{}, job("", runnerwire.OriginPipeline, func(j *runnerwire.Job) {
			j.Kind, j.Verify = runnerwire.JobVerify, &runnerwire.VerifySpec{Phase: runnerwire.VerifyGreen}
		}), true, nil},
		{"read-only review of a full-access agent", full, job("codex", runnerwire.OriginPipeline, func(j *runnerwire.Job) {
			j.Kind = runnerwire.JobReview
		}), true, nil},
	}
	for _, c := range cases {
		offer := Offer{Job: c.job, Shell: c.cfg.ShellAccess(c.job)}
		start, reasons := BuiltInDecision(c.cfg, offer)
		var codes []string
		for _, r := range reasons {
			codes = append(codes, r.Code)
		}
		if start != c.start || !slices.Equal(codes, c.reasons) {
			t.Errorf("%s: start %v, reasons %v; want %v, %v", c.name, start, codes, c.start, c.reasons)
		}
	}
	_, reasons := BuiltInDecision(full, Offer{Job: job("codex", runnerwire.OriginAutopilot, nil), Shell: full.ShellAccess(job("codex", "", nil))})
	if len(reasons) != 1 || reasons[0].Agent != "codex" || reasons[0].Setting != "codexSandbox=danger-full-access" {
		t.Fatalf("shell reason = %+v", reasons)
	}
}

// The app's answer: the first one wins, an unknown run's is dropped, no is
// declined_in_app, silence is timed_out, and a cancel ends the wait.
func TestAppAccepter(t *testing.T) {
	a := &AppAccepter{}
	if a.Answer(runnerwire.OfferAnswer{RunID: "nobody", Start: true}) {
		t.Fatal("an answer for a run nobody waits on was taken")
	}
	offer := Offer{Job: testJob("run-a", "x")}
	far := time.Now().Add(time.Hour)

	answers := a.open("run-a")
	if !a.Answer(runnerwire.OfferAnswer{RunID: "run-a", Start: true, AlwaysAllowShell: true}) {
		t.Fatal("the answer was not taken")
	}
	if a.Answer(runnerwire.OfferAnswer{RunID: "run-a", Start: false}) {
		t.Fatal("a second answer was taken")
	}
	if d := a.wait(context.Background(), offer, answers, far, false); !d.start || !d.alwaysAllowShell {
		t.Fatalf("start: %+v", d)
	}
	a.closeAsk("run-a")

	answers = a.open("run-b")
	a.Answer(runnerwire.OfferAnswer{RunID: "run-b"})
	if d := a.wait(context.Background(), offer, answers, far, false); d.start || d.code != runnerwire.DeclineInApp || d.reason == "" {
		t.Fatalf("no: %+v", d)
	}
	a.closeAsk("run-b")

	answers = a.open("run-c")
	if d := a.wait(context.Background(), offer, answers, time.Now().Add(30*time.Millisecond), false); d.start || d.code != runnerwire.DeclineTimedOut {
		t.Fatalf("timeout: %+v", d)
	}
	a.closeAsk("run-c")
	if a.Answer(runnerwire.OfferAnswer{RunID: "run-c", Start: true}) {
		t.Fatal("an answer after the ask closed was taken")
	}

	answers = a.open("run-d")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d := a.wait(ctx, offer, answers, far, false); d.start || d.code != "" {
		t.Fatalf("cancelled: %+v", d)
	}
	a.closeAsk("run-d")

	if (&AppAccepter{}).timeout() != 30*time.Minute {
		t.Fatal("the app waits 30 minutes by default")
	}
}

// When the owner asked to confirm every run, serve's terminal answers too:
// the first answer wins; the terminal's own timeout leaves the app asking.
func TestAppAccepterRacesTheTerminal(t *testing.T) {
	offer := Offer{Job: testJob("run-t", "x")}
	far := time.Now().Add(time.Hour)

	yes := &AppAccepter{Terminal: acceptFunc(func(context.Context, Offer) (bool, string) { return true, "" })}
	if d := yes.wait(context.Background(), offer, yes.open("run-t"), far, true); !d.start {
		t.Fatalf("terminal yes: %+v", d)
	}
	no := &AppAccepter{Terminal: acceptFunc(func(context.Context, Offer) (bool, string) { return false, ReasonDeclined })}
	if d := no.wait(context.Background(), offer, no.open("run-t"), far, true); d.start || d.code != runnerwire.DeclineOnRunner {
		t.Fatalf("terminal no: %+v", d)
	}
	// The terminal gave up on its own; the app still decides.
	gaveUp := &AppAccepter{Terminal: acceptFunc(func(context.Context, Offer) (bool, string) { return false, ReasonTimedOut })}
	answers := gaveUp.open("run-t")
	go func() {
		time.Sleep(20 * time.Millisecond)
		gaveUp.Answer(runnerwire.OfferAnswer{RunID: "run-t", Start: true})
	}()
	if d := gaveUp.wait(context.Background(), offer, answers, far, true); !d.start {
		t.Fatalf("app after the terminal gave up: %+v", d)
	}
	// The app first: the terminal's question ends, saying why.
	var mu sync.Mutex
	var cause error
	ended := make(chan struct{})
	waiting := &AppAccepter{Terminal: acceptFunc(func(ctx context.Context, _ Offer) (bool, string) {
		<-ctx.Done()
		mu.Lock()
		cause = context.Cause(ctx)
		mu.Unlock()
		close(ended)
		return false, ReasonCancelled
	})}
	answers = waiting.open("run-t")
	waiting.Answer(runnerwire.OfferAnswer{RunID: "run-t", Start: true})
	if d := waiting.wait(context.Background(), offer, answers, far, true); !d.start {
		t.Fatalf("app first: %+v", d)
	}
	<-ended
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(cause, errAnsweredInApp) {
		t.Fatalf("terminal question ended by %v", cause)
	}
	// Without the terminal flag the terminal is never asked.
	asked := false
	quiet := &AppAccepter{Terminal: acceptFunc(func(context.Context, Offer) (bool, string) { asked = true; return true, "" })}
	if d := quiet.wait(context.Background(), offer, quiet.open("run-t"), time.Now().Add(20*time.Millisecond), false); d.start || asked {
		t.Fatalf("asked the terminal without askEveryRun: %+v %v", d, asked)
	}
}

// startBuiltInRunner is startJobRunner for the built-in runner of a
// single-user hub: serve's edition, and a welcome that confirms it. The
// hello before that first welcome claims nothing: the runner is never the
// built-in one on its own word. The hello the welcome brings is the
// caller's to read.
func startBuiltInRunner(t *testing.T, tweak func(*Client)) *jobEnv {
	t.Helper()
	env := startJobRunnerWith(t, func(h *jobHub) {
		h.owner, h.edition, h.builtIn = wsOwner, runnerwire.EditionSingle, true
	}, func(c *Client) {
		c.Edition = runnerwire.EditionSingle
		c.Config.MaxConcurrent = 4
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) {
			t.Error("the built-in runner asked its terminal accepter")
			return false, "no"
		})
		if tweak != nil {
			tweak(c)
		}
	})
	if first := env.hub.hello().Hello; first == nil || slices.Contains(first.Capabilities, runnerwire.CapAppAccept) ||
		slices.Contains(first.Capabilities, runnerwire.CapRepoAdmin) || first.Local != nil {
		t.Fatalf("the hello before the welcome claims the built-in runner's part: %+v", first)
	}
	return env
}

// waitAsk returns the first offer_ask for runID after skip earlier ones.
func (h *jobHub) waitAsk(runID string, skip int) runnerwire.OfferAsk {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, msg := range h.messages(runID) {
			if msg.Type == runnerwire.TypeOfferAsk && msg.OfferAsk != nil {
				if n == skip {
					return *msg.OfferAsk
				}
				n++
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("run %s: no offer_ask #%d; got %+v", runID, skip+1, h.messages(runID))
	return runnerwire.OfferAsk{}
}

// noMessage fails when runID got a message of type typ within a moment.
func (h *jobHub) noMessage(runID, typ string) {
	h.t.Helper()
	time.Sleep(300 * time.Millisecond)
	for _, msg := range h.messages(runID) {
		if msg.Type == typ {
			h.t.Fatalf("run %s: unexpected %s: %+v", runID, typ, msg)
		}
	}
}

func (h *jobHub) answer(runID, account string, start, always bool) {
	h.t.Helper()
	h.send(runnerwire.Envelope{Type: runnerwire.TypeOfferAnswer,
		OfferAnswer: &runnerwire.OfferAnswer{RunID: runID, AccountID: account, Start: start, AlwaysAllowShell: always}})
}

// The built-in runner says what it is, and starts a run someone started by
// hand at once: no question anywhere, no offer_ask.
func TestBuiltInRunnerStartsRuns(t *testing.T) {
	env := startBuiltInRunner(t, nil)
	hello := env.hub.hello()
	info := hello.Hello
	for _, cp := range []string{runnerwire.CapOfferAsk, runnerwire.CapAppAccept, runnerwire.CapRepoAdmin} {
		if !slices.Contains(info.Capabilities, cp) {
			t.Errorf("hello lacks %s: %v", cp, info.Capabilities)
		}
	}
	if info.Local == nil || info.Local.Permission["claude"] != runnerwire.PermEdit || info.Local.AskEveryRun ||
		info.Local.ShellWithoutAsking == nil || !info.Local.AutoPR {
		t.Fatalf("hello's local settings = %+v", info.Local)
	}
	job := testJob("run-hand", "ok: do it")
	job.Origin = runnerwire.OriginPerson
	env.hub.offer(job)
	env.hub.wait("run-hand", runnerwire.TypeFinish)
	if msgs := env.hub.messages("run-hand"); msgs[0].Type != runnerwire.TypeAccept {
		t.Fatalf("first message %+v", msgs[0])
	}
	for _, msg := range env.hub.messages("run-hand") {
		if msg.Type == runnerwire.TypeOfferAsk {
			t.Fatalf("asked about a run started by hand: %+v", msg)
		}
	}
	// An automatic run of an agent that cannot run any command starts too.
	auto := testJob("run-auto", "ok: do it")
	auto.Origin = runnerwire.OriginAutopilot
	env.hub.offer(auto)
	env.hub.wait("run-auto", runnerwire.TypeAccept)
}

// An automatic run of a full-access agent waits for the owner's OK in the
// app: only the owner's answer for that run counts; "don't ask again" is
// saved; a no is declined_in_app.
func TestBuiltInRunnerAsksInTheApp(t *testing.T) {
	path := t.TempDir() + "/runner.json"
	env := startBuiltInRunner(t, func(c *Client) {
		c.Config.PermissionMode = "bypassPermissions"
		c.ConfigPath = path
		if err := SaveConfig(path, c.Config); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.hello()
	job := testJob("run-ask", "ok: do it")
	job.Origin = runnerwire.OriginAutopilot
	env.hub.offer(job)
	ask := env.hub.waitAsk("run-ask", 0)
	if ask.RunID != "run-ask" || ask.Where != runnerwire.AskInApp || len(ask.Reasons) != 1 ||
		ask.Reasons[0] != (runnerwire.AskReason{Code: runnerwire.AskShellAgent, Agent: "claude", Setting: "permissionMode=bypassPermissions"}) {
		t.Fatalf("ask = %+v", ask)
	}
	expires, err := time.Parse(time.RFC3339, ask.ExpiresAt)
	if err != nil || time.Until(expires) < 25*time.Minute || time.Until(expires) > 31*time.Minute {
		t.Fatalf("expires %q (%v)", ask.ExpiresAt, err)
	}
	// Someone else, and a run that is not waiting: nothing happens.
	env.hub.answer("run-ask", "u-other", true, true)
	env.hub.answer("run-other", wsOwner, true, true)
	env.hub.noMessage("run-ask", runnerwire.TypeAccept)

	env.hub.answer("run-ask", wsOwner, true, true)
	env.hub.wait("run-ask", runnerwire.TypeFinish)
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(env.client.cfg().ShellWithoutAsking, "claude") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !slices.Contains(env.client.cfg().ShellWithoutAsking, "claude") {
		t.Fatal("don't ask again was not applied")
	}
	if saved, err := LoadConfig(path); err != nil || !slices.Equal(saved.ShellWithoutAsking, []string{"claude"}) {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	// The next automatic run of that agent starts unasked.
	next := testJob("run-next", "ok: again")
	next.Origin = runnerwire.OriginPipeline
	env.hub.offer(next)
	env.hub.wait("run-next", runnerwire.TypeAccept)
	for _, msg := range env.hub.messages("run-next") {
		if msg.Type == runnerwire.TypeOfferAsk {
			t.Fatalf("asked again: %+v", msg)
		}
	}

	// A no.
	if err := func() error {
		env.client.settingsMu.Lock()
		defer env.client.settingsMu.Unlock()
		_, err := env.client.updateConfig(func(cfg *Config) error { cfg.ShellWithoutAsking = nil; return nil })
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	no := testJob("run-no", "ok: no")
	no.Origin = runnerwire.OriginAutopilot
	env.hub.offer(no)
	env.hub.waitAsk("run-no", 0)
	env.hub.answer("run-no", wsOwner, false, true)
	decline := env.hub.wait("run-no", runnerwire.TypeDecline)
	if decline.Code != runnerwire.DeclineInApp || decline.Reason == "" {
		t.Fatalf("decline = %+v", decline)
	}
	if slices.Contains(env.client.cfg().ShellWithoutAsking, "claude") {
		t.Fatal("don't ask again applied on a no")
	}
}

// Nobody answers: timed_out.
func TestBuiltInRunnerAskTimesOut(t *testing.T) {
	env := startBuiltInRunner(t, func(c *Client) {
		c.Config.PermissionMode = "bypassPermissions"
		c.App = &AppAccepter{Timeout: 300 * time.Millisecond}
	})
	env.hub.hello()
	job := testJob("run-late", "ok:")
	job.Origin = runnerwire.OriginAutopilot
	env.hub.offer(job)
	if decline := env.hub.wait("run-late", runnerwire.TypeDecline); decline.Code != runnerwire.DeclineTimedOut {
		t.Fatalf("decline = %+v", decline)
	}
}

// Pending asks go out again after a reconnect, as they were; a cancel from
// the hub ends the wait.
func TestBuiltInRunnerResendsAsks(t *testing.T) {
	env := startBuiltInRunner(t, func(c *Client) {
		c.Config.PermissionMode = "bypassPermissions"
	})
	env.hub.hello()
	wait := testJob("run-wait", "ok:")
	wait.Origin = runnerwire.OriginHandoff
	env.hub.offer(wait)
	first := env.hub.waitAsk("run-wait", 0)
	env.hub.kick()
	// The reconnect's hello lists the run it still asks about, so the hub
	// keeps it offered instead of queueing it again.
	if hello := env.hub.hello(); !slices.Contains(hello.Active, "run-wait") {
		t.Fatalf("hello after the reconnect lists %v, not the run it asks about", hello.Active)
	}
	again := env.hub.waitAsk("run-wait", 1)
	if again.ExpiresAt != first.ExpiresAt || again.Where != runnerwire.AskInApp {
		t.Fatalf("resent ask %+v, first %+v", again, first)
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-wait", Reason: "stop"})
	if finish := env.hub.wait("run-wait", runnerwire.TypeFinish); finish.Result.Status != runnerwire.RunCancelled {
		t.Fatalf("finish = %+v", finish.Result)
	}
}

// A runner the hub did not confirm stays classic: its hello drops the
// built-in capabilities, it drops every offer_answer, and it refuses the
// settings only the built-in runner takes.
func TestUnconfirmedRunnerStaysClassic(t *testing.T) {
	asked := make(chan string, 4)
	env := startJobRunnerWith(t, func(h *jobHub) {
		h.owner, h.edition = wsOwner, runnerwire.EditionSingle // no builtIn
	}, func(c *Client) {
		c.Edition = runnerwire.EditionSingle
		c.Accepter = blockingAccepter(asked)
	})
	// Its own side holds, but it claims nothing before the hub's word, and
	// the hub never gives it: no hello says otherwise.
	first := env.hub.hello()
	if slices.Contains(first.Hello.Capabilities, runnerwire.CapAppAccept) || slices.Contains(first.Hello.Capabilities, runnerwire.CapRepoAdmin) ||
		!slices.Contains(first.Hello.Capabilities, runnerwire.CapOfferAsk) || first.Hello.Local != nil {
		t.Fatalf("hello = %+v", first.Hello)
	}
	if env.client.BuiltIn() {
		t.Fatal("built-in without the hub's word")
	}
	job := testJob("run-classic", "ok:")
	job.Origin = runnerwire.OriginPerson
	env.hub.offer(job)
	if got := <-asked; got != "run-classic" {
		t.Fatalf("asked about %s", got)
	}
	env.hub.answer("run-classic", wsOwner, true, false)
	env.hub.noMessage("run-classic", runnerwire.TypeAccept)

	on := true
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest,
		Settings: &runnerwire.SettingsRequest{ID: "s-local", AccountID: wsOwner, AskEveryRun: &on}})
	done := waitSettingsDone(t, env, "s-local")
	if done.Code != runnerwire.SettingsErrLocalOnly {
		t.Fatalf("local settings on a classic runner: %+v", done)
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-classic", Reason: "stop"})
	env.hub.wait("run-classic", runnerwire.TypeFinish)
}

func waitSettingsDone(t *testing.T, env *jobEnv, id string) runnerwire.SettingsDone {
	t.Helper()
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

// personAccepter is a terminal someone sits at: it says so (offer_ask goes
// out) and blocks until the run ends.
type personAccepter struct{ asked chan string }

func (p personAccepter) Accept(ctx context.Context, o Offer) (bool, string) {
	p.asked <- o.Job.RunID
	<-ctx.Done()
	return false, ReasonCancelled
}
func (personAccepter) CanAsk() bool              { return true }
func (personAccepter) AskTimeout() time.Duration { return 10 * time.Minute }

// A classic runner tells the hub why a run waits at its terminal
// (offer_ask, where runner), and its declines carry codes.
func TestClassicRunnerAsksOnTheRunner(t *testing.T) {
	asked := make(chan string, 4)
	env := startJobRunner(t, func(c *Client) {
		c.Accepter = personAccepter{asked: asked}
		c.Config.AutoAccept = &runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true, Agents: []string{"claude"}}
		c.Config.Interactive = new(bool)
		off := false
		c.Config.GlobalRuns = &off
	})
	env.hub.hello()
	env.hub.offer(testJob("run-terminal", "ok:"))
	<-asked
	ask := env.hub.waitAsk("run-terminal", 0)
	codes := map[string]string{}
	for _, r := range ask.Reasons {
		codes[r.Code] += r.Missing
	}
	if ask.Where != runnerwire.AskOnRunner || codes[runnerwire.AskNeedsStepUp] != "" || codes[runnerwire.AskNotOnList] != runnerwire.AskMissingRepo ||
		len(ask.Reasons) != 2 {
		t.Fatalf("ask = %+v", ask)
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-terminal", Reason: "stop"})
	env.hub.wait("run-terminal", runnerwire.TypeFinish)

	for runID, c := range map[string]struct {
		edit func(*runnerwire.Job)
		code string
	}{
		"run-norepo":       {func(j *runnerwire.Job) { j.RepoID = "nope" }, runnerwire.DeclineRepoNotAdded},
		"run-noagent":      {func(j *runnerwire.Job) { j.Agent = "gemini" }, runnerwire.DeclineAgentMissing},
		"run-interactive":  {func(j *runnerwire.Job) { j.Mode = runnerwire.ModeInteractive }, runnerwire.DeclineInteractiveOff},
		"run-global":       {func(j *runnerwire.Job) { j.Global, j.Project, j.NodeID, j.Slug = true, "", "", "fix" }, runnerwire.DeclineGlobalOff},
		"run-badid!":       {nil, runnerwire.DeclineOther},
		"run-unknownmodel": {func(j *runnerwire.Job) { j.Model = "nope" }, runnerwire.DeclineOther},
	} {
		job := testJob(runID, "ok:")
		if c.edit != nil {
			c.edit(&job)
		}
		env.hub.offer(job)
		if decline := env.hub.wait(runID, runnerwire.TypeDecline); decline.Code != c.code {
			t.Errorf("%s: decline %+v, want code %s", runID, decline, c.code)
		}
	}
}

// A no at the terminal is declined_on_runner; nobody there is no_one_there.
func TestClassicDeclineCodes(t *testing.T) {
	for reason, code := range map[string]string{
		ReasonDeclined:   runnerwire.DeclineOnRunner,
		ReasonTimedOut:   runnerwire.DeclineTimedOut,
		ReasonNoTerminal: runnerwire.DeclineNoOneThere,
		ReasonInputGone:  runnerwire.DeclineNoOneThere,
		"something else": runnerwire.DeclineOther,
	} {
		j := &jobs{c: &Client{Config: &Config{}, Accepter: acceptFunc(func(context.Context, Offer) (bool, string) { return false, reason })}}
		r := &run{job: testJob("run-1", "x"), repo: runnerwire.Repo{ID: "r1"}}
		if ok, got, _ := j.ask(context.Background(), r); ok || got != code {
			t.Errorf("%q: code %q, want %q", reason, got, code)
		}
	}
}
