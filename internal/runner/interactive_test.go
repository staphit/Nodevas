package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// fakeTerm is a Terminal driven by the test: emit is the program's output,
// exit ends it.
type fakeTerm struct {
	out    chan []byte
	exit   chan int
	closed chan struct{}
	once   sync.Once
	left   []byte

	mu     sync.Mutex
	input  bytes.Buffer
	sizes  [][2]int
	killed bool
}

func newFakeTerm() *fakeTerm {
	return &fakeTerm{out: make(chan []byte, 64), exit: make(chan int, 1), closed: make(chan struct{})}
}

func (f *fakeTerm) emit(s string) { f.out <- []byte(s) }

func (f *fakeTerm) Read(p []byte) (int, error) {
	if len(f.left) == 0 {
		// Buffered output first, as a real pipe delivers it before EOF;
		// a bare select would pick closed over waiting data half the time.
		select {
		case f.left = <-f.out:
		default:
			select {
			case f.left = <-f.out:
			case <-f.closed:
				return 0, io.EOF
			}
		}
	}
	n := copy(p, f.left)
	f.left = f.left[n:]
	return n, nil
}

func (f *fakeTerm) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.input.Write(p)
}

func (f *fakeTerm) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, [2]int{cols, rows})
	return nil
}

func (f *fakeTerm) Wait() (int, error) { return <-f.exit, nil }

func (f *fakeTerm) Kill() {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	select {
	case f.exit <- -1:
	default:
	}
}

func (f *fakeTerm) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeTerm) typed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.input.String()
}

