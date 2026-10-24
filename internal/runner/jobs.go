package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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

	// Outbound, unsent, in this order. events is bounded: a run that talks a
	// lot while the socket is down loses its oldest events, never its finish.
	accept  *runnerwire.Accept
	seq     int64
	events  []runnerwire.RunEvent
	dropped int
	changes *runnerwire.RunChanges
	pr      *runnerwire.PRInfo
	finish  *runnerwire.RunResult
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

		watched:   map[string]*watchedPR{},
		watchErrs: map[string]string{},
		prPending: map[string]runnerwire.PRInfo{},

		merges:        map[string]mergeRecord{},
		mergeInFlight: map[string]bool{},
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

// active is the hello's Active list: accepted runs whose finish the hub has
// not had yet. A finished run is listed until its finish goes out, or the hub
// would fail it for being missing a moment before hearing it succeeded.
func (j *jobs) active() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var ids []string
	for _, id := range j.order {
		if r := j.runs[id]; r != nil && r.accepted {
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
			Reason: "this runner already ran or refused this run"})
		j.mu.Unlock()
		j.wake()
		j.c.logf("run %s: offered again after it ended; declined", job.RunID)
		return
	}
	repo, reason := j.checkLocked(job)
	if reason != "" {
		j.rememberLocked(job.RunID)
		j.declines = append(j.declines, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: job.RunID, Reason: reason})
		j.mu.Unlock()
		j.wake()
		j.c.logf("run %s: declined: %s", job.RunID, reason)
		return
	}
	ctx, cancel := context.WithCancelCause(j.ctx)
	r := &run{job: *job, repo: repo, cancel: cancel}
	j.runs[job.RunID] = r
	j.order = append(j.order, job.RunID)
	j.wg.Add(1)
	j.mu.Unlock()
	j.c.logf("run %s: offered by %s: node %s %q in %s with %s/%s",
		job.RunID, orDash(job.RequestedBy), job.NodeID, job.NodeTitle, repo.Name, job.Agent, orDash(job.Model))
	go j.execute(ctx, r)
}

// checkLocked vets an offer against this runner's own settings. Everything
// the hub sends is a request; the allowlist, the agents and the limits here
// are what decide.
func (j *jobs) checkLocked(job *runnerwire.Job) (runnerwire.Repo, string) {
	cfg := j.c.Config
	if !validRunID.MatchString(job.RunID) {
		return runnerwire.Repo{}, "run id is not usable on this runner"
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
		return repo, fmt.Sprintf("repository %q is not on this runner's allowlist", job.RepoID)
	}
	if j.c.adapterFor(job.Agent) == nil {
		return repo, fmt.Sprintf("agent %q not supported yet", job.Agent)
	}
	// W2.5: interactive runs (interactive.go).
	if reason := j.checkMode(job); reason != "" {
		return repo, reason
	}
	var agent *runnerwire.Agent
	for i := range j.agents {
		if j.agents[i].Kind == job.Agent {
			agent = &j.agents[i]
		}
	}
	if agent == nil || !agent.Installed {
		return repo, fmt.Sprintf("agent %q is not installed on this runner", job.Agent)
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
		return repo, fmt.Sprintf("model %q is not one this runner offers for %s", model, job.Agent)
	}
	if job.Base != "" {
		if err := checkBase(job.Base); err != nil {
			return repo, err.Error()
		}
	}
	if len(job.Prompt) > runnerwire.MaxPromptBytes {
		return repo, "prompt is too long"
	}
	if reason := checkKind(job, cfg); reason != "" {
		return repo, reason
	}
	busy := 0
	for _, r := range j.runs {
		if r.finish == nil {
			busy++
		}
	}
	if limit := cfg.MaxConcurrentRuns(); busy >= limit {
		return repo, fmt.Sprintf("runner is busy: it runs at most %d at once", limit)
	}
	return repo, ""
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
	if job.Kind == runnerwire.JobReview || job.Kind == runnerwire.JobRevise {
		var reason string
		check, reason = j.checkPR(ctx, r)
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		if reason != "" {
			j.decline(r, reason)
			return
		}
	}

	ok, reason := j.ask(ctx, r)
	if ctx.Err() != nil {
		j.finish(r, cancelled(ctx))
		return
	}
	if !ok {
		j.decline(r, reason)
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
	}

	dataDir, err := j.c.Config.RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = CreateWorktree(ctx, r.repo.Path, dataDir, job.RunID, job.NodeID, job.Base)
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
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: wt.Base, Branch: wt.Branch}
	j.mu.Unlock()
	j.wake()
	j.c.logf("run %s: accepted; worktree %s on %s from %s", job.RunID, wt.Path, wt.Branch, wt.BaseRef)
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("worktree %s on branch %s from %s", wt.Path, wt.Branch, wt.BaseRef)})

	result, _ := j.runAgent(ctx, r, wt, job)
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
	changes, pr := j.publish(r, wt, result)
	j.finishWith(r, result, changes, pr)
}

