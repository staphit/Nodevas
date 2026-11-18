package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// jobs is the runner's side of runs: it vets offers, asks for acceptance,
// makes worktrees, runs agents and queues what the hub must hear about them.
//
// It outlives any one connection. A run keeps going while the socket is down;
// its accept, events and finish wait here and go out, in that order, once the
// runner is connected again.
type jobs struct {
	c      *Client
	ctx    context.Context // the runner's lifetime; cancelling it stops every run
	stop   context.CancelFunc
	notify chan struct{} // something worth sending now, not at the next tick

	mu        sync.Mutex
	runs      map[string]*run
	order     []string // run IDs oldest first, so their messages go out in that order
	seen      map[string]bool
	seenOrder []string
	declines  []runnerwire.Envelope
	agents    []runnerwire.Agent
	// asks are the offers waiting for a person (offerask.go), by run, and
	// askOrder the ones the hub has not been told about yet, oldest first.
	asks     map[string]runnerwire.OfferAsk
	askOrder []string
	// hubName is what the hub calls this runner, from its welcome.
	hubName string

	// Pull requests being watched (see prwatch.go) and their unsent updates,
	// latest per run.
	watched    map[string]*watchedPR
	watchOrder []string
	watchErrs  map[string]string
	prPending  map[string]runnerwire.PRInfo
	prOrder    []string

	// Remote merges (see merge.go): answers kept to dedupe re-sent requests,
	// requests being worked on, and answers not yet written. mergeMu lets one
	// merge at a time talk to gh.
	merges        map[string]mergeRecord
	mergeInFlight map[string]bool
	mergeResults  []runnerwire.Envelope
	mergeMu       sync.Mutex

	// Workspace actions (see workspace_actions.go), kept like merges: answers
	// to dedupe re-sent requests, requests being worked on, answers not yet
	// written. They take mergeMu too: one action or merge at a time.
	workspaceRecords  map[string]workspaceRecord
	workspaceInFlight map[string]bool
	workspaceResults  []runnerwire.Envelope

	proxy    *Proxy
	proxyErr error

	// terms holds interactive runs' terminals (W2.5, see interactive.go).
	terms termRelay

	wg sync.WaitGroup
}

// run is one job this runner took on, from the offer until the hub has its
// finish.
type run struct {
	job    runnerwire.Job
	repo   runnerwire.Repo
	cancel context.CancelCauseFunc
	// cfg is the runner's config when the run was offered: the run uses
	// these settings to the end, whatever the app changes meanwhile.
	cfg *Config
	// accepted: someone said yes and the worktree exists. From here until the
	// finish is delivered, the run is listed as active in every hello.
	accepted bool
	started  time.Time
	// publishing: the agent is done and its work is being committed and
	// pushed; a cancel no longer applies.
	publishing bool
	// remoteTyping: the dispatcher may type into this interactive run's
	// terminal (auto-accepted under AllowRemoteTyping, or a yes here).
	remoteTyping bool
	// noRelay: auto-accepted without AllowShell, so the run's permission
	// prompts are not relayed to the hub (denied headless) whatever
	// permissionRelay says: a person there could otherwise approve a shell
	// command the auto-accept policy keeps from the agent.
	noRelay bool

	// Outbound, unsent, in this order. events is bounded: a run that talks a
	// lot while the socket is down loses its oldest events, never its finish.
	accept  *runnerwire.Accept
	seq     int64
	events  []runnerwire.RunEvent
	dropped int
	changes *runnerwire.RunChanges
	pr      *runnerwire.PRInfo
	finish  *runnerwire.RunResult

	// trail is what a handoff note needs from the events (handoff.go).
	trail handoffTrail
}

const (
	// maxBufferedEvents is how many unsent events a run keeps.
	maxBufferedEvents = 2000
	// maxSeen bounds the memory of finished run IDs kept to ignore re-offers.
	maxSeen = 1000
	// batchBytes keeps one events message under the hub's message limit.
	batchBytes = runnerwire.MaxMessageBytes - 64<<10
	// stderrTail is how much of an agent's stderr explains a failure.
	stderrTail = 4 << 10
	// waitDelay is how long the runner waits for an exited agent's children
	// to let go of its output before it stops reading.
	waitDelay = 5 * time.Second
	// stopGrace bounds how long a stopping runner waits for its runs to end.
	stopGrace = 15 * time.Second
)

// cancelRequest is why a run's context ended when the hub asked.
type cancelRequest struct{ reason string }

func (c cancelRequest) Error() string { return "cancelled by the hub: " + c.reason }

func newJobs(ctx context.Context, c *Client) *jobs {
	ctx, stop := context.WithCancel(ctx)
	return &jobs{
		c:      c,
		ctx:    ctx,
		stop:   stop,
		notify: make(chan struct{}, 1),
		runs:   map[string]*run{},
		seen:   map[string]bool{},
		asks:   map[string]runnerwire.OfferAsk{},

		watched:   map[string]*watchedPR{},
		watchErrs: map[string]string{},
		prPending: map[string]runnerwire.PRInfo{},

		merges:        map[string]mergeRecord{},
		mergeInFlight: map[string]bool{},

		workspaceRecords:  map[string]workspaceRecord{},
		workspaceInFlight: map[string]bool{},
	}
}

func (j *jobs) wake() {
	select {
	case j.notify <- struct{}{}:
	default:
	}
}

// shutdown kills every run and waits a while for them to end. Their finishes
// are not delivered; the hub fails those runs when the runner next says hello
// without them.
func (j *jobs) shutdown() {
	j.stop()
	done := make(chan struct{})
	go func() { j.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		j.c.logf("some runs did not stop within %s", stopGrace)
	}
	j.mu.Lock()
	proxy := j.proxy
	j.mu.Unlock()
	if proxy != nil {
		_ = proxy.Close()
	}
}

func (j *jobs) setAgents(agents []runnerwire.Agent) {
	j.mu.Lock()
	j.agents = agents
	j.mu.Unlock()
}

