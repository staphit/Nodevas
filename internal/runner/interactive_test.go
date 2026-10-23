package runner

import (
	"bytes"
	"context"
	"io"
	"os/exec"
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
	if err := cfg.Set("interactive", []string{"false"}); err != nil || cfg.InteractiveOn() {
		t.Fatalf("set false: %v", err)
	}
	if slices.Contains(capabilities(cfg), runnerwire.CapInteractive) {
		t.Fatal("interactive capability advertised while off")
	}
	if err := cfg.Set("interactive", []string{"maybe"}); err == nil {
		t.Fatal("accepted maybe")
	}
	if err := cfg.Set("interactive", []string{"default"}); err != nil || !cfg.InteractiveOn() {
		t.Fatalf("reset: %v", err)
	}
	if got := slices.Contains(capabilities(cfg), runnerwire.CapInteractive); got != ptySupported() {
		t.Fatalf("interactive capability %t, pty supported %t", got, ptySupported())
	}
}

func TestDescribeInteractiveOffer(t *testing.T) {
	job := testJob("run-1", "do it")
	if text := describeOffer(Offer{Job: job}); strings.Contains(text, "INTERACTIVE") {
		t.Fatalf("headless offer says interactive:\n%s", text)
	}
	job.Mode = runnerwire.ModeInteractive
	text := describeOffer(Offer{Job: job})
	if !strings.Contains(text, "INTERACTIVE run offered") || !strings.Contains(text, "can type into it") {
		t.Fatalf("interactive offer:\n%s", text)
	}
}

// interactiveAgent is the jobs tests' fake agent plus the real adapter's
// interactive command.
type interactiveAgent struct{ fakeAgent }

func (a interactiveAgent) InteractiveCommand(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, error) {
	return a.real.(adapter.Interactive).InteractiveCommand(job, env)
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

func TestInteractiveRun(t *testing.T) {
	offered := make(chan Offer, 1)
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(_ context.Context, o Offer) (bool, string) {
			offered <- o
			return true, ""
		})
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

func TestInteractiveDeclinedWithoutInteractiveAdapter(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		c.NewTerminal = func(*exec.Cmd, int, int) (Terminal, error) { return newFakeTerm(), nil }
	})
	env.hub.hello()
	env.hub.offer(interactiveJob("run-noad"))
	decline := env.hub.wait("run-noad", runnerwire.TypeDecline)
	if !strings.Contains(decline.Reason, "cannot run interactively") {
		t.Fatalf("decline = %q", decline.Reason)
	}
}
