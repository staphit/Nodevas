package runs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// envConn is a runner connection that keeps everything sent to it.
type envConn struct {
	mu   sync.Mutex
	sent []runnerwire.Envelope
}

func (c *envConn) Bye(string) {}

func (c *envConn) Send(env runnerwire.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, env)
	return nil
}

func (c *envConn) answers() []runnerwire.OfferAnswer {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []runnerwire.OfferAnswer
	for _, env := range c.sent {
		if env.Type == runnerwire.TypeOfferAnswer && env.OfferAnswer != nil {
			out = append(out, *env.OfferAnswer)
		}
	}
	return out
}

// askFixture is a dispatcher with one connected runner of ann's, counting
// notifications and keeping audit entries.
type askFixture struct {
	s       *Store
	d       *Dispatcher
	reg     *runners.Registry
	conn    *envConn
	runner  runnerwire.Runner
	detach  func()
	mu      sync.Mutex
	notices map[string]int
	audit   []AuditEntry
}

func newAskFixture(t *testing.T, caps ...string) *askFixture {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	reg := runners.NewRegistry(s.db, func() {})
	runner, _, err := reg.Create(ctx, identity.Actor{ID: "u-ann", Name: "ann"}, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, runner.ID, runnerwire.Info{
		Capabilities: append([]string{runnerwire.CapHeadless, runnerwire.CapOfferAsk}, caps...), MaxConcurrent: 4,
		Agents: []runnerwire.Agent{{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}}}},
	}); err != nil {
		t.Fatal(err)
	}
	f := &askFixture{s: s, reg: reg, conn: &envConn{}, runner: runner, notices: map[string]int{}}
	f.detach = reg.Attach(runner.ID, f.conn)
	f.d = NewDispatcher(s, reg, nil, nil)
	f.d.SetNotify(func(_, runID string) {
		f.mu.Lock()
		f.notices[runID]++
		f.mu.Unlock()
	})
	f.d.SetAudit(func(entry AuditEntry) {
		f.mu.Lock()
		f.audit = append(f.audit, entry)
		f.mu.Unlock()
	})
	return f
}

func (f *askFixture) noticesOf(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notices[id]
}

// offered records a run with origin and offers it.
func (f *askFixture) offered(t *testing.T, origin string) Record {
	t.Helper()
	ctx := context.Background()
	r, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: f.runner.ID, RunnerName: "desk",
		OwnerID: "u-ann", Agent: "codex", Model: "default", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann", Origin: origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	r = f.d.Submit(ctx, r)
	if r.Status != runnerwire.RunOffered {
		t.Fatalf("not offered: %s", r.Status)
	}
	return r
}

func shellAsk(runID, where string) runnerwire.OfferAsk {
	return runnerwire.OfferAsk{RunID: runID, Where: where, ExpiresAt: "2026-11-14T14:32:00+08:00", Machine: "desk",
		Reasons: []runnerwire.AskReason{{Code: runnerwire.AskShellAgent, Agent: "codex", Setting: "codexSandbox=danger-full-access"}}}
}