// withoutOpenCode2ACP is ad, except that an OpenCode 2 run set to go over
// ACP goes through `opencode run` instead, and note says so. OpenCode 2's acp
// takes no --cwd or --standalone, so it would run in the owner's background
// service, with that service's environment rather than this run's config.
func withoutOpenCode2ACP(ad adapter.Adapter, version string) (adapter.Adapter, string) {
	if _, acp := ad.(adapter.OpenCodeACP); acp && adapter.OpenCodeV2(version) {
		return adapter.OpenCode{}, "OpenCode 2 does not run over ACP on this runner yet; using opencode run"
	}
	return ad, ""
}

// agentVersion is the version line Detect read for kind, or "".
func (j *jobs) agentVersion(kind string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, agent := range j.agents {
		if agent.Kind == kind {
			return agent.Version
		}
	}
	return ""
}

// active is the hello's Active list: accepted runs whose finish the hub has
// not had yet, and offers still waiting for a person (offer_ask). A finished
// run is listed until its finish goes out, or the hub would fail it for being
// missing a moment before hearing it succeeded; an offer that is asked about
// is listed so a reconnect keeps it offered, and the ask said again after the
// welcome is about a run the hub still holds as offered.
func (j *jobs) active() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var ids []string
	for _, id := range j.order {
		r := j.runs[id]
		if r == nil {
			continue
		}
		if _, asking := j.asks[id]; r.accepted || asking {
			ids = append(ids, id)
		}
	}
	return ids
}

// offer handles a TypeOffer. It never blocks: the question goes to its own
// goroutine.
func (j *jobs) offer(job *runnerwire.Job) {
	if job == nil || job.RunID == "" {
		return
	}
	j.mu.Lock()
	if j.runs[job.RunID] != nil {
		// Being asked about or running; the hub re-offers a run it did not
		// see in an active list. Its accept or finish is on the way.
		j.mu.Unlock()
		j.c.logf("run %s: offered again while in hand; ignored", job.RunID)
		return
	}
	if j.seen[job.RunID] {
		// Over and reported, yet offered again: the hub re-queued it before
		// our finish arrived and refused that finish. A decline ends it
		// instead of leaving it offered to a runner that will never answer.
		j.declines = append(j.declines, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: job.RunID,
			Reason: "this runner already ran or refused this run", Code: runnerwire.DeclineOther})
		j.mu.Unlock()
		j.wake()
		j.c.logf("run %s: offered again after it ended; declined", job.RunID)
		return
	}
	// The settings now are the run's to the end.
	cfg := j.c.cfg()
	repo, code, reason := j.checkLocked(job, cfg)
	if reason != "" {
		j.rememberLocked(job.RunID)
		j.declines = append(j.declines, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: job.RunID, Reason: reason, Code: code})
		j.mu.Unlock()
		j.wake()
		j.c.logf("run %s: declined: %s", job.RunID, reason)
		return
	}
	ctx, cancel := context.WithCancelCause(j.ctx)
	r := &run{job: *job, repo: repo, cancel: cancel, cfg: cfg}
	j.runs[job.RunID] = r
	j.order = append(j.order, job.RunID)
	j.wg.Add(1)
	j.mu.Unlock()
	j.c.logf("run %s: offered by %s: %s in %s with %s/%s",
		job.RunID, orDash(job.RequestedBy), offerText(job), repo.Name, job.Agent, orDash(job.Model))
	go j.execute(ctx, r)
}

// checkLocked vets an offer against cfg, this runner's own settings.
// Everything the hub sends is a request; the allowlist, the agents and the
// limits here are what decide. A refusal comes with its decline code
// (runnerwire.Decline*) and reason.
func (j *jobs) checkLocked(job *runnerwire.Job, cfg *Config) (runnerwire.Repo, string, string) {
	other := runnerwire.DeclineOther
	if !validRunID.MatchString(job.RunID) {
		return runnerwire.Repo{}, other, "run id is not usable on this runner"
	}
	// A run for no node is a global agent's, with no board (global.go).
	if reason := checkGlobal(job, cfg); reason != "" {
		if job.Global && !cfg.GlobalRunsOn() {
			return runnerwire.Repo{}, runnerwire.DeclineGlobalOff, reason
		}
		return runnerwire.Repo{}, other, reason
	}
	var repo runnerwire.Repo
	found := false
	for _, candidate := range cfg.Repos {
		if candidate.ID == job.RepoID {
			repo, found = candidate, true
			break
		}
	}
	if !found {
		return repo, runnerwire.DeclineRepoNotAdded, fmt.Sprintf("repository %q is not on this runner's allowlist", job.RepoID)
	}
	busy := func() string {
		n := 0
		for _, r := range j.runs {
			if r.finish == nil {
				n++
			}
		}
		if limit := j.c.MaxConcurrentRuns(); n >= limit {
			return fmt.Sprintf("runner is busy: it runs at most %d at once", limit)
		}
		return ""
	}
	// A verify job runs no agent: the repo's own test command (verify.go).
	if job.Kind == runnerwire.JobVerify {
		if job.Mode != runnerwire.ModeHeadless {
			return repo, other, "a verify job is headless"
		}
		if reason := checkKind(job, cfg); reason != "" {
			return repo, other, reason
		}
		if reason := busy(); reason != "" {
			return repo, runnerwire.DeclineBusy, reason
		}
		return repo, "", ""
	}
	if j.c.adapterUnder(cfg, job.Agent) == nil {
		return repo, runnerwire.DeclineAgentMissing, fmt.Sprintf("agent %q not supported yet", job.Agent)
	}
	// W2.5: interactive runs (interactive.go).
	if reason := j.checkMode(job, cfg); reason != "" {
		if job.Mode == runnerwire.ModeInteractive && !cfg.InteractiveOn() {
			return repo, runnerwire.DeclineInteractiveOff, reason
		}
		return repo, other, reason
	}
	var agent *runnerwire.Agent
	for i := range j.agents {
		if j.agents[i].Kind == job.Agent {
			agent = &j.agents[i]
		}
	}
	if agent != nil && agent.SignedOut {
		return repo, runnerwire.DeclineAgentSignedOut, fmt.Sprintf("agent %q is not signed in on this runner", job.Agent)
	}
	if agent == nil || !agent.Installed {
		return repo, runnerwire.DeclineAgentMissing, fmt.Sprintf("agent %q is not installed on this runner", job.Agent)
	}
	model := job.Model
	if model == "" {
		model = "default"
	}
	listed := false
	for _, m := range agent.Models {
		listed = listed || m.ID == model
	}
	if !listed {
		return repo, other, fmt.Sprintf("model %q is not one this runner offers for %s", model, job.Agent)
	}
	if job.Base != "" {
		if err := checkBase(job.Base); err != nil {
			return repo, other, err.Error()
		}
	}
	if len(job.Prompt) > runnerwire.MaxPromptBytes {
		return repo, other, "prompt is too long"
	}
	if reason := checkKind(job, cfg); reason != "" {
		return repo, other, reason
	}
	if job.Kind == runnerwire.JobRevise {
		if reason := j.ownBranchLocked(job, repo); reason != "" {
			return repo, other, reason
		}
	}
	if reason := busy(); reason != "" {
		return repo, runnerwire.DeclineBusy, reason
	}
	return repo, "", ""
}