// ask decides an offer: auto-accepted when the owner's policy covers it,
// else asked at the terminal. With nobody there, the decline says what kept
// auto-accept from applying.
func (j *jobs) ask(ctx context.Context, r *run) (bool, string) {
	offer := Offer{Job: r.job, Repo: r.repo, Shell: j.c.Config.ShellAccess(r.job)}
	policy := j.c.Config.AutoAcceptPolicy()
	auto, why := AutoAcceptDecision(policy, offer)
	interactive := r.job.Mode == runnerwire.ModeInteractive
	if auto {
		j.c.logf("run %s: auto-accepted: %s", r.job.RunID, why)
		if interactive {
			j.setRemoteTyping(r, policy.AllowRemoteTyping)
		}
		return true, ""
	}
	j.c.logf("run %s: not auto-accepted: %s; asking at the terminal", r.job.RunID, why)
	ok, reason := false, ReasonNoTerminal
	if j.c.Accepter != nil {
		ok, reason = j.c.Accepter.Accept(ctx, offer)
	}
	if ok && interactive {
		typing := false
		if asker, can := j.c.Accepter.(RemoteTypingAsker); can {
			typing = asker.AllowRemoteTyping(ctx, offer)
		}
		j.setRemoteTyping(r, typing)
	}
	if !ok && reason == ReasonNoTerminal && why != ReasonAutoAcceptOff {
		reason = why + " (and nobody is at the runner's terminal to accept)"
	}
	return ok, reason
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

// runAgent starts the agent in the worktree and waits for it, a cancel or the
// timeout, whichever comes first. job is r.job, or for a review the job with
// the PR's diff added to its prompt. text is the agent's whole final message
// (adapter.Outcome.Text), which a review needs beyond the summary's cap.
func (j *jobs) runAgent(ctx context.Context, r *run, wt Worktree, job runnerwire.Job) (runnerwire.RunResult, string) {
	// W2.5: the agent's TUI in a terminal instead (interactive.go).
	if job.Mode == runnerwire.ModeInteractive {
		return j.runInteractive(ctx, r, wt), ""
	}
	fail := func(kind, format string, args ...any) (runnerwire.RunResult, string) {
		return runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: kind,
			Error: adapter.Truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)}, ""
	}
	ad := j.c.adapterFor(job.Agent)
	bin, err := j.c.Commands.LookPath(adapter.Binary(job.Agent))
	if err != nil {
		return fail(runnerwire.FailureAgentAbsent, "%s is no longer on PATH", adapter.Binary(job.Agent))
	}
	proxy, err := j.proxyFor()
	if err != nil {
		return fail(runnerwire.FailureTask, "could not start the MCP proxy: %v", err)
	}
	secret := proxy.Register(job.RunID)
	defer proxy.Revoke(secret)
	tmp, err := os.MkdirTemp("", "nodevas-run-")
	if err != nil {
		return fail(runnerwire.FailureTask, "could not make a temporary directory: %v", err)
	}
	defer os.RemoveAll(tmp)
	exe, err := os.Executable()
	if err != nil {
		return fail(runnerwire.FailureTask, "cannot find the nodevas executable for the MCP server: %v", err)
	}

	cfg := j.c.Config
	mode, modeText := cfg.PermissionModeFor(job.Agent), permissionText(cfg, job.Agent)
	if job.Kind == runnerwire.JobReview || job.Kind == runnerwire.JobPlan {
		// A plan reads the code as a review does; what it writes goes on the
		// board, through MCP, not into files.
		mode, modeText = reviewPermission(cfg, job.Agent)
		if job.Kind == runnerwire.JobPlan {
			modeText = strings.Replace(modeText, "for a review", "for a plan", 1)
		}
	}
	cmd, err := ad.Command(job, adapter.RunEnv{
		Binary:         bin,
		Workdir:        wt.Path,
		Branch:         wt.Branch,
		TempDir:        tmp,
		MCP:            adapter.MCPServer{Command: exe, Args: mcpArgs(proxy.URL(secret), job, r.repo)},
		PermissionMode: mode,
		// Each adapter reads only the settings meant for it.
		AllowedTools:     cfg.AllowedTools,
		IgnoreUserConfig: cfg.CodexIgnoreUserConfig,
	})
	if err != nil {
		return fail(runnerwire.FailureTask, "%v", err)
	}
	parser := ad.NewParser()
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

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timeout := j.c.runTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	stopped := ""
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		stopped = "cancel"
		tree.kill()
		waitErr = <-done
	case <-timer.C:
		stopped = "timeout"
		tree.kill()
		waitErr = <-done
	}
	stdout.flush()

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
	}
	return result, outcome.Text
}

