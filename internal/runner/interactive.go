package runner

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Interactive runs (W2.5; see runnerwire/terminal.go): the agent's own TUI in
// a pseudo-terminal, its screen relayed to the hub as term_out, keystrokes and
// resizes from the hub written back. Everything else (offer, acceptance,
// worktree, commit, PR, finish) is the headless run's.

const (
	// initialCols and initialRows size the terminal until the hub sends a
	// resize from a browser.
	initialCols = 120
	initialRows = 40
	// termCoalesce gathers output for this long before sending it, so a TUI's
	// redraw is one message rather than dozens.
	termCoalesce = 16 * time.Millisecond
	// maxPendingTerm bounds a run's unsent output while the hub is
	// unreachable; past it the oldest output is dropped.
	maxPendingTerm = 1 << 20
	// termInputQueue is how many keystroke chunks may wait for the program.
	termInputQueue = 256
	// termTail is how much recent raw output the end-of-session summary is
	// made from.
	termTail = 32 << 10
	// summaryLines is how many lines of the final screen the summary keeps.
	summaryLines = 20
	// termDrainQuiet / termDrainMax: after the program exits, its last output
	// is read until the terminal has been quiet this long, at most that long.
	termDrainQuiet = 150 * time.Millisecond
	termDrainMax   = 2 * time.Second
	// termCloseWait bounds waiting for the reader after the terminal closes.
	termCloseWait = 5 * time.Second
)

// capabilities is the hello's Capabilities: headless always, interactive when
// this machine has a pseudo-terminal and the owner has not turned it off.
func capabilities(cfg *Config) []string {
	caps := []string{runnerwire.CapHeadless, runnerwire.CapPlan}
	if (cfg == nil || cfg.InteractiveOn()) && ptySupported() {
		caps = append(caps, runnerwire.CapInteractive)
	}
	if _, _, reason := cfg.CopilotLLM(); reason == "" {
		caps = append(caps, runnerwire.CapLLM)
	}
	if _, _, reason := cfg.AdvisorLLM(); reason == "" {
		caps = append(caps, runnerwire.CapLLMAdvisor)
	}
	return caps
}

// checkMode vets an offer's run mode against this runner. "" means fine.
func (j *jobs) checkMode(job *runnerwire.Job) string {
	switch job.Mode {
	case runnerwire.ModeHeadless:
		return ""
	case runnerwire.ModeInteractive:
		if !j.c.Config.InteractiveOn() {
			return "this runner does not take interactive runs (nodevas runner set interactive true)"
		}
		if !j.c.terminalSupported() {
			return "this runner cannot run agents in a terminal on this OS"
		}
		if _, ok := j.c.adapterFor(job.Agent).(adapter.Interactive); !ok {
			return fmt.Sprintf("agent %q cannot run interactively on this runner", job.Agent)
		}
		return ""
	}
	return fmt.Sprintf("run mode %q is not supported by this runner", job.Mode)
}