// cancelRun handles a TypeCancel.
func (j *jobs) cancelRun(runID, reason string) {
	j.mu.Lock()
	r := j.runs[runID]
	ended := r == nil || r.finish != nil
	publishing := !ended && r.publishing
	j.mu.Unlock()
	if ended {
		j.c.logf("run %s: cancel for a run that is not running here; ignored", runID)
		return
	}
	if publishing {
		j.c.logf("run %s: cancel after the agent finished; its changes are being published; ignored", runID)
		return
	}
	if reason == "" {
		reason = "cancelled"
	}
	j.c.logf("run %s: cancelling: %s", runID, reason)
	r.cancel(cancelRequest{reason: reason})
}

// execute is one run, from the question to the finish.
func (j *jobs) execute(ctx context.Context, r *run) {
	defer j.wg.Done()
	defer r.cancel(nil)
	job := r.job

	// Review and revise name an existing pull request: it must be this
	// repository's, open, and on the branch the job names, before anyone is
	// asked to accept.
	var check prCheck
	if job.Kind == runnerwire.JobReview || job.Kind == runnerwire.JobRevise || job.Kind == runnerwire.JobVerify {
		var reason string
		check, reason = j.checkPR(ctx, r)
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		if reason != "" {
			j.decline(r, runnerwire.DeclineOther, reason)
			return
		}
	}

	// A job continuing another run's partial work names a handoff branch:
	// it must be one this runner made, for this repository (handoff.go).
	if reason := j.prepareHandoff(ctx, r); reason != "" {
		j.decline(r, runnerwire.DeclineOther, reason)
		return
	}
	job = r.job

	ok, code, reason := j.ask(ctx, r)
	if ctx.Err() != nil {
		j.finish(r, cancelled(ctx))
		return
	}
	if !ok {
		j.decline(r, code, reason)
		return
	}
	switch job.Kind {
	case runnerwire.JobReview:
		j.executeReview(ctx, r, check)
		return
	case runnerwire.JobRevise:
		j.executeRevise(ctx, r, check)
		return
	case runnerwire.JobPlan:
		j.executePlan(ctx, r)
		return
	case runnerwire.JobVerify:
		j.executeVerify(ctx, r, check)
		return
	case runnerwire.JobJudge:
		j.executeJudge(ctx, r)
		return
	}

	dataDir, err := j.runCfg(r).RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = CreateWorktreeFrom(ctx, r.repo.Path, dataDir, job.RunID, branchKey(job.NodeID, job.Slug), job.Base, job.HandoffBranch)
	}
	if err != nil {
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate("could not create the worktree: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return
	}
	// A global agent has no board: its worktree says so while it runs, for a
	// `nodevas mcp` the owner's config starts there (noboard.go). No mark, no
	// run: Codex would start that one with nothing else to stop it.
	unmark := func() {}
	if job.Global {
		if unmark, err = markNoBoard(wt, job.RunID); err != nil {
			j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
				Error: adapter.Truncate("could not mark the worktree as a global agent's (no board): "+err.Error(),
					runnerwire.MaxSummaryBytes)})
			return
		}
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: wt.Base, Branch: wt.Branch}
	j.mu.Unlock()
	j.wake()
	from := wt.BaseRef
	if job.HandoffBranch != "" {
		from = job.HandoffBranch + " (continuing the previous run's work; base " + wt.BaseRef + ")"
	}
	j.c.logf("run %s: accepted; worktree %s on %s from %s", job.RunID, wt.Path, wt.Branch, from)
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("worktree %s on branch %s from %s", wt.Path, wt.Branch, from)})

	result, _ := j.runAgent(ctx, r, wt, job)
	unmark()
	if result.Status == runnerwire.RunCancelled {
		// Stopped on purpose: nothing is committed or pushed, and the
		// worktree stays as the agent left it.
		j.finish(r, result)
		return
	}
	if !j.guardGitDir(r, wt) {
		return
	}
	j.mu.Lock()
	r.publishing = true
	j.mu.Unlock()
	// Stopped at a usage limit: keep the partial work for the next run
	// instead of publishing it (handoff.go). Nothing is pushed. A global
	// agent's has no next run: it is committed on its own branch below.
	if result.Status == runnerwire.RunFailed && runnerwire.HandoffWorthy(result.FailureKind) &&
		job.Mode != runnerwire.ModeInteractive && !job.Global {
		changes := j.handOff(r, wt, &result)
		j.finishWith(r, result, changes, nil)
		return
	}
	changes, pr := j.publish(r, wt, result)
	j.finishWith(r, result, changes, pr)
}