func (f *fakeTerm) resizes() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sizes)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pendingLen(s *termSession) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// collect flushes the relay into a slice of term_out messages.
func collect(t *testing.T, relay *termRelay) []runnerwire.Envelope {
	t.Helper()
	var msgs []runnerwire.Envelope
	if err := relay.flush(func(msg runnerwire.Envelope) error {
		msgs = append(msgs, msg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestTermRelayChunksAndSequences(t *testing.T) {
	var relay termRelay
	term := newFakeTerm()
	t.Cleanup(func() { _ = term.Close() })
	s := relay.open("run-1", term)
	var states []string
	var mu sync.Mutex
	go s.readLoop(func(st string) { mu.Lock(); states = append(states, st); mu.Unlock() }, relay.wake)

	big := strings.Repeat("0123456789abcdef", (2*runnerwire.MaxTermChunk+100)/16)
	term.emit("\x1b]7501;{\"state\":\"working\"}\x07")
	term.emit("\x1b]7501;work")
	term.emit("ing\x1b\\") // the same state, split: reported once
	term.emit(big)
	term.emit("\x1b]7501;idle\x07")
	total := len(big) + len("\x1b]7501;{\"state\":\"working\"}\x07") + len("\x1b]7501;working\x1b\\") + len("\x1b]7501;idle\x07")
	eventually(t, "output read", func() bool { return pendingLen(s) == total })

	msgs := collect(t, &relay)
	var all []byte
	for i, msg := range msgs {
		if msg.Type != runnerwire.TypeTermOut || msg.RunID != "run-1" || msg.Term == nil {
			t.Fatalf("message %d = %+v", i, msg)
		}
		if msg.Term.Seq != int64(i+1) {
			t.Fatalf("message %d has seq %d", i, msg.Term.Seq)
		}
		if len(msg.Term.Data) > runnerwire.MaxTermChunk {
			t.Fatalf("chunk of %d bytes", len(msg.Term.Data))
		}
		all = append(all, msg.Term.Data...)
	}
	if len(msgs) < 3 || !strings.Contains(string(all), big) || len(all) != total {
		t.Fatalf("%d messages, %d bytes", len(msgs), len(all))
	}
	mu.Lock()
	got := slices.Clone(states)
	mu.Unlock()
	if !slices.Equal(got, []string{"working", "idle"}) {
		t.Fatalf("states = %q", got)
	}

	// More output continues the numbering.
	sent := len(msgs)
	term.emit("more")
	eventually(t, "more output", func() bool { return pendingLen(s) == 4 })
	msgs = collect(t, &relay)
	if len(msgs) != 1 || msgs[0].Term.Seq != int64(sent+1) || string(msgs[0].Term.Data) != "more" {
		t.Fatalf("after: %+v", msgs)
	}
}

func TestTermRelayFailedWriteKeepsOutput(t *testing.T) {
	var relay termRelay
	term := newFakeTerm()
	t.Cleanup(func() { _ = term.Close() })
	s := relay.open("run-1", term)
	go s.readLoop(func(string) {}, relay.wake)
	term.emit("screen")
	eventually(t, "output", func() bool { return pendingLen(s) == 6 })
	if err := relay.flush(func(runnerwire.Envelope) error { return io.ErrClosedPipe }); err == nil {
		t.Fatal("flush hid the write error")
	}
	msgs := collect(t, &relay)
	if len(msgs) != 1 || msgs[0].Term.Seq != 1 || string(msgs[0].Term.Data) != "screen" {
		t.Fatalf("after reconnect: %+v", msgs)
	}
}

func TestTermRelayDropsOldestWhileUnreachable(t *testing.T) {
	var relay termRelay
	s := relay.open("run-1", newFakeTerm())
	chunk := bytes.Repeat([]byte("x"), 100<<10)
	for range 12 { // 1.2 MB, over maxPendingTerm
		s.output(chunk, func() {})
	}
	s.output([]byte("END"), func() {})
	msgs := collect(t, &relay)
	if len(msgs) < 2 || !strings.Contains(string(msgs[0].Term.Data), "bytes of output dropped") {
		t.Fatalf("first message = %q", msgs[0].Term.Data)
	}
	var rest []byte
	for _, msg := range msgs[1:] {
		rest = append(rest, msg.Term.Data...)
	}
	if len(rest) != maxPendingTerm || !bytes.HasSuffix(rest, []byte("END")) {
		t.Fatalf("kept %d bytes", len(rest))
	}
	for i, msg := range msgs {
		if msg.Term.Seq != int64(i+1) {
			t.Fatalf("seq %d at %d", msg.Term.Seq, i)
		}
	}
}

func TestTermInputAndResize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := newJobs(ctx, &Client{Config: &Config{}})
	term := newFakeTerm()
	s := j.terms.open("run-i", term)
	go s.writeLoop()

	// View only until remote typing is allowed.
	j.termInput("run-i", &runnerwire.TermChunk{Data: []byte("whoami\r")})
	s.typing.Store(true)
	j.termInput("run-i", &runnerwire.TermChunk{Data: []byte("ls\r")})
	j.termInput("run-i", &runnerwire.TermChunk{Data: bytes.Repeat([]byte("y"), runnerwire.MaxTermChunk+1)})
	j.termInput("run-i", &runnerwire.TermChunk{Data: []byte("q")})
	eventually(t, "input typed", func() bool { return term.typed() == "ls\rq" })

	j.termResize("run-i", &runnerwire.TermChunk{Cols: 1000, Rows: 1000})
	j.termResize("run-i", &runnerwire.TermChunk{Cols: 0, Rows: 30})
	j.termResize("run-i", &runnerwire.TermChunk{Cols: 100, Rows: 30})
	if got := term.resizes(); !slices.Equal(got, [][2]int{{runnerwire.MaxTermCols, runnerwire.MaxTermRows}, {100, 30}}) {
		t.Fatalf("resizes = %v", got)
	}

	// A run without a terminal (headless, or unknown): ignored, no panic.
	j.termInput("run-headless", &runnerwire.TermChunk{Data: []byte("rm -rf /\r")})
	j.termResize("run-headless", &runnerwire.TermChunk{Cols: 80, Rows: 24})
	j.termInput("run-i", nil)

	// Once the program is gone, input goes nowhere.
	close(s.stop)
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	j.termInput("run-i", &runnerwire.TermChunk{Data: []byte("late")})
	j.termResize("run-i", &runnerwire.TermChunk{Cols: 90, Rows: 20})
	time.Sleep(20 * time.Millisecond)
	if term.typed() != "ls\rq" || len(term.resizes()) != 2 {
		t.Fatalf("after exit: typed %q, resizes %v", term.typed(), term.resizes())
	}
}

func TestInteractiveSetting(t *testing.T) {
	cfg := &Config{}
	if !cfg.InteractiveOn() {
		t.Fatal("interactive is off by default")
	}
	if got := slices.Contains(capabilities(cfg), runnerwire.CapInteractive); got != ptySupported() {
		t.Fatalf("default: interactive capability %t, pty supported %t", got, ptySupported())
	}
	if err := cfg.Set("interactive", []string{"maybe"}); err == nil {
		t.Fatal("accepted maybe")
	}
	if err := cfg.Set("interactive", []string{"false"}); err != nil || cfg.InteractiveOn() {
		t.Fatalf("set false: %v", err)
	}
	if slices.Contains(capabilities(cfg), runnerwire.CapInteractive) {
		t.Fatal("interactive capability advertised while off")
	}
	if err := cfg.Set("interactive", []string{"default"}); err != nil || !cfg.InteractiveOn() || cfg.Interactive != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := cfg.Set("interactive", []string{"true"}); err != nil || !cfg.InteractiveOn() {
		t.Fatalf("set true: %v", err)
	}
	// Taking interactive runs by default changes nothing about who accepts
	// them, or whether the hub may type into them.
	if p := cfg.AutoAcceptPolicy(); p.Enabled || p.AllowRemoteTyping {
		t.Fatalf("default policy = %+v", p)
	}
	cfg.SetAutoAccept(true, Kinds)
	if p := cfg.AutoAcceptPolicy(); slices.Contains(p.Modes, runnerwire.ModeInteractive) || p.AllowRemoteTyping {
		t.Fatalf("autoaccept on: policy = %+v", p)
	}
}

func TestDescribeInteractiveOffer(t *testing.T) {
	job := testJob("run-1", "do it")
	if text := describeOffer(Offer{Job: job}); strings.Contains(text, "INTERACTIVE") {
		t.Fatalf("headless offer says interactive:\n%s", text)
	}
	job.Mode = runnerwire.ModeInteractive
	text := describeOffer(Offer{Job: job})
	if !strings.Contains(text, "INTERACTIVE run offered") || !strings.Contains(text, "only if you allow") {
		t.Fatalf("interactive offer:\n%s", text)
	}
}

// interactiveAgent is the jobs tests' fake agent plus the real adapter's
// interactive command.
type interactiveAgent struct{ fakeAgent }

func (a interactiveAgent) InteractiveCommand(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, error) {
	cmd, err := a.real.(adapter.Interactive).InteractiveCommand(job, env)
	if err == nil && len(cmd.Args) >= 4 && cmd.Args[1] == "--session-id" {
		sid, prompt := cmd.Args[2], cmd.Args[3]
		cmd.Args[1], cmd.Args[2], cmd.Args[3] = prompt, "--session-id", sid
	}
	return cmd, err
}

type startedTerm struct {
	cmd        *exec.Cmd
	cols, rows int
	term       *fakeTerm
}

// startInteractiveRunner is startJobRunner with interactive agents and a fake
// terminal; started receives each terminal as it starts.
func startInteractiveRunner(t *testing.T, tweak func(*Client)) (*jobEnv, chan startedTerm) {
	started := make(chan startedTerm, 4)
	env := startJobRunner(t, func(c *Client) {
		on := true
		c.Config.Interactive = &on
		prev := c.Adapter
		c.Adapter = func(kind string) adapter.Adapter {
			if a, ok := prev(kind).(fakeAgent); ok {
				return interactiveAgent{a}
			}
			return nil
		}
		c.NewTerminal = func(cmd *exec.Cmd, cols, rows int) (Terminal, error) {
			term := newFakeTerm()
			started <- startedTerm{cmd: cmd, cols: cols, rows: rows, term: term}
			return term, nil
		}
		if tweak != nil {
			tweak(c)
		}
	})
	return env, started
}

func interactiveJob(runID string) runnerwire.Job {
	job := testJob(runID, "ok: fix it")
	job.Mode = runnerwire.ModeInteractive
	return job
}

func waitStarted(t *testing.T, started chan startedTerm) startedTerm {
	t.Helper()
	select {
	case st := <-started:
		return st
	case <-time.After(20 * time.Second):
		t.Fatal("no terminal started")
	}
	return startedTerm{}
}

func termOutput(env *jobEnv, runID string) (string, []int64) {
	var data strings.Builder
	var seqs []int64
	for _, msg := range env.hub.messages(runID) {
		if msg.Type == runnerwire.TypeTermOut && msg.Term != nil {
			data.Write(msg.Term.Data)
			seqs = append(seqs, msg.Term.Seq)
		}
	}
	return data.String(), seqs
}

func hubHasEvent(env *jobEnv, runID, text string) bool {
	for _, msg := range env.hub.messages(runID) {
		for _, ev := range msg.Events {
			if strings.Contains(ev.Text, text) {
				return true
			}
		}
	}
	return false
}

// typingAccepter says yes, and yes or no to remote typing.
type typingAccepter struct {
	acceptFunc
	typing bool
}

func (a typingAccepter) AllowRemoteTyping(context.Context, Offer) bool { return a.typing }

func TestInteractiveRun(t *testing.T) {
	offered := make(chan Offer, 1)
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.Accepter = typingAccepter{typing: true, acceptFunc: func(_ context.Context, o Offer) (bool, string) {
			offered <- o
			return true, ""
		}}
	})
	hello := env.hub.hello()
	if got := slices.Contains(hello.Hello.Capabilities, runnerwire.CapInteractive); got != ptySupported() {
		t.Fatalf("hello capabilities %q (pty supported %t)", hello.Hello.Capabilities, ptySupported())
	}
	env.hub.offer(interactiveJob("run-int"))
	st := waitStarted(t, started)
	env.hub.wait("run-int", runnerwire.TypeAccept)
	if o := <-offered; o.Job.Mode != runnerwire.ModeInteractive {
		t.Fatalf("accepter saw mode %q", o.Job.Mode)
	}
	if st.cols != initialCols || st.rows != initialRows {
		t.Fatalf("initial size %dx%d", st.cols, st.rows)
	}
	if !strings.Contains(strings.Join(st.cmd.Args, " "), "--strict-mcp-config") || slices.Contains(st.cmd.Args, "-p") {
		t.Fatalf("command %q", st.cmd.Args)
	}

	st.term.emit("Welcome to the agent\r\n\x1b]7501;{\"state\":\"working\"}\x07")
	st.term.emit("\x1b[1mworking on it\x1b[0m\r\n")
	eventually(t, "term_out at the hub", func() bool {
		out, _ := termOutput(env, "run-int")
		return strings.Contains(out, "working on it")
	})
	eventually(t, "agent state event", func() bool { return hubHasEvent(env, "run-int", "agent state: working") })

	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeTermIn, RunID: "run-int",
		Term: &runnerwire.TermChunk{Data: []byte("yes\r")}})
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeTermResize, RunID: "run-int",
		Term: &runnerwire.TermChunk{Cols: 900, Rows: 50}})
	eventually(t, "input typed", func() bool { return st.term.typed() == "yes\r" })
	eventually(t, "resize", func() bool {
		return slices.Contains(st.term.resizes(), [2]int{runnerwire.MaxTermCols, 50})
	})

	st.term.emit("All done.\r\n")
	st.term.exit <- 0
	finish := env.hub.wait("run-int", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.ExitCode != 0 {
		t.Fatalf("finish = %+v", finish.Result)
	}
	if !strings.HasPrefix(finish.Result.Summary, "Interactive session ended") ||
		!strings.Contains(finish.Result.Summary, "All done.") || strings.Contains(finish.Result.Summary, "\x1b") {
		t.Fatalf("summary = %q", finish.Result.Summary)
	}
	out, seqs := termOutput(env, "run-int")
	if !strings.Contains(out, "All done.") {
		t.Fatalf("last output missing: %q", out)
	}
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("seqs = %v", seqs)
		}
	}
	// The last screen went out before the finish.
	msgs := env.hub.messages("run-int")
	lastOut, finishAt := -1, -1
	for i, msg := range msgs {
		switch msg.Type {
		case runnerwire.TypeTermOut:
			lastOut = i
		case runnerwire.TypeFinish:
			finishAt = i
		}
	}
	if lastOut > finishAt {
		t.Fatalf("term_out after finish: %d > %d", lastOut, finishAt)
	}
	if !hubHasEvent(env, "run-int", "interactively") || !hubHasEvent(env, "run-int", "exited with code 0") {
		t.Fatal("start/exit events missing")
	}
}