// runInteractive is runAgent for an interactive run: it starts the agent's TUI
// in a terminal in the worktree and waits for it to exit (someone quit it), a
// cancel or the timeout.
func (j *jobs) runInteractive(ctx context.Context, r *run, wt Worktree) runnerwire.RunResult {
	job := r.job
	fail := func(format string, args ...any) runnerwire.RunResult {
		return runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)}
	}
	ia, ok := j.c.adapterFor(job.Agent).(adapter.Interactive)
	if !ok {
		return fail("agent %q cannot run interactively on this runner", job.Agent)
	}
	bin, err := j.c.Commands.LookPath(adapter.Binary(job.Agent))
	if err != nil {
		return fail("%s is no longer on PATH", adapter.Binary(job.Agent))
	}
	proxy, err := j.proxyFor()
	if err != nil {
		return fail("could not start the MCP proxy: %v", err)
	}
	secret := proxy.Register(job.RunID)
	defer proxy.Revoke(secret)
	tmp, err := os.MkdirTemp("", "nodevas-run-")
	if err != nil {
		return fail("could not make a temporary directory: %v", err)
	}
	defer os.RemoveAll(tmp)
	exe, err := os.Executable()
	if err != nil {
		return fail("cannot find the nodevas executable for the MCP server: %v", err)
	}

	cfg := j.c.Config
	cmd, err := ia.InteractiveCommand(job, adapter.RunEnv{
		Binary:         bin,
		Workdir:        wt.Path,
		Branch:         wt.Branch,
		TempDir:        tmp,
		MCP:            adapter.MCPServer{Command: exe, Args: mcpArgs(proxy.URL(secret), job, r.repo)},
		PermissionMode: cfg.PermissionModeFor(job.Agent),
		AllowedTools:   cfg.AllowedTools,
	})
	if err != nil {
		return fail("%v", err)
	}
	term, err := j.c.startTerminal(cmd, initialCols, initialRows)
	if err != nil {
		return fail("could not start %s in a terminal: %v", job.Agent, err)
	}
	s := j.terms.open(job.RunID, term)
	text := fmt.Sprintf("started %s interactively in a %dx%d terminal (model %s, %s)",
		job.Agent, initialCols, initialRows, orDash(job.Model), permissionText(cfg, job.Agent))
	if job.Agent == "codex" && cfg.CodexIgnoreUserConfig {
		text += "; codexIgnoreUserConfig does not apply to the interactive CLI"
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
	j.c.logf("run %s: %s", job.RunID, text)

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		s.readLoop(func(state string) {
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "agent state: " + state})
		}, j.terms.wake)
	}()
	go s.writeLoop()

	done := make(chan error, 1)
	exitCode := -1
	go func() {
		code, err := term.Wait()
		exitCode = code
		done <- err
	}()
	timeout := j.c.runTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	stopped := ""
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		stopped = "cancel"
		term.Kill()
		waitErr = <-done
	case <-timer.C:
		stopped = "timeout"
		term.Kill()
		waitErr = <-done
	}
	s.finish(readerDone)

	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("%s exited with code %d", job.Agent, exitCode)})
	result := runnerwire.RunResult{ExitCode: exitCode, Summary: s.summary()}
	switch {
	case stopped == "cancel":
		cancelledResult := cancelled(ctx)
		result.Status, result.Error = cancelledResult.Status, cancelledResult.Error
	case stopped == "timeout":
		result.Status = runnerwire.RunFailed
		result.Error = "timed out after " + minutesText(timeout)
	case exitCode == 0:
		result.Status = runnerwire.RunSucceeded
	default:
		result.Status = runnerwire.RunFailed
		result.Error = fmt.Sprintf("%s exited with code %d", job.Agent, exitCode)
		if waitErr != nil && exitCode < 0 {
			result.Error = waitErr.Error()
		}
		result.Error = adapter.Truncate(result.Error, runnerwire.MaxSummaryBytes)
	}
	return result
}

// termInput handles a TypeTermIn: keystrokes for an interactive run that is
// running. Anything else (a headless run, one that ended, an oversized chunk)
// is dropped.
func (j *jobs) termInput(runID string, chunk *runnerwire.TermChunk) {
	if chunk == nil || len(chunk.Data) == 0 {
		return
	}
	s := j.terms.get(runID)
	if s == nil {
		return
	}
	if len(chunk.Data) > runnerwire.MaxTermChunk {
		j.c.logf("run %s: dropped %d bytes of terminal input (over %d)", runID, len(chunk.Data), runnerwire.MaxTermChunk)
		return
	}
	if !s.input(chunk.Data) {
		j.c.logf("run %s: terminal input dropped: the agent is not reading it", runID)
	}
}

// termResize handles a TypeTermResize, clamped to the contract's bounds.
func (j *jobs) termResize(runID string, chunk *runnerwire.TermChunk) {
	if chunk == nil || chunk.Cols <= 0 || chunk.Rows <= 0 {
		return
	}
	s := j.terms.get(runID)
	if s == nil {
		return
	}
	cols, rows := min(chunk.Cols, runnerwire.MaxTermCols), min(chunk.Rows, runnerwire.MaxTermRows)
	if err := s.resize(cols, rows); err != nil {
		j.c.logf("run %s: resizing the terminal to %dx%d: %v", runID, cols, rows, err)
	}
}