// ask decides an offer. The built-in runner of a single-user hub starts it
// right away or asks in the app (askBuiltIn). Any other runner auto-accepts
// it when the owner's policy covers it, else asks at the terminal, telling
// the hub why it waits (offer_ask, where "runner"); with nobody there, the
// decline says what kept auto-accept from applying. A no comes with its
// decline code (runnerwire.Decline*).
func (j *jobs) ask(ctx context.Context, r *run) (bool, string, string) {
	offer := Offer{Job: r.job, Repo: r.repo, Shell: j.runCfg(r).ShellAccess(r.job)}
	if j.c.BuiltIn() {
		return j.askBuiltIn(ctx, r, offer)
	}
	policy := j.runCfg(r).AutoAcceptPolicy()
	auto, why, reasons := AutoAcceptAsk(policy, offer)
	interactive := r.job.Mode == runnerwire.ModeInteractive
	if auto {
		j.c.logf("run %s: auto-accepted: %s", r.job.RunID, why)
		if interactive {
			j.setRemoteTyping(r, policy.AllowRemoteTyping)
		}
		if !policy.AllowShell {
			// Nobody here said yes: a prompt relayed to the hub must not be
			// how the agent gets the shell the policy leaves to a person.
			j.mu.Lock()
			r.noRelay = true
			j.mu.Unlock()
		}
		return true, "", ""
	}
	j.c.logf("run %s: not auto-accepted: %s; asking at the terminal", r.job.RunID, why)
	ok, reason := false, ReasonNoTerminal
	if j.c.Accepter != nil {
		if person, timeout := personAt(j.c.Accepter); person {
			// The hub says the run waits for a person here, and why.
			j.queueAsk(runnerwire.OfferAsk{RunID: r.job.RunID, Where: runnerwire.AskOnRunner, Reasons: reasons,
				ExpiresAt: time.Now().Add(timeout).UTC().Format(time.RFC3339), Machine: machineName()})
			defer j.dropAsk(r.job.RunID)
		}
		ok, reason = j.c.Accepter.Accept(ctx, offer)
	}
	if ok && interactive {
		typing := false
		if asker, can := j.c.Accepter.(RemoteTypingAsker); can {
			typing = asker.AllowRemoteTyping(ctx, offer)
		}
		j.setRemoteTyping(r, typing)
	}
	if ok {
		return true, "", ""
	}
	code := runnerwire.DeclineOther
	switch reason {
	case ReasonDeclined:
		code = runnerwire.DeclineOnRunner
	case ReasonTimedOut:
		code = runnerwire.DeclineTimedOut
	case ReasonNoTerminal, ReasonInputGone:
		code = runnerwire.DeclineNoOneThere
	}
	if reason == ReasonNoTerminal && why != ReasonAutoAcceptOff {
		reason = why + " (and nobody is at the runner's terminal to accept)"
	}
	return false, code, reason
}

// askBuiltIn decides an offer to the built-in runner: it starts right away
// unless BuiltInDecision says the owner is asked first, in the app (and at
// serve's terminal too, when they asked to confirm every run). An
// interactive run is the owner's own terminal in their own app: typing is
// allowed.
func (j *jobs) askBuiltIn(ctx context.Context, r *run, offer Offer) (bool, string, string) {
	job := r.job
	interactive := job.Mode == runnerwire.ModeInteractive
	start, reasons := BuiltInDecision(j.runCfg(r), offer)
	if !start {
		app := j.c.app()
		answers := app.open(job.RunID)
		defer app.closeAsk(job.RunID)
		deadline := time.Now().Add(app.timeout())
		j.queueAsk(runnerwire.OfferAsk{RunID: job.RunID, Where: runnerwire.AskInApp, Reasons: reasons,
			ExpiresAt: deadline.UTC().Format(time.RFC3339), Machine: machineName()})
		defer j.dropAsk(job.RunID)
		j.c.logf("run %s: waiting for your OK in Nodevas: %s", job.RunID, askText(reasons))
		decision := app.wait(ctx, offer, answers, deadline, j.runCfg(r).AskEveryRun)
		if !decision.start {
			return false, decision.code, decision.reason
		}
		if decision.alwaysAllowShell && slices.ContainsFunc(reasons, func(reason runnerwire.AskReason) bool {
			return reason.Code == runnerwire.AskShellAgent
		}) {
			j.c.allowShellWithoutAsking(job.Agent)
		}
	}
	j.c.logf("run %s started (%s)", job.RunID, startedBy(job.Origin))
	if interactive {
		j.setRemoteTyping(r, true)
	}
	return true, "", ""
}

// disabledMCP is, for an OpenCode run, every MCP server but the run's own
// (every one at all for a global agent's run, which has none), to switch
// off. When OpenCode cannot say which it loads, the run goes on with them
// and says so.
func (j *jobs) disabledMCP(ctx context.Context, r *run, bin, dir string) []string {
	if r.job.Agent != "opencode" {
		return nil
	}
	list := adapter.OpenCodeMCPServers
	if r.job.Global {
		// No server of its own takes the owner's nodevas's place.
		list = adapter.OpenCodeEveryMCPServer
	}
	names, err := list(ctx, bin, dir, adapter.ReadOnly(r.job))
	if err != nil {
		j.c.logf("run %s: cannot list OpenCode's MCP servers to switch them off: %v", r.job.RunID, err)
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: "warning: could not switch off OpenCode's other MCP servers: " + adapter.Truncate(err.Error(), 300)})
		return nil
	}
	if len(names) > 0 {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: "OpenCode MCP servers switched off for this run: " + strings.Join(names, ", ")})
	}
	return names
}

// setRemoteTyping records whether the dispatcher may type into r's terminal.
func (j *jobs) setRemoteTyping(r *run, on bool) {
	j.mu.Lock()
	r.remoteTyping = on
	j.mu.Unlock()
	if on {
		j.c.logf("run %s: remote typing allowed", r.job.RunID)
	} else {
		j.c.logf("run %s: view only: typing from the hub is refused", r.job.RunID)
	}
}