func TestInteractiveRunCancelAndFailure(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) { c.Config.MaxConcurrent = 2 })
	env.hub.hello()

	env.hub.offer(interactiveJob("run-cancel"))
	st := waitStarted(t, started)
	env.hub.wait("run-cancel", runnerwire.TypeAccept)
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-cancel", Reason: "stop it"})
	finish := env.hub.wait("run-cancel", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunCancelled || finish.Result.Error != "stop it" {
		t.Fatalf("cancel finish = %+v", finish.Result)
	}
	st.term.mu.Lock()
	killed := st.term.killed
	st.term.mu.Unlock()
	if !killed {
		t.Fatal("terminal not killed")
	}

	env.hub.offer(interactiveJob("run-fail"))
	st = waitStarted(t, started)
	st.term.exit <- 2
	finish = env.hub.wait("run-fail", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunFailed || finish.Result.ExitCode != 2 ||
		!strings.Contains(finish.Result.Error, "exited with code 2") {
		t.Fatalf("fail finish = %+v", finish.Result)
	}
}

func TestInteractiveOfferDeclined(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) {
		off := false
		c.Config.Interactive = &off
	})
	hello := env.hub.hello()
	if slices.Contains(hello.Hello.Capabilities, runnerwire.CapInteractive) {
		t.Fatal("interactive capability while off")
	}
	env.hub.offer(interactiveJob("run-off"))
	decline := env.hub.wait("run-off", runnerwire.TypeDecline)
	if !strings.Contains(decline.Reason, "does not take interactive runs") {
		t.Fatalf("decline = %q", decline.Reason)
	}

	job := testJob("run-mode", "ok: x")
	job.Mode = "telepathic"
	env.hub.offer(job)
	decline = env.hub.wait("run-mode", runnerwire.TypeDecline)
	if !strings.Contains(decline.Reason, "telepathic") {
		t.Fatalf("decline = %q", decline.Reason)
	}
	select {
	case <-started:
		t.Fatal("a terminal started for a declined run")
	default:
	}
}