// termRelay holds the terminals of interactive runs and their unsent output.
// Its zero value is ready to use.
type termRelay struct {
	mu       sync.Mutex
	sessions map[string]*termSession
	order    []string
	notify   chan struct{}
	once     sync.Once
}

// notifyCh signals output worth sending now.
func (t *termRelay) notifyCh() chan struct{} {
	t.once.Do(func() { t.notify = make(chan struct{}, 1) })
	return t.notify
}

func (t *termRelay) wake() {
	select {
	case t.notifyCh() <- struct{}{}:
	default:
	}
}

func (t *termRelay) open(runID string, term Terminal) *termSession {
	s := &termSession{runID: runID, term: term, relay: t,
		in: make(chan []byte, termInputQueue), stop: make(chan struct{})}
	s.tail.max = termTail
	s.lastOutput.Store(time.Now().UnixNano())
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessions == nil {
		t.sessions = map[string]*termSession{}
	}
	t.sessions[runID] = s
	t.order = append(t.order, runID)
	return s
}

func (t *termRelay) get(runID string) *termSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[runID]
}

// flush sends every run's pending output, oldest run first, in chunks of at
// most MaxTermChunk. A chunk leaves the queue only once written; the hub drops
// a chunk whose seq it already has. A run whose terminal closed is forgotten
// once its output is out.
func (t *termRelay) flush(write func(runnerwire.Envelope) error) error {
	t.mu.Lock()
	sessions := make([]*termSession, 0, len(t.order))
	for _, id := range t.order {
		sessions = append(sessions, t.sessions[id])
	}
	t.mu.Unlock()
	for _, s := range sessions {
		for {
			msg, commit := s.next()
			if commit == nil {
				break
			}
			if err := write(msg); err != nil {
				return err
			}
			commit()
		}
		if s.drained() {
			t.mu.Lock()
			if t.sessions[s.runID] == s {
				delete(t.sessions, s.runID)
				t.order = slices.DeleteFunc(t.order, func(id string) bool { return id == s.runID })
			}
			t.mu.Unlock()
		}
	}
	return nil
}

// termSession is one interactive run's terminal.
type termSession struct {
	runID string
	term  Terminal
	relay *termRelay
	in    chan []byte
	stop  chan struct{}
	// lastOutput is when output last arrived, as UnixNano.
	lastOutput atomic.Int64

	mu        sync.Mutex
	pending   []byte
	dropped   int
	dropGen   int // counts drops, so a chunk being written knows its bytes went
	seq       int64
	scheduled bool // a coalescing timer is running
	closed    bool // the program exited; no more input
	// ended: its last output has been read, so once pending is out the
	// session is done. Set after closed, at the end of finish: until then
	// the final screen may still be arriving.
	ended bool
	tail  tailBuffer
}

// readLoop copies the terminal's output into the pending buffer until the
// terminal closes, reporting OSC 7501 states (deduplicated) as it goes.
func (s *termSession) readLoop(state func(string), wake func()) {
	var scanner oscScanner
	last := ""
	buf := make([]byte, 32<<10)
	for {
		n, err := s.term.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.lastOutput.Store(time.Now().UnixNano())
			for _, payload := range scanner.feed(chunk) {
				if st := agentState(payload); st != "" && st != last {
					last = st
					state(st)
				}
			}
			s.output(chunk, wake)
		}
		if err != nil {
			return
		}
	}
}

// output queues a chunk of program output.
func (s *termSession) output(p []byte, wake func()) {
	s.mu.Lock()
	_, _ = s.tail.Write(p)
	s.pending = append(s.pending, p...)
	if over := len(s.pending) - maxPendingTerm; over > 0 {
		// The hub has been unreachable for a while: keep the newest output.
		s.pending = append(s.pending[:0:0], s.pending[over:]...)
		s.dropped += over
		s.dropGen++
	}
	now := len(s.pending) >= runnerwire.MaxTermChunk
	schedule := !now && !s.scheduled
	if schedule {
		s.scheduled = true
	}
	s.mu.Unlock()
	switch {
	case now:
		wake()
	case schedule:
		time.AfterFunc(termCoalesce, wake)
	}
}