// reviewPermission is how freely a review's agent may act: read only where
// the CLI has such a mode (Codex's read-only sandbox; Gemini CLI's default
// approval mode, under which an edit needs an approval nobody can give
// headless; OpenCode with edits and the shell denied; agy's plan mode), the
// owner's setting otherwise. The prompt forbids edits either way, and the
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
	}
	return cfg.PermissionModeFor(kind), permissionText(cfg, kind)
}

// permissionText says how freely the agent may act, for the run's log.
func permissionText(cfg *Config, kind string) string {
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
// runner's proxy, as a worker, under a session name that says which run it is.
func mcpArgs(server string, job runnerwire.Job, repo runnerwire.Repo) []string {
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
	return []string{
		"mcp",
		"--server", server,
		"--project", job.Project,
		"--agent-role", "worker",
		"--actor", "run:" + job.Agent,
		"--session-name", repoName + "-" + job.Agent + "-" + slug(model) + "-" + runID,
	}
}

func (j *jobs) proxyFor() (*Proxy, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.proxy == nil && j.proxyErr == nil {
		j.proxy, j.proxyErr = StartProxy(j.c.Config, j.c.HTTPClient)
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
	ev.Text = adapter.Truncate(ev.Text, runnerwire.MaxEventText)
	ev.Input = adapter.Truncate(ev.Input, runnerwire.MaxEventText)
	j.mu.Lock()
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
	switch {
	case result.Status != runnerwire.RunFailed:
		result.FailureKind = ""
	case result.FailureKind == "":
		result.FailureKind = runnerwire.FailureTask
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

func (j *jobs) decline(r *run, reason string) {
	j.mu.Lock()
	j.forgetLocked(r.job.RunID)
	j.declines = append(j.declines, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: r.job.RunID, Reason: reason})
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
	// A merge's answer goes before the PR update that shows it merged.
	if len(j.mergeResults) > 0 {
		return j.mergeResults[0], func() { j.mergeResults = j.mergeResults[1:] }
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