// Accepted by a person who did not allow remote typing: the hub watches, and
// what it types never reaches the agent.
func TestInteractiveRunViewOnly(t *testing.T) {
	for _, accepter := range []Accepter{
		acceptFunc(func(context.Context, Offer) (bool, string) { return true, "" }),
		typingAccepter{acceptFunc: func(context.Context, Offer) (bool, string) { return true, "" }},
	} {
		env, started := startInteractiveRunner(t, func(c *Client) { c.Accepter = accepter })
		env.hub.hello()
		env.hub.offer(interactiveJob("run-view"))
		st := waitStarted(t, started)
		env.hub.wait("run-view", runnerwire.TypeAccept)
		eventually(t, "view-only event", func() bool { return hubHasEvent(env, "run-view", "view only") })
		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeTermIn, RunID: "run-view",
			Term: &runnerwire.TermChunk{Data: []byte("rm -rf ~\r")}})
		time.Sleep(100 * time.Millisecond)
		if typed := st.term.typed(); typed != "" {
			t.Fatalf("view-only terminal got %q", typed)
		}
		st.term.exit <- 0
		env.hub.wait("run-view", runnerwire.TypeFinish)
	}
}

// Interactive is on by default, and an interactive offer is still the
// owner's to accept: auto-accept with its default modes (headless) does not
// cover it, so it is asked about at the terminal, and a no there declines it
// before any terminal starts.
func TestInteractiveDefaultStillAsks(t *testing.T) {
	asked := make(chan Offer, 1)
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.Config.Interactive = nil
		c.Config.AutoAccept = &runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
			Repos: []string{"r1"}, Agents: []string{"claude"}}
		c.Accepter = acceptFunc(func(_ context.Context, o Offer) (bool, string) {
			asked <- o
			return false, ReasonDeclined
		})
	})
	hello := env.hub.hello()
	if got := slices.Contains(hello.Hello.Capabilities, runnerwire.CapInteractive); got != ptySupported() {
		t.Fatalf("hello capabilities %q (pty supported %t)", hello.Hello.Capabilities, ptySupported())
	}
	job := interactiveJob("run-ask")
	job.SteppedUp = true
	env.hub.offer(job)
	decline := env.hub.wait("run-ask", runnerwire.TypeDecline)
	if decline.Reason != ReasonDeclined {
		t.Fatalf("decline = %q", decline.Reason)
	}
	select {
	case o := <-asked:
		if o.Job.Mode != runnerwire.ModeInteractive {
			t.Fatalf("asked about %+v", o.Job)
		}
	default:
		t.Fatal("an interactive offer was decided without asking")
	}
	select {
	case <-started:
		t.Fatal("a terminal started for a declined run")
	default:
	}
}