// startAgent starts the agent in the worktree and waits for it, a cancel or
// the timeout, whichever comes first; runAgent (worktree_repoconfig.go) calls
// it. job is r.job, or for a review the job with the PR's diff added to its
// prompt. text is the agent's whole final message (adapter.Outcome.Text),
// which a review needs beyond the summary's cap.
func (j *jobs) startAgent(ctx context.Context, r *run, wt Worktree, job runnerwire.Job) (runnerwire.RunResult, string) {
	// W2.5: the agent's TUI in a terminal instead (interactive.go).
	if job.Mode == runnerwire.ModeInteractive {
		return j.runInteractive(ctx, r, wt), ""
	}
	fail := func(kind, format string, args ...any) (runnerwire.RunResult, string) {
		return runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: kind,
			Error: adapter.Truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)}, ""
	}
	version := j.agentVersion(job.Agent)
	ad, note := withoutOpenCode2ACP(j.c.adapterUnder(j.runCfg(r), job.Agent), version)
	if note != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: note})
	}
	bin, err := agentBinary(j.c.Commands, j.runCfg(r), job.Agent)
	if errors.Is(err, errAgentPathGone) {
		return fail(runnerwire.FailureAgentAbsent, "%s", agentPathGoneError(job.Agent))
	}
	if err != nil {
		return fail(runnerwire.FailureAgentAbsent, "%s is no longer on PATH", adapter.Binary(job.Agent))
	}
	// A global agent has no board: no door through the proxy, no MCP server.
	var proxy *Proxy
	var secret string
	if !job.Global {
		if proxy, err = j.proxyFor(); err != nil {
			return fail(runnerwire.FailureTask, "could not start the MCP proxy: %v", err)
		}
		secret = proxy.Register(job.RunID)
		defer proxy.Revoke(secret)
	}
	tmp, err := j.runTempDir()
	if err != nil {
		return fail(runnerwire.FailureTask, "could not make a temporary directory: %v", err)
	}
	defer os.RemoveAll(tmp)
	exe, err := os.Executable()
	if err != nil {
		return fail(runnerwire.FailureTask, "cannot find the nodevas executable for the MCP server: %v", err)
	}

	cfg := j.runCfg(r)
	j.mu.Lock()
	relay := cfg.PermissionRelay && !r.noRelay && !job.Global
	j.mu.Unlock()
	mode, modeText := runPermission(cfg, job, note == "")
	runEnv := adapter.RunEnv{
		Binary:         bin,
		Workdir:        wt.Path,
		Branch:         wt.Branch,
		TempDir:        tmp,
		MCP:            adapter.MCPServer{Command: exe},
		NoBoard:        job.Global,
		PermissionMode: mode,
		// Each adapter reads only the settings meant for it.
		AllowedTools:     cfg.AllowedTools,
		IgnoreUserConfig: cfg.CodexIgnoreUserConfig,
		DisableMCP:       j.disabledMCP(ctx, r, bin, wt.Path),
		Version:          version,
		PermissionRelay:  relay,
	}
	if !job.Global {
		serverFile, err := writeMCPServerFile(tmp, proxy.URL(secret))
		if err != nil {
			return fail(runnerwire.FailureTask, "could not write the MCP server's address: %v", err)
		}
		runEnv.MCP.Args = mcpArgs(serverFile, job, r.repo, adapter.RelayMCPTools(job.Agent, runEnv)...)
	}
	cmd, relayOff, err := adapter.Launch(ad, job, runEnv)
	if err != nil {
		return fail(runnerwire.FailureTask, "%v", err)
	}
	if claimed, ok := strings.CutSuffix(modeText, relayedText); ok {
		switch {
		case job.Global:
			modeText = claimed + noBoardNotRelayedText
		case !relay:
			modeText = claimed + autoAcceptedNotRelayedText
		case relayOff:
			// The adapter could not set the relay up (agy: no session
			// settings, no hook that runs nodevas): the log says what the
			// run really does.
			modeText = claimed + notRelayedText
		}
	}
	if job.Global {
		modeText += "; no board (a global agent)"
	}
	parser := ad.NewParser()
	// An ACP agent is spoken to while it runs (adapter/acp.go).
	var conv adapter.Conversation
	var stdin io.WriteCloser
	if ca, ok := ad.(adapter.Conversational); ok {
		conv = ca.NewConversation(job, runEnv)
		parser = conv
		if stdin, err = cmd.StdinPipe(); err != nil {
			return fail(runnerwire.FailureTask, "could not open %s's stdin: %v", job.Agent, err)
		}
	}
	stdout := &lineWriter{fn: func(line []byte) {
		for _, ev := range parser.Parse(line) {
			j.emit(r, ev)
		}
	}}
	stderr := &tailBuffer{max: stderrTail}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// The agent's children may hold its stdout after it exits; past this,
	// Wait stops waiting for them.
	cmd.WaitDelay = waitDelay
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		return fail(runnerwire.FailureAgentAbsent, "could not start %s: %v", job.Agent, err)
	}
	tree := attachTree(cmd)
	defer tree.close()
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("started %s (model %s, %s)", job.Agent, orDash(job.Model), modeText)})
	if conv != nil {
		conv.Begin(stdin)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timeout := j.c.runTimeout(j.runCfg(r), job.Kind)
	expired, stopTimer := j.c.startRunTimer(timeout)
	defer stopTimer()
	var waitErr error
	stopped := ""
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		stopped = "cancel"
		tree.kill()
		waitErr = <-done
	case <-expired:
		stopped = "timeout"
		tree.kill()
		waitErr = <-done
	}
	stdout.flush()
	if ender, ok := conv.(interface{ End() }); ok {
		// Permission requests still waiting for a person stop waiting.
		ender.End()
	}

	outcome := parser.Outcome()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("%s exited with code %d", job.Agent, exitCode)})
	result := runnerwire.RunResult{ExitCode: exitCode, Summary: outcome.Summary, Usage: outcome.Usage}
	switch {
	case stopped == "cancel":
		cancelledResult := cancelled(ctx)
		result.Status, result.Error = cancelledResult.Status, cancelledResult.Error
	case stopped == "timeout":
		result.Status = runnerwire.RunFailed
		result.Error = "timed out after " + minutesText(timeout)
		result.FailureKind = runnerwire.FailureTask
	case exitCode == 0 && !outcome.IsError:
		result.Status = runnerwire.RunSucceeded
	default:
		result.Status = runnerwire.RunFailed
		errText := stderr.String()
		result.Error = outcome.Error
		if result.Error == "" {
			result.Error = errText
		}
		if result.Error == "" && waitErr != nil {
			result.Error = waitErr.Error()
		}
		if result.Error == "" {
			result.Error = fmt.Sprintf("%s exited with code %d", job.Agent, exitCode)
		}
		result.Error = adapter.Truncate(result.Error, runnerwire.MaxSummaryBytes)
		// What the CLI said, then its stderr: a login problem often shows only
		// on stderr while the stream says something generic.
		result.FailureKind = ad.Classify(outcome.Error + "\n" + errText)
		// A CLI that is there but signed out: the app says how to sign in,
		// and a look now has it stop offering the agent.
		if result.FailureKind == runnerwire.FailureAgentAbsent && adapter.SignedOut(job.Agent, outcome.Error+"\n"+errText) {
			result.FailureCode = runnerwire.FailureSignedOut
			j.c.lookSoon()
		}
		// When a limit lifts, in this machine's zone: "resets 3pm" is the
		// owner's 3pm, which only the runner knows.
		if runnerwire.HandoffWorthy(result.FailureKind) {
			reset := outcome.ResetAt
			if reset.IsZero() {
				reset, _ = runnerwire.ParseResetTime(outcome.Error+"\n"+errText, time.Now(), time.Local)
			}
			if !reset.IsZero() && reset.After(time.Now()) {
				result.ResetAt = reset.UTC().Format(time.RFC3339)
			}
		}
	}
	return result, outcome.Text
}