// next is the session's next term_out message and what to do once it is
// written; commit is nil when there is nothing to send.
func (s *termSession) next() (runnerwire.Envelope, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduled = false
	if s.dropped > 0 {
		marker := fmt.Sprintf("\r\n\x1b[0m[nodevas: %d bytes of output dropped while the hub was unreachable]\r\n", s.dropped)
		seq := s.seq + 1
		return termOut(s.runID, seq, []byte(marker)), func() {
			s.mu.Lock()
			s.dropped, s.seq = 0, seq
			s.mu.Unlock()
		}
	}
	if len(s.pending) == 0 {
		return runnerwire.Envelope{}, nil
	}
	n := min(len(s.pending), runnerwire.MaxTermChunk)
	data := append([]byte(nil), s.pending[:n]...)
	seq := s.seq + 1
	gen := s.dropGen
	return termOut(s.runID, seq, data), func() {
		s.mu.Lock()
		// Output dropped while this was being written took the oldest
		// bytes, these among them; what is at the front now was not sent.
		if s.dropGen == gen {
			s.pending = append(s.pending[:0:0], s.pending[n:]...)
		}
		s.seq = seq
		s.mu.Unlock()
	}
}

func termOut(runID string, seq int64, data []byte) runnerwire.Envelope {
	return runnerwire.Envelope{Type: runnerwire.TypeTermOut, RunID: runID,
		Term: &runnerwire.TermChunk{Seq: seq, Data: data}}
}

// drained: the program is gone and all its output went out.
func (s *termSession) drained() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended && len(s.pending) == 0 && s.dropped == 0
}

// input queues keystrokes for the program without blocking; false means the
// queue is full and they were dropped.
func (s *termSession) input(data []byte) bool {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return true
	}
	select {
	case s.in <- append([]byte(nil), data...):
		return true
	default:
		return false
	}
}

// writeLoop types queued input into the program, until the session ends.
func (s *termSession) writeLoop() {
	for {
		select {
		case <-s.stop:
			return
		case data := <-s.in:
			if _, err := s.term.Write(data); err != nil {
				return
			}
		}
	}
}

func (s *termSession) resize(cols, rows int) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	return s.term.Resize(cols, rows)
}

// finish runs once the program has exited: it reads the last of its output,
// closes the terminal and stops taking input.
func (s *termSession) finish(readerDone <-chan struct{}) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	// A pseudo-terminal does not report end of output when its program exits
	// (the runner holds it open), so wait for the output to go quiet — for at
	// least one quiet period from now, not from the last output: the program's
	// final screen can still be in flight when Wait returns.
	start := time.Now()
	deadline := start.Add(termDrainMax)
	for time.Now().Before(deadline) {
		select {
		case <-readerDone:
			deadline = time.Time{}
			continue
		case <-time.After(termDrainQuiet / 3):
		}
		last := time.Unix(0, s.lastOutput.Load())
		if last.Before(start) {
			last = start
		}
		if time.Since(last) >= termDrainQuiet {
			break
		}
	}
	closed := make(chan struct{})
	go func() { _ = s.term.Close(); close(closed) }()
	select {
	case <-readerDone:
	case <-time.After(termCloseWait):
	}
	select {
	case <-closed:
	case <-time.After(termCloseWait):
	}
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.relay.wake()
}

// summary is the run's summary: that the session ended, and the last lines of
// its screen as plain text.
func (s *termSession) summary() string {
	s.mu.Lock()
	raw := append([]byte(nil), s.tail.buf...)
	s.mu.Unlock()
	text := "Interactive session ended"
	if tail := lastLines(plainText(raw), summaryLines); tail != "" {
		text += "\n\n" + tail
	}
	return truncateTail(text, runnerwire.MaxSummaryBytes)
}

// truncateTail cuts s to at most n bytes, keeping the first line and the end.
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head, rest, _ := strings.Cut(s, "\n")
	keep := n - len(head) - len("\n?吒n")
	if keep <= 0 {
		return adapter.Truncate(s, n)
	}
	rest = rest[len(rest)-keep:]
	return head + "\n?吒n" + strings.ToValidUTF8(rest, "")
}