func TestInteractiveDeclinedWithoutInteractiveAdapter(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		on := true
		c.Config.Interactive = &on
		c.NewTerminal = func(*exec.Cmd, int, int) (Terminal, error) { return newFakeTerm(), nil }
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-noad"))
	decline := env.hub.wait("run-noad", runnerwire.TypeDecline)
	if !strings.Contains(decline.Reason, "cannot run interactively") {
		t.Fatalf("decline = %q", decline.Reason)
	}
}

// An interactive run never relays a permission prompt: the person at the
// terminal answers it. Its start line does not say otherwise, whatever the
// owner's permissionRelay.
func TestInteractiveRunNeverSaysRelayed(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.Config.PermissionRelay = true
		c.Accepter = typingAccepter{typing: true, acceptFunc: func(context.Context, Offer) (bool, string) { return true, "" }}
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-int-relay"))
	st := waitStarted(t, started)
	eventually(t, "start line", func() bool { return hubHasEvent(env, "run-int-relay", "interactively") })
	if hubHasEvent(env, "run-int-relay", "relayed") {
		t.Error("an interactive run's log says its prompts are relayed")
	}
	st.term.exit <- 0
	env.hub.wait("run-int-relay", runnerwire.TypeFinish)
}

// End sends the CLI's graceful quit sequence (Ctrl+C spaced 300ms apart) and
// waits for the process to exit on its own; only if it ignores the quit past
// the grace period is the process tree killed. Either way it finishes succeeded
// with EndReason "ended".
func TestEndQuitsGracefullyThenKills(t *testing.T) {
	t.Run("graceful", func(t *testing.T) {
		env, started := startInteractiveRunner(t, func(c *Client) {
			c.endQuitGap = 20 * time.Millisecond
			c.endGrace = 500 * time.Millisecond
		})
		env.hub.hello()
		env.hub.offer(interactiveJob("run-end-graceful"))
		st := waitStarted(t, started)
		env.hub.wait("run-end-graceful", runnerwire.TypeAccept)

		// Simulate Claude/Agy exiting on the second Ctrl+C (even with exit code 1,
		// as Claude's trust screen exits with code 1 on double Ctrl+C).
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if strings.Count(st.term.typed(), "\x03") >= 2 {
					st.term.exit <- 1
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()

		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-end-graceful"})
		finish := env.hub.wait("run-end-graceful", runnerwire.TypeFinish)
		if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonEnded || finish.Result.Error != "" {
			t.Fatalf("graceful end finish = %+v", finish.Result)
		}
		st.term.mu.Lock()
		killed := st.term.killed
		st.term.mu.Unlock()
		if killed {
			t.Fatal("graceful exit should not hard-kill the terminal")
		}
		if got := strings.Count(st.term.typed(), "\x03"); got != 2 {
			t.Fatalf("expected 2 Ctrl+C presses before exit, got %d (%q)", got, st.term.typed())
		}
	})

	t.Run("kills after grace", func(t *testing.T) {
		env, started := startInteractiveRunner(t, func(c *Client) {
			c.endQuitGap = 15 * time.Millisecond
			c.endGrace = 60 * time.Millisecond
		})
		env.hub.hello()
		env.hub.offer(interactiveJob("run-end-kill"))
		st := waitStarted(t, started)
		env.hub.wait("run-end-kill", runnerwire.TypeAccept)

		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-end-kill"})
		finish := env.hub.wait("run-end-kill", runnerwire.TypeFinish)
		if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonEnded || finish.Result.Error != "" {
			t.Fatalf("kill-after-grace finish = %+v", finish.Result)
		}
		st.term.mu.Lock()
		killed := st.term.killed
		st.term.mu.Unlock()
		if !killed {
			t.Fatal("stubborn terminal was not killed after grace period")
		}
		if got := strings.Count(st.term.typed(), "\x03"); got != 3 {
			t.Fatalf("expected 3 Ctrl+C presses before kill, got %d (%q)", got, st.term.typed())
		}
	})
}

// An End arriving before the PTY opens is recorded as pendingEnd and applied
// as soon as the terminal starts, rather than being silently dropped.
func TestEndBeforeTerminalOpensIsKept(t *testing.T) {
	entering := make(chan struct{}, 1)
	release := make(chan struct{})
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.endQuitGap = 15 * time.Millisecond
		c.endGrace = 50 * time.Millisecond
		orig := c.NewTerminal
		c.NewTerminal = func(cmd *exec.Cmd, cols, rows int) (Terminal, error) {
			entering <- struct{}{}
			<-release
			return orig(cmd, cols, rows)
		}
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-early-end"))
	env.hub.wait("run-early-end", runnerwire.TypeAccept)

	select {
	case <-entering:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for NewTerminal to be called")
	}

	// Send End before NewTerminal returns and before terms.open runs.
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-early-end"})
	eventually(t, "pendingEnd set on run", func() bool {
		env.client.jobs.mu.Lock()
		defer env.client.jobs.mu.Unlock()
		r := env.client.jobs.runs["run-early-end"]
		return r != nil && r.pendingEnd
	})

	close(release)
	st := waitStarted(t, started)
	finish := env.hub.wait("run-early-end", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonEnded || finish.Result.Error != "" {
		t.Fatalf("early end finish = %+v", finish.Result)
	}
	if strings.Count(st.term.typed(), "\x03") == 0 {
		t.Fatalf("pending end did not send quit sequence; typed = %q", st.term.typed())
	}
}

// Ending an interactive run publishes its worktree edits as a succeeded run,
// even when the process had to be killed after the grace period (exit code -1):
// no -1 failure is shown.
func TestEndPublishesAnInteractiveRunAsSucceeded(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-end-pub"))
	st := waitStarted(t, started)
	env.hub.wait("run-end-pub", runnerwire.TypeAccept)

	// Simulate edits made by the interactive agent inside its worktree.
	if err := os.WriteFile(filepath.Join(st.cmd.Dir, "note.txt"), []byte("kept work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.term.emit("Updated note.txt\r\n")
	eventually(t, "term_out received", func() bool {
		out, _ := termOutput(env, "run-end-pub")
		return strings.Contains(out, "Updated note.txt")
	})

	// Do not exit on Ctrl+C so the grace period hard-kills with exitCode -1.
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-end-pub"})
	finish := env.hub.wait("run-end-pub", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonEnded || finish.Result.Error != "" {
		t.Fatalf("finish = %+v", finish.Result)
	}
	if hubHasEvent(env, "run-end-pub", "exited with code -1") {
		t.Fatal("End logged an 'exited with code -1' failure event")
	}

	var changes *runnerwire.RunChanges
	for _, msg := range env.hub.messages("run-end-pub") {
		if msg.Type == runnerwire.TypeChanges && msg.Changes != nil {
			changes = msg.Changes
		}
	}
	if changes == nil || changes.Commit == "" {
		t.Fatalf("expected published commit on End, got changes = %+v", changes)
	}
}

// Quiet timeout is fixed at 20 minutes by default and ends an idle interactive
// session gracefully as succeeded with EndReason "quiet" and a log line.
func TestQuietTimeoutEndsAfter20Minutes(t *testing.T) {
	if interactiveQuiet != 20*time.Minute {
		t.Fatalf("interactiveQuiet = %v, want 20m", interactiveQuiet)
	}
	if got := (&Client{}).quietTimeout(); got != 20*time.Minute {
		t.Fatalf("default Client.quietTimeout() = %v, want 20m", got)
	}

	env, started := startInteractiveRunner(t, func(c *Client) {
		c.interactiveQuietTimeout = 60 * time.Millisecond
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-quiet"))
	st := waitStarted(t, started)
	env.hub.wait("run-quiet", runnerwire.TypeAccept)

	st.term.emit("Ready.\r\n❯ ")
	finish := env.hub.wait("run-quiet", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonQuiet || finish.Result.Error != "" {
		t.Fatalf("quiet finish = %+v", finish.Result)
	}
	if !hubHasEvent(env, "run-quiet", "inactivity") {
		t.Fatal("quiet timeout did not emit an inactivity log event")
	}
	if strings.Count(st.term.typed(), "\x03") == 0 {
		t.Fatalf("quiet timeout did not send quit sequence; typed = %q", st.term.typed())
	}
}

// Typing into the terminal counts as activity and resets the quiet timer even
// when the program produces no output.
func TestQuietCountsInputToo(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.interactiveQuietTimeout = 90 * time.Millisecond
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
		c.Accepter = typingAccepter{typing: true, acceptFunc: func(context.Context, Offer) (bool, string) { return true, "" }}
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-quiet-in"))
	st := waitStarted(t, started)
	env.hub.wait("run-quiet-in", runnerwire.TypeAccept)

	// Keep sending keystrokes with no PTY output for well past the 90ms timeout.
	for range 6 {
		time.Sleep(30 * time.Millisecond)
		env.hub.send(runnerwire.Envelope{
			Type:  runnerwire.TypeTermIn,
			RunID: "run-quiet-in",
			Term:  &runnerwire.TermChunk{Data: []byte("a")},
		})
	}
	eventually(t, "all keystrokes typed", func() bool { return len(st.term.typed()) >= 6 })
	for _, msg := range env.hub.messages("run-quiet-in") {
		if msg.Type == runnerwire.TypeFinish {
			t.Fatalf("session ended while user input was still arriving: %+v", msg.Result)
		}
	}

	// Once input stops, the quiet timeout fires.
	finish := env.hub.wait("run-quiet-in", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonQuiet {
		t.Fatalf("quiet finish after input stopped = %+v", finish.Result)
	}
}

// When the screen is showing a CLI question/permission/trust menu (including
// Ink CSI cursor-forward spaces), quiet timeout reports EndReason "quiet_waiting".
func TestQuietWhileMenuShownIsQuietWaiting(t *testing.T) {
	// Unit-check menu markers and idle false-positive guards across all 4 CLIs.
	cases := []struct {
		name  string
		agent string
		raw   string
		want  bool
	}{
		{"claude trust with CSI spaces", "claude", "Quick\x1b[1Csafety\x1b[1Ccheck\r\n❯\x1b[1C1.\x1b[1CYes,\x1b[1CI\x1b[1Ctrust\x1b[1Cthis\x1b[1Cfolder\r\nEnter\x1b[1Cto\x1b[1Cconfirm · Esc\x1b[1Cto\x1b[1Ccancel\r\n", true},
		{"claude permission", "claude", "Do you want to proceed?\r\n❯ Yes\r\n  No\r\n", true},
		{"claude idle prompt", "claude", "Done.\r\n❯ \r\n  shift+tab to cycle\r\n", false},
		{"codex trust menu", "codex", "Do you trust the contents of this directory?\r\n› 1. Yes, continue\r\nPress enter to continue\r\n", true},
		{"codex approval", "codex", "Would you like to run npm test?\r\nApprove  Deny\r\n", true},
		{"codex idle prompt", "codex", "Ready\r\n› Ask Codex to do anything\r\n", false},
		{"opencode update modal", "opencode", "Update Available\r\nA new release v2.3.2 is available.\r\nSkip  Confirm\r\n", true},
		{"opencode permission", "opencode", "Permission required\r\nAllow once  Allow always  Reject\r\n", true},
		{"opencode idle prompt", "opencode", "Ask anything...\r\ntab agents  ctrl+p commands\r\n", false},
		{"agy trust screen", "antigravity", "Do you trust the contents of this project?\r\n> Yes, I trust this folder\r\n↑/↓ Navigate · enter Confirm\r\n", true},
		{"agy idle prompt", "antigravity", "Ready\r\n> \r\n? for shortcuts\r\n", false},
	}
	for _, tc := range cases {
		if got := screenWaiting(tc.agent, []byte(tc.raw)); got != tc.want {
			t.Errorf("%s: screenWaiting(%q) = %v, want %v", tc.name, tc.agent, got, tc.want)
		}
	}

	env, started := startInteractiveRunner(t, func(c *Client) {
		c.interactiveQuietTimeout = 60 * time.Millisecond
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-quiet-wait"))
	st := waitStarted(t, started)
	env.hub.wait("run-quiet-wait", runnerwire.TypeAccept)

	st.term.emit("Quick safety check\r\n❯\x1b[1C1.\x1b[1CYes,\x1b[1CI\x1b[1Ctrust\x1b[1Cthis\x1b[1Cfolder\r\nEnter\x1b[1Cto\x1b[1Cconfirm\r\n")
	finish := env.hub.wait("run-quiet-wait", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonQuietWaiting {
		t.Fatalf("quiet_waiting finish = %+v", finish.Result)
	}
}

// Idle repaints (cursor show/hide, full-screen or prompt redraws with no new
// visible content) do not reset the quiet timeout.
func TestIdleRepaintDoesNotResetQuiet(t *testing.T) {
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.interactiveQuietTimeout = 100 * time.Millisecond
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-repaint"))
	st := waitStarted(t, started)
	env.hub.wait("run-repaint", runnerwire.TypeAccept)

	st.term.emit("Claude Code\r\nAll set.\r\n❯ ")
	stopRepaint := make(chan struct{})
	defer close(stopRepaint)
	go func() {
		repaints := []string{
			"\x1b[?25l\x1b[?25h",
			"\x1b[2J\x1b[HClaude Code\r\nAll set.\r\n❯ ",
			"\r\x1b[2K❯ ",
			"\x1b[1;1H\x1b[2KClaude Code",
		}
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-stopRepaint:
				return
			case <-tick.C:
				select {
				case st.term.out <- []byte(repaints[i%len(repaints)]):
				default:
				}
			}
		}
	}()

	finish := env.hub.wait("run-repaint", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonQuiet {
		t.Fatalf("repaint finish = %+v", finish.Result)
	}
}

// capturingInteractiveAgent wraps interactiveAgent to record the RunEnv passed
// to InteractiveCommand.
type capturingInteractiveAgent struct {
	interactiveAgent
	onEnv func(adapter.RunEnv)
}

func (a capturingInteractiveAgent) InteractiveCommand(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, error) {
	if a.onEnv != nil {
		a.onEnv(env)
	}
	return a.interactiveAgent.InteractiveCommand(job, env)
}

// Interactive Claude runs get a runner-generated UUID v4 passed via
// RunEnv.SessionID and recorded on r.session; other interactive agents do not.
func TestInteractiveClaudeGetsSessionID(t *testing.T) {
	envs := make(chan adapter.RunEnv, 4)
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.Config.MaxConcurrent = 2
		prev := c.Adapter
		c.Adapter = func(kind string) adapter.Adapter {
			ad := prev(kind)
			if ia, ok := ad.(interactiveAgent); ok {
				return capturingInteractiveAgent{
					interactiveAgent: ia,
					onEnv:            func(e adapter.RunEnv) { envs <- e },
				}
			}
			return ad
		}
	})
	env.hub.hello()

	env.hub.offer(interactiveJob("run-claude-sid"))
	st := waitStarted(t, started)
	env.hub.wait("run-claude-sid", runnerwire.TypeAccept)

	gotEnv := <-envs
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(gotEnv.SessionID) || !runnerwire.ValidSessionID(gotEnv.SessionID) {
		t.Fatalf("claude RunEnv.SessionID = %q, want valid UUID v4", gotEnv.SessionID)
	}
	env.client.jobs.mu.Lock()
	r := env.client.jobs.runs["run-claude-sid"]
	recorded := ""
	if r != nil {
		recorded = r.session
	}
	env.client.jobs.mu.Unlock()
	if recorded != gotEnv.SessionID {
		t.Fatalf("r.session = %q, want %q", recorded, gotEnv.SessionID)
	}
	st.term.exit <- 0
	env.hub.wait("run-claude-sid", runnerwire.TypeFinish)

	// A non-Claude interactive agent gets no runner-generated SessionID.
	codexJob := interactiveJob("run-codex-sid")
	codexJob.Agent = "codex"
	codexJob.Model = "default"
	env.hub.offer(codexJob)
	st2 := waitStarted(t, started)
	env.hub.wait("run-codex-sid", runnerwire.TypeAccept)
	gotCodexEnv := <-envs
	if gotCodexEnv.SessionID != "" {
		t.Fatalf("codex RunEnv.SessionID = %q, want empty", gotCodexEnv.SessionID)
	}
	env.client.jobs.mu.Lock()
	r2 := env.client.jobs.runs["run-codex-sid"]
	recorded2 := ""
	if r2 != nil {
		recorded2 = r2.session
	}
	env.client.jobs.mu.Unlock()
	if recorded2 != "" {
		t.Fatalf("codex r.session = %q, want empty", recorded2)
	}
	st2.term.exit <- 0
	env.hub.wait("run-codex-sid", runnerwire.TypeFinish)
}

// End for an unknown run ID (or a headless run) is ignored cleanly without
// panicking or affecting other runs.
func TestEndUnknownRunIgnored(t *testing.T) {
	env, started := startInteractiveRunner(t, nil)
	env.hub.hello()

	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-does-not-exist"})
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: ""})

	// Follow with a normal interactive run to verify the client loop stayed healthy.
	env.hub.offer(interactiveJob("run-after-unknown-end"))
	st := waitStarted(t, started)
	env.hub.wait("run-after-unknown-end", runnerwire.TypeAccept)
	st.term.exit <- 0
	finish := env.hub.wait("run-after-unknown-end", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != "" {
		t.Fatalf("finish = %+v", finish.Result)
	}
}