// Single-user, §1.5: an ask is kept beside its offered run, clamped, shown
// on the run while it is offered and notified once; it goes when the run
// moves on or the runner reconnects, and the runner's resend brings it back
// without a second notification.
func TestOfferAskStoredClearedAndRefilled(t *testing.T) {
	f := newAskFixture(t, runnerwire.CapAppAccept)
	ctx := context.Background()
	r := f.offered(t, runnerwire.OriginAutopilot)
	if job := f.conn.sent[0].Job; job == nil || job.Origin != runnerwire.OriginAutopilot {
		t.Fatalf("offer = %+v, want origin autopilot", job)
	}

	if err := f.d.OfferAsk(ctx, "another-runner", shellAsk(r.ID, runnerwire.AskInApp)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another runner's ask: %v", err)
	}
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, "browser")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an ask that says no place: %v", err)
	}
	if run := f.d.WithAsk(r.Run); run.Ask != nil {
		t.Fatalf("an ask before any: %+v", run.Ask)
	}
	// "In the app" from a runner the hub did not welcome as its built-in
	// one: answered on that runner's PC, so the app shows no buttons.
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if run := f.d.WithAsk(r.Run); run.Ask == nil || run.Ask.Where != runnerwire.AskOnRunner {
		t.Fatalf("a classic runner's ask in the app = %+v", run.Ask)
	}
	f.reg.SetWelcomed(f.runner.ID, true)

	ask := shellAsk(r.ID, runnerwire.AskInApp)
	for range 12 {
		ask.Reasons = append(ask.Reasons, runnerwire.AskReason{Code: runnerwire.AskEveryRun, Setting: strings.Repeat("x", 1000)})
	}
	ask.Reasons = append([]runnerwire.AskReason{{Agent: "no code"}}, ask.Reasons...)
	if err := f.d.OfferAsk(ctx, f.runner.ID, ask); err != nil {
		t.Fatal(err)
	}
	run := f.d.WithAsk(r.Run)
	if run.Ask == nil || run.Ask.Where != runnerwire.AskInApp || run.Ask.Machine != "desk" ||
		run.Ask.ExpiresAt != "2026-11-14T06:32:00Z" || len(run.Ask.Reasons) != runnerwire.MaxAskReasons ||
		run.Ask.Reasons[0].Code != runnerwire.AskShellAgent || len(run.Ask.Reasons[1].Setting) != runnerwire.MaxAskFieldBytes {
		t.Fatalf("stored ask = %+v", run.Ask)
	}
	if n := f.noticesOf(r.ID); n != 1 {
		t.Fatalf("%d notifications for a new ask, want 1", n)
	}
	// Said again (a resend): not news.
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if n := f.noticesOf(r.ID); n != 1 {
		t.Fatalf("%d notifications after a resend, want 1", n)
	}

	// A reconnect: the hub forgets, the runner says it again.
	f.d.Attached(ctx, f.runner.ID, []string{r.ID})
	if _, ok := f.d.Ask(r.ID); ok {
		t.Fatal("an ask survived the runner's reconnect")
	}
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.d.Ask(r.ID); !ok || f.noticesOf(r.ID) != 1 {
		t.Fatalf("refill: ask %v, %d notifications", ok, f.noticesOf(r.ID))
	}

	// A run that is not offered shows none, even if one was stored.
	shown := r.Run
	shown.Status = runnerwire.RunRunning
	if f.d.WithAsk(shown).Ask != nil {
		t.Fatal("an ask on a running run")
	}

	// Declined in the app: the ask goes, the code stays on the run.
	if err := f.d.Decline(ctx, f.runner.ID, r.ID, "you chose not to start it", runnerwire.DeclineInApp); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.d.Ask(r.ID); ok {
		t.Fatal("an ask survived the decline")
	}
	declined, err := f.s.Get(ctx, r.ID)
	if err != nil || declined.DeclineCode != runnerwire.DeclineInApp || declined.Origin != runnerwire.OriginAutopilot {
		t.Fatalf("declined = %+v, %v", declined.Run, err)
	}
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); !errors.Is(err, ErrTransition) {
		t.Fatalf("an ask about a declined run: %v", err)
	}

	// An accept, a cancel or a runner that goes away end the ask too.
	accepted := f.offered(t, runnerwire.OriginPipeline)
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(accepted.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: accepted.ID, Base: "main", Branch: "agent/x"}); err != nil {
		t.Fatal(err)
	}
	cancelled := f.offered(t, "")
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(cancelled.ID, runnerwire.AskOnRunner)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Cancel(ctx, cancelled.ID, "cancelled by ann"); err != nil {
		t.Fatal(err)
	}
	gone := f.offered(t, runnerwire.OriginPerson)
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(gone.ID, runnerwire.AskOnRunner)); err != nil {
		t.Fatal(err)
	}
	f.d.Detached(ctx, f.runner.ID) // still online: a newer connection keeps it
	if _, ok := f.d.Ask(gone.ID); !ok {
		t.Fatal("a stale detach dropped the live connection's ask")
	}
	f.detach()
	f.d.Detached(ctx, f.runner.ID)
	for _, id := range []string{accepted.ID, cancelled.ID, gone.ID} {
		if _, ok := f.d.Ask(id); ok {
			t.Fatalf("an ask survived on %s", id)
		}
	}
}