// runPermission is how freely job's agent may act under this runner's
// settings, and how its start line says so. acp is false when an OpenCode run
// set to go over ACP goes through `opencode run` instead (withoutOpenCode2ACP).
func runPermission(cfg *Config, job runnerwire.Job, acp bool) (mode, text string) {
	if !adapter.ReadOnly(job) {
		text = permissionText(cfg, job.Agent)
		if claimed, ok := strings.CutSuffix(text, relayedText); ok && job.Agent == "opencode" && !acp {
			text = claimed + notRelayedText
		}
		return cfg.PermissionModeFor(job.Agent), text
	}
	// A plan or a judge reads the code as a review does; what a plan writes
	// goes on the board, through MCP, not into files.
	mode, text = reviewPermission(cfg, job.Agent)
	switch job.Kind {
	case runnerwire.JobPlan:
		text = strings.Replace(text, "for a review", "for a plan", 1)
	case runnerwire.JobJudge:
		text = strings.Replace(text, "for a review", "for a judge", 1)
	}
	return mode, text
}

// reviewPermission is how freely a review's agent may act: read only where
// the CLI has such a mode (Codex's read-only sandbox; Gemini CLI's default
// approval mode, under which an edit needs an approval nobody can give
// headless; OpenCode with edits and the shell denied; agy's plan mode). Claude
// Code keeps the owner's mode, with its writing, shell and web tools denied
// (adapter.ClaudeReadOnlyDeny), except a mode that skips its prompts
// (bypassPermissions, auto): that would leave reads anywhere on this machine
// open, so the run gets acceptEdits instead, where such a read needs a yes
// nobody gives headless. The prompt forbids edits either way, and the
// review's worktree is thrown away after.
func reviewPermission(cfg *Config, kind string) (mode, text string) {
	switch kind {
	case "codex":
		return "read-only", "sandbox read-only for a review"
	case "gemini":
		return "default", "approval mode default for a review"
	case "opencode":
		return "read-only", "permission read-only for a review"
	case "antigravity":
		return "plan", "mode plan for a review"
	case "claude":
		mode = cfg.ClaudePermissionMode()
		if mode == "bypassPermissions" || mode == "auto" {
			mode = adapter.DefaultClaudePermissionMode
		}
		text = "permission mode " + mode + " for a review"
		if cfg.PermissionRelay && adapter.PermissionRelayApplies(kind, mode, false) {
			text += relayedText
		}
		return mode, text
	}
	return cfg.PermissionModeFor(kind), permissionText(cfg, kind)
}

// relayedText ends the start line of a run whose permission prompts are
// relayed; notRelayedText that of one whose adapter could not relay them.
const (
	relayedText    = ", permission prompts relayed to Nodevas"
	notRelayedText = ", permission prompts not relayed (denied headless)"
	// autoAcceptedNotRelayedText ends that of a run auto-accepted without
	// allow-shell (run.noRelay).
	autoAcceptedNotRelayedText = ", permission prompts not relayed: auto-accepted without allow-shell (denied headless)"
	// noBoardNotRelayedText ends that of a global agent's run, which has no
	// board to ask through.
	noBoardNotRelayedText = ", permission prompts not relayed: a global agent has no board (denied headless)"
)

// permissionText says how freely the agent may act, for the run's log.
func permissionText(cfg *Config, kind string) string {
	text := modeText(cfg, kind)
	if cfg.RelaysPermissions(kind) {
		text += relayedText
	}
	return text
}

func modeText(cfg *Config, kind string) string {
	switch kind {
	case "codex":
		text := "sandbox " + cfg.CodexSandboxMode()
		if cfg.CodexIgnoreUserConfig {
			text += ", without the owner's config.toml"
		}
		return text
	case "gemini":
		return "approval mode " + cfg.GeminiApproval()
	case "opencode":
		return "permission " + cfg.OpenCodePermissionMode()
	case "antigravity":
		mode := cfg.AntigravityModeOrDefault()
		if mode == "full" {
			return "mode full (accept-edits, every permission skipped)"
		}
		return "mode " + mode
	}
	return "permission mode " + cfg.ClaudePermissionMode()
}

// mcpArgs is the run's `nodevas mcp` command line: the board through this
// runner's proxy (its URL in serverFile, see writeMCPServerFile), as a worker,
// under a session name that says which run it is, with only the tools a run
// uses: each definition is paid for on every model call.
func mcpArgs(serverFile string, job runnerwire.Job, repo runnerwire.Repo, relayTools ...string) []string {
	model := job.Model
	if model == "" {
		model = "default"
	}
	runID := slug(job.RunID)
	if len(runID) > 6 {
		runID = runID[:6]
	}
	repoName := slug(repo.Name)
	if repoName == "" {
		repoName = "repo"
	}
	args := []string{
		"mcp",
		"--server-file", serverFile,
		"--project", job.Project,
		"--agent-role", roleAgentClass(job),
		"--actor", "run:" + job.Agent,
		"--session-name", repoName + "-" + job.Agent + "-" + slug(model) + "-" + runID,
	}
	// A role job is served its role's tools only (the hub's allowlist); a
	// legacy job keeps the run toolset, exactly as before.
	if role := roleArgs(job, relayTools...); role != nil {
		return append(args, role...)
	}
	return append(args, "--toolset", "run")
}

// writeMCPServerFile puts the run's proxy URL in its private temp dir, owner
// only, for `nodevas mcp --server-file`: the URL carries the run's proxy
// secret, and every user on the machine can read a command line.
func writeMCPServerFile(tmp, url string) (string, error) {
	path := filepath.Join(tmp, "mcp-server")
	return path, os.WriteFile(path, []byte(url), 0o600)
}

// runTempDir makes a run's private directory for the files programs outside
// any sandbox read (the MCP config, agy's hooks): under the runner's data
// dir, owner-only, never the system's temp dir, which a Codex run's default
// sandbox may write. The caller removes it when the run ends.
func (j *jobs) runTempDir() (string, error) {
	dataDir, err := j.c.cfg().RunDataDir()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "run-")
}

func (j *jobs) proxyFor() (*Proxy, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.proxy == nil && j.proxyErr == nil {
		j.proxy, j.proxyErr = StartProxy(j.c.cfg(), j.c.HTTPClient)
	}
	return j.proxy, j.proxyErr
}

// cancelled is the result for a run whose context ended: the hub's cancel, or
// the runner stopping.
func cancelled(ctx context.Context) runnerwire.RunResult {
	reason := "runner stopped"
	var req cancelRequest
	if errors.As(context.Cause(ctx), &req) {
		reason = req.reason
	}
	return runnerwire.RunResult{Status: runnerwire.RunCancelled, ExitCode: -1, Error: reason}
}

func minutesText(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", n)
	}
	return d.String()
}

// emit stamps an event and queues it.
func (j *jobs) emit(r *run, ev runnerwire.RunEvent) {
	// Whatever the agent read or printed, a key does not reach the hub.
	ev.Text = adapter.Truncate(redact.Output(ev.Text), runnerwire.MaxEventText)
	ev.Input = adapter.Truncate(redact.Output(ev.Input), runnerwire.MaxEventText)
	j.mu.Lock()
	r.trail.see(ev)
	r.seq++
	ev.Seq = r.seq
	ev.At = time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	r.events = append(r.events, ev)
	if over := len(r.events) - maxBufferedEvents; over > 0 {
		// Drop a chunk, not one at a time, so a long outage is not a copy per event.
		over = max(over, maxBufferedEvents/10)
		r.events = append([]runnerwire.RunEvent(nil), r.events[over:]...)
		r.dropped += over
	}
	full := len(r.events) >= runnerwire.MaxEventsPerMessage
	j.mu.Unlock()
	if full {
		j.wake()
	}
}

func (j *jobs) finish(r *run, result runnerwire.RunResult) {
	j.finishWith(r, result, nil, nil)
}

// finishWith queues a run's changes, PR and finish together, so they go out in
// that order after its last events.
func (j *jobs) finishWith(r *run, result runnerwire.RunResult, changes *runnerwire.RunChanges, pr *runnerwire.PRInfo) {
	// Every failed run says why in a word the hub can route on; a failure
	// nothing recognised (a worktree git refused, a push) is the task's.
	// Every text in the result goes through redaction here, whatever its
	// source already did.
	result.Summary, result.Error = redact.Output(result.Summary), redact.Output(result.Error)
	result.HandoffNote = redact.Output(result.HandoffNote)
	if result.Verify != nil {
		v := *result.Verify
		v.Output = redact.Output(v.Output)
		result.Verify = &v
	}
	if len(result.Checks) > 0 {
		checks := slices.Clone(result.Checks)
		for i := range checks {
			checks[i].Note = redact.Output(checks[i].Note)
		}
		result.Checks = checks
	}
	if pr != nil {
		pr.Error = redact.Output(pr.Error)
	}
	switch {
	case result.Status != runnerwire.RunFailed:
		result.FailureKind = ""
	case result.FailureKind == "":
		result.FailureKind = runnerwire.FailureTask
	}
	if result.FailureKind != runnerwire.FailureAgentAbsent {
		result.FailureCode = ""
	}
	j.mu.Lock()
	r.changes, r.pr = changes, pr
	r.finish = &result
	dropped := r.dropped
	j.mu.Unlock()
	j.wake()
	line := fmt.Sprintf("run %s: %s", r.job.RunID, result.Status)
	if result.FailureKind != "" && result.FailureKind != runnerwire.FailureTask {
		line += " (" + result.FailureKind + ")"
	}
	if result.Verdict != "" {
		line += ", verdict " + result.Verdict
	}
	if result.Error != "" {
		line += ": " + firstLine(result.Error)
	}
	if !r.started.IsZero() {
		line += fmt.Sprintf(" after %s", time.Since(r.started).Round(time.Second))
	}
	if result.Usage != nil && result.Usage.CostUSD > 0 {
		line += fmt.Sprintf(" (≈$%.2f)", result.Usage.CostUSD)
	}
	if dropped > 0 {
		line += fmt.Sprintf("; %d events dropped while the hub was unreachable", dropped)
	}
	j.c.logf("%s", line)
}

// decline tells the hub the runner will not take r: code is its
// runnerwire.Decline* code, reason the English words beside it.
func (j *jobs) decline(r *run, code, reason string) {
	j.mu.Lock()
	j.forgetLocked(r.job.RunID)
	j.declines = append(j.declines, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: r.job.RunID, Reason: reason, Code: code})
	j.mu.Unlock()
	j.wake()
	j.c.logf("run %s: declined: %s", r.job.RunID, reason)
}

// forgetLocked drops a run whose story the hub has (or will have, for a
// decline), remembering its ID so a re-offer is ignored.
func (j *jobs) forgetLocked(id string) {
	delete(j.runs, id)
	for i, existing := range j.order {
		if existing == id {
			j.order = append(j.order[:i], j.order[i+1:]...)
			break
		}
	}
	j.rememberLocked(id)
}

func (j *jobs) rememberLocked(id string) {
	if j.seen[id] {
		return
	}
	j.seen[id] = true
	j.seenOrder = append(j.seenOrder, id)
	if len(j.seenOrder) > maxSeen {
		delete(j.seen, j.seenOrder[0])
		j.seenOrder = j.seenOrder[1:]
	}
}

// flush sends everything queued, oldest run first and, per run, accept then
// events then finish. A message leaves the queue only once written; one that
// fails is sent again on the next connection, which the hub tolerates (it
// drops events with a seq it already has).
func (j *jobs) flush(write func(runnerwire.Envelope) error) error {
	for {
		j.mu.Lock()
		msg, commit := j.nextLocked()
		j.mu.Unlock()
		if commit == nil {
			return nil
		}
		if err := write(msg); err != nil {
			return err
		}
		j.mu.Lock()
		commit()
		j.mu.Unlock()
	}
}