func TestCleanDeclineCode(t *testing.T) {
	for code, want := range map[string]string{
		"":                             "",
		runnerwire.DeclineInApp:        runnerwire.DeclineInApp,
		runnerwire.DeclineTimedOut:     runnerwire.DeclineTimedOut,
		runnerwire.DeclineRepoNotAdded: runnerwire.DeclineRepoNotAdded,
		runnerwire.DeclineOther:        runnerwire.DeclineOther,
		"<script>":                     runnerwire.DeclineOther,
	} {
		if got := CleanDeclineCode(code); got != want {
			t.Errorf("CleanDeclineCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestCleanDeclineCodeKeepsContinueCodes(t *testing.T) {
	for _, code := range []string{
		runnerwire.DeclinePREnded,
		runnerwire.DeclinePRMoved,
		runnerwire.DeclineBranchMoved,
	} {
		if got := CleanDeclineCode(code); got != code {
			t.Errorf("CleanDeclineCode(%q) = %q, want %q", code, got, code)
		}
	}
}

// The answer from the app reaches only the built-in runner that asked in
// the app, from its owner, while the run waits; and it is audited.
func TestAnswerOfferRelaysOnlyToTheBuiltInRunner(t *testing.T) {
	f := newAskFixture(t, runnerwire.CapAppAccept)
	ctx := context.Background()
	r := f.offered(t, runnerwire.OriginAutopilot)
	yes := OfferAnswer{AccountID: "u-ann", AccountName: "ann", Start: true, AlwaysAllowShell: true}

	if err := f.d.AnswerOffer(ctx, r, OfferAnswer{AccountID: "u-bob", Start: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not the owner: %v", err)
	}
	if err := f.d.AnswerOffer(ctx, r, yes); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("no ask: %v", err)
	}
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskOnRunner)); err != nil {
		t.Fatal(err)
	}
	if err := f.d.AnswerOffer(ctx, r, yes); !errors.Is(err, ErrApproveOnRunner) {
		t.Fatalf("asked on the runner: %v", err)
	}
	// Asked in the app, but the hub never welcomed it as built in.
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if err := f.d.AnswerOffer(ctx, r, yes); !errors.Is(err, ErrApproveOnRunner) {
		t.Fatalf("a classic runner: %v", err)
	}
	if len(f.conn.answers()) != 0 {
		t.Fatalf("answers sent to a classic runner: %+v", f.conn.answers())
	}

	f.reg.SetWelcomed(f.runner.ID, true)
	if err := f.d.OfferAsk(ctx, f.runner.ID, shellAsk(r.ID, runnerwire.AskInApp)); err != nil {
		t.Fatal(err)
	}
	if err := f.d.AnswerOffer(ctx, r, yes); err != nil {
		t.Fatal(err)
	}
	// One answer per ask: a second (a double click, another tab) finds
	// none, and nothing more goes out or is recorded.
	if run := f.d.WithAsk(r.Run); run.Ask != nil {
		t.Fatalf("the ask stayed after its answer: %+v", run.Ask)
	}
	if err := f.d.AnswerOffer(ctx, r, OfferAnswer{AccountID: "u-ann", Start: false}); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("a second answer: %v", err)
	}
	answers := f.conn.answers()
	if len(answers) != 1 || answers[0] != (runnerwire.OfferAnswer{RunID: r.ID, AccountID: "u-ann", Start: true, AlwaysAllowShell: true}) {
		t.Fatalf("answers = %+v", answers)
	}
	f.mu.Lock()
	audit := f.audit
	f.mu.Unlock()
	if len(audit) != 1 || audit[0].Action != ActionOfferAnswer || audit[0].Target != r.ID || audit[0].ActorID != "u-ann" ||
		audit[0].Detail["start"] != true || audit[0].Detail["alwaysAllowShell"] != true || audit[0].Detail["origin"] != runnerwire.OriginAutopilot {
		t.Fatalf("audit = %+v", audit)
	}

	// Gone: nobody to tell (its asks went with it).
	f.detach()
	f.d.Detached(ctx, f.runner.ID)
	if err := f.d.AnswerOffer(ctx, r, yes); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("after the runner left: %v", err)
	}
	f.d.asksMu.Lock()
	f.d.asks[r.ID] = pendingAsk{runnerID: f.runner.ID, ask: shellAsk(r.ID, runnerwire.AskInApp)}
	f.d.asksMu.Unlock()
	if err := f.d.AnswerOffer(ctx, r, yes); !errors.Is(err, runners.ErrRunnerOffline) {
		t.Fatalf("offline: %v", err)
	}
}

// Origins: what a run records is what it is offered with; follow-ups no
// one clicked for are a pipeline's or a handoff's, and retry now makes a
// waiting run a person's.
func TestFollowUpOrigins(t *testing.T) {
	f := newHandoffFixture(t)
	ctx := context.Background()
	if _, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", RunnerID: f.runner.ID, OwnerID: "u-ann", Agent: "claude", RepoID: "repo",
		CreatedBy: "u-ann", Origin: "robot",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown origin: %v", err)
	}

	first := f.start(t, func(in *New) {
		in.Origin = runnerwire.OriginPerson
		in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}, {Agent: "claude"}}
	})
	if job := f.conn.last(t, first.ID); job.Origin != runnerwire.OriginPerson {
		t.Fatalf("first offer origin = %q", job.Origin)
	}
	// A plain failure falls back as a pipeline step.
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureModel})
	fallback, ok := f.follower(t, first.ID)
	if !ok || fallback.Origin != runnerwire.OriginPipeline || f.conn.last(t, fallback.ID).Origin != runnerwire.OriginPipeline {
		t.Fatalf("fallback = %+v", fallback.Run)
	}
	// One that continues partial work after a usage limit is a handoff.
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: fallback.ID, Base: "main", Branch: "agent/y"}); err != nil {
		t.Fatal(err)
	}
	f.fail(t, fallback.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota,
		HandoffBranch: "nodevas/handoff/" + fallback.ID, HandoffNote: "did half"})
	handoff, ok := f.follower(t, fallback.ID)
	if !ok || handoff.Origin != runnerwire.OriginHandoff {
		t.Fatalf("handoff fallback = %+v", handoff.Run)
	}

	// A usage-limit wait is a handoff, until a person retries it now.
	waiting := f.start(t, func(in *New) { in.Origin = runnerwire.OriginPerson })
	f.fail(t, waiting.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureRateLimit})
	wait, ok := f.follower(t, waiting.ID)
	if !ok || wait.Origin != runnerwire.OriginHandoff || wait.RetryAt == "" {
		t.Fatalf("wait = %+v", wait.Run)
	}
	now, err := f.d.RetryNow(ctx, wait.ID, "ann")
	if err != nil || now.Origin != runnerwire.OriginPerson || f.conn.last(t, wait.ID).Origin != runnerwire.OriginPerson {
		t.Fatalf("retry now = %+v, %v", now.Run, err)
	}
}