func (j *jobs) nextLocked() (runnerwire.Envelope, func()) {
	if len(j.declines) > 0 {
		return j.declines[0], func() { j.declines = j.declines[1:] }
	}
	// Why an offer waits; its accept or decline comes only once it is over.
	if len(j.askOrder) > 0 {
		id := j.askOrder[0]
		ask, ok := j.asks[id]
		if !ok {
			return runnerwire.Envelope{}, nil // never: dropAsk takes it off askOrder too
		}
		ask.Reasons = slices.Clone(ask.Reasons)
		return runnerwire.Envelope{Type: runnerwire.TypeOfferAsk, RunID: id, OfferAsk: &ask}, func() {
			if len(j.askOrder) > 0 && j.askOrder[0] == id {
				j.askOrder = j.askOrder[1:]
			}
		}
	}
	// A merge's answer goes before the PR update that shows it merged.
	if len(j.mergeResults) > 0 {
		return j.mergeResults[0], func() { j.mergeResults = j.mergeResults[1:] }
	}
	// A discard's answer goes before the PR update that shows its PR closed.
	if len(j.workspaceResults) > 0 {
		return j.workspaceResults[0], func() { j.workspaceResults = j.workspaceResults[1:] }
	}
	for _, id := range j.order {
		r := j.runs[id]
		if r.accept != nil {
			accept := *r.accept
			return runnerwire.Envelope{Type: runnerwire.TypeAccept, RunID: id, Accept: &accept},
				func() { r.accept = nil }
		}
		if len(r.events) > 0 {
			batch := takeBatch(r.events)
			last := batch[len(batch)-1].Seq
			return runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: id, Events: batch}, func() {
				i := 0
				for i < len(r.events) && r.events[i].Seq <= last {
					i++
				}
				r.events = r.events[i:]
			}
		}
		if r.finish == nil {
			continue
		}
		if r.changes != nil {
			changes := *r.changes
			return runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: id, Changes: &changes},
				func() { r.changes = nil }
		}
		if r.pr != nil {
			pr := *r.pr
			return runnerwire.Envelope{Type: runnerwire.TypePR, RunID: id, PR: &pr},
				func() { r.pr = nil }
		}
		result := *r.finish
		return runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: id, Result: &result},
			func() { j.forgetLocked(id) }
	}
	// PR updates from the watcher, after every run's own messages.
	if len(j.prOrder) > 0 {
		id := j.prOrder[0]
		pr := j.prPending[id]
		return runnerwire.Envelope{Type: runnerwire.TypePR, RunID: id, PR: &pr}, func() {
			// A newer update queued while this one was being written stays.
			if now, ok := j.prPending[id]; ok && now == pr {
				delete(j.prPending, id)
				j.prOrder = j.prOrder[1:]
			}
		}
	}
	return runnerwire.Envelope{}, nil
}

// queueAsk records that an offer waits for a person, and tells the hub why
// (offer_ask) at the next flush.
func (j *jobs) queueAsk(ask runnerwire.OfferAsk) {
	j.mu.Lock()
	if j.asks == nil {
		j.asks = map[string]runnerwire.OfferAsk{}
	}
	j.asks[ask.RunID] = ask
	j.askOrder = append(slices.DeleteFunc(j.askOrder, func(id string) bool { return id == ask.RunID }), ask.RunID)
	j.mu.Unlock()
	j.wake()
}

// dropAsk forgets an ask that is over. The hub clears its own copy on the
// run's accept, decline or finish.
func (j *jobs) dropAsk(runID string) {
	j.mu.Lock()
	delete(j.asks, runID)
	j.askOrder = slices.DeleteFunc(j.askOrder, func(id string) bool { return id == runID })
	j.mu.Unlock()
}

// resendAsks queues every pending ask again, for a new connection: the hub
// keeps asks in memory only.
func (j *jobs) resendAsks() {
	j.mu.Lock()
	j.askOrder = j.askOrder[:0]
	for _, id := range j.order {
		if _, ok := j.asks[id]; ok {
			j.askOrder = append(j.askOrder, id)
		}
	}
	j.mu.Unlock()
}

// machineName is this machine's name, for "waiting on <PC>".
func machineName() string {
	host, _ := os.Hostname()
	return truncate(host, runnerwire.MaxAskFieldBytes)
}

// takeBatch copies the first events that fit one message.
func takeBatch(events []runnerwire.RunEvent) []runnerwire.RunEvent {
	size := 0
	n := 0
	for n < len(events) && n < runnerwire.MaxEventsPerMessage {
		raw, _ := json.Marshal(events[n])
		if n > 0 && size+len(raw) > batchBytes {
			break
		}
		size += len(raw) + 1
		n++
	}
	return append([]runnerwire.RunEvent(nil), events[:n]...)
}

// lineWriter splits an agent's stdout into lines for its parser. It is the
// command's Stdout rather than a pipe the runner reads, so cmd.WaitDelay can
// stop the copying when the agent's children keep the pipe open.
type lineWriter struct {
	fn       func(line []byte)
	buf      []byte
	skipping bool
}

// maxLine drops a single stdout line longer than this (a tool result that
// dumped a huge file); its events would be truncated to nothing useful anyway.
const maxLine = 16 << 20

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) add(chunk []byte) {
	if w.skipping {
		return
	}
	if len(w.buf)+len(chunk) > maxLine {
		w.buf, w.skipping = nil, true
		return
	}
	w.buf = append(w.buf, chunk...)
}

func (w *lineWriter) emit() {
	if w.skipping {
		w.skipping = false
		// Not JSON, so the parser turns it into a log event.
		w.fn([]byte("[runner: dropped an output line longer than 16 MB]"))
	} else if len(w.buf) > 0 {
		w.fn(w.buf)
	}
	w.buf = w.buf[:0]
}

// flush hands on a last line that had no newline.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 || w.skipping {
		w.emit()
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(t.buf), ""))
}

// runCfg is the config r was offered under (run.cfg), or the one in effect
// for a run made without one.
func (j *jobs) runCfg(r *run) *Config {
	if r.cfg != nil {
		return r.cfg
	}
	return j.c.cfg()
}
