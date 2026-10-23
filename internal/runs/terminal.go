package runs

// Interactive runs (W2.5): the hub relays a run's terminal between its runner
// and the browsers attached to GET /api/runs/:id/terminal. It keeps a little
// recent output so a browser that attaches late sees the screen, and nothing
// else: it never interprets the bytes, and it never runs anything.
//
// Output flows runner → hub → browsers and must never wait on a browser: a
// browser that cannot keep up is dropped (it may reconnect and get the
// scrollback again), the runner's socket is never held up. Input flows
// browser → hub → runner, only from a viewer allowed to type, only while the
// run is running.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

var (
	// ErrTermGone is an interactive run whose terminal is no longer kept: it
	// ended, and its output expired (or the hub restarted since).
	ErrTermGone = errors.New("the run's terminal output is no longer kept")
	// ErrTermBusy is a terminal with as many viewers as it takes.
	ErrTermBusy = errors.New("too many viewers on this terminal")
	// ErrTermClosed is a viewer the hub let go; see TermViewer.Reason.
	ErrTermClosed = errors.New("terminal viewer closed")
)

// Terminal limits.
const (
	// DefaultTermKeep is how long a run's output stays readable after the run
	// ends, so the final screen can still be looked at.
	DefaultTermKeep = 10 * time.Minute
	// MaxTermViewers caps the browsers attached to one run.
	MaxTermViewers = 16
	// termViewerQueueBytes and termViewerQueueFrames bound what may wait for
	// one browser. Past either, the browser is too slow and is dropped.
	termViewerQueueBytes  = 2 << 20
	termViewerQueueFrames = 256
	// termAlignWindow is how far past a scrollback cut the hub looks for a
	// line break to start the replay on, so it rarely begins mid-sequence.
	termAlignWindow = 4 << 10
)

// TermFrame is one message for a browser: raw output (Binary) or a JSON
// runnerwire.TermControl.
type TermFrame struct {
	Binary bool
	Data   []byte
}

// TermCloseReason says why the hub let a viewer go.
type TermCloseReason int

const (
	// TermOpen: the viewer has not been let go.
	TermOpen TermCloseReason = iota
	// TermSlow: the browser could not keep up with the output.
	TermSlow
	// TermEnded: the run ended and its output expired.
	TermEnded
)

// TermViewer is one attached browser. Its frames are read with Next.
type TermViewer struct {
	term    *Terminal
	canType bool

	frames chan TermFrame
	done   chan struct{}

	mu      sync.Mutex
	pending int
	reason  TermCloseReason
}

// CanType reports whether this viewer's input reaches the runner.
func (v *TermViewer) CanType() bool { return v.canType }

// Reason says why the viewer was let go, or TermOpen.
func (v *TermViewer) Reason() TermCloseReason {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reason
}

// Next returns the next frame for the browser, in order. It fails with
// ErrTermClosed once the hub has let the viewer go, or with ctx's error.
func (v *TermViewer) Next(ctx context.Context) (TermFrame, error) {
	select {
	case <-v.done:
		return TermFrame{}, ErrTermClosed
	default:
	}
	select {
	case frame := <-v.frames:
		v.mu.Lock()
		v.pending -= len(frame.Data)
		v.mu.Unlock()
		return frame, nil
	case <-v.done:
		return TermFrame{}, ErrTermClosed
	case <-ctx.Done():
		return TermFrame{}, ctx.Err()
	}
}

// push queues a frame without ever blocking; a viewer that is too far behind
// is let go instead. The terminal's lock is held.
func (v *TermViewer) push(frame TermFrame) {
	v.mu.Lock()
	if v.reason != TermOpen {
		v.mu.Unlock()
		return
	}
	if v.pending+len(frame.Data) > termViewerQueueBytes {
		v.mu.Unlock()
		v.close(TermSlow)
		return
	}
	select {
	case v.frames <- frame:
		v.pending += len(frame.Data)
		v.mu.Unlock()
	default:
		v.mu.Unlock()
		v.close(TermSlow)
	}
}

func (v *TermViewer) close(reason TermCloseReason) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.reason != TermOpen {
		return
	}
	v.reason = reason
	close(v.done)
}

// Detach removes the viewer from its terminal. Call it when the browser goes.
func (v *TermViewer) Detach() {
	v.term.detach(v)
}

// Terminal is one interactive run's relay.
type Terminal struct {
	runID, runnerID string
	owner           *Terminals

	mu      sync.Mutex
	status  string
	buf     []byte
	lastSeq int64
	viewers map[*TermViewer]struct{}
	expiry  *time.Timer
	gone    bool
	// cols and rows are the last size a typing viewer asked for.
	cols, rows int
}

// RunID is the run this terminal belongs to.
func (t *Terminal) RunID() string { return t.runID }

// Status is the run's status as the terminal last heard it.
func (t *Terminal) Status() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func controlFrame(control runnerwire.TermControl) TermFrame {
	raw, _ := json.Marshal(control)
	return TermFrame{Data: raw}
}

// scrollback is the recent output to replay, at most TermScrollbackBytes,
// starting after a line break where one is near the cut. The lock is held.
func (t *Terminal) scrollback() []byte {
	data := t.buf
	if cut := len(data) - runnerwire.TermScrollbackBytes; cut > 0 {
		data = data[cut:]
		window := data[:min(termAlignWindow, len(data))]
		for i, b := range window {
			if b == '\n' {
				data = data[i+1:]
				break
			}
		}
	}
	return append([]byte(nil), data...)
}

// output appends a chunk from the runner and hands it to every viewer. It is
// dropped unless it comes from the run's own runner while the run is
// running, with a seq past the last one.
func (t *Terminal) output(runnerID string, chunk runnerwire.TermChunk) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gone || runnerID != t.runnerID || t.status != runnerwire.RunRunning ||
		chunk.Seq <= t.lastSeq || len(chunk.Data) == 0 {
		return false
	}
	t.lastSeq = chunk.Seq
	data := chunk.Data
	if len(data) > runnerwire.MaxTermChunk {
		data = data[:runnerwire.MaxTermChunk]
	}
	t.buf = append(t.buf, data...)
	// Let the buffer run to twice the scrollback before compacting, so the
	// copy happens once per scrollback's worth of output, not per chunk.
	if len(t.buf) > 2*runnerwire.TermScrollbackBytes {
		t.buf = append(make([]byte, 0, 2*runnerwire.TermScrollbackBytes+runnerwire.MaxTermChunk),
			t.buf[len(t.buf)-runnerwire.TermScrollbackBytes:]...)
	}
	// The data is never written to again, so every viewer shares it.
	frame := TermFrame{Binary: true, Data: data}
	for v := range t.viewers {
		v.push(frame)
	}
	return true
}

// setStatus follows the run's status. A stale report (a status the run has
// already moved past) is ignored, so the order the reports arrive in does not
// matter. A run that ended keeps its terminal for the owner's keep time.
func (t *Terminal) setStatus(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gone || status == t.status || !(Legal(t.status, status) || statusRank(status) > statusRank(t.status)) {
		return
	}
	t.status = status
	frame := controlFrame(runnerwire.TermControl{Type: "status", Status: status})
	for v := range t.viewers {
		v.push(frame)
	}
	if runnerwire.RunFinal(status) && t.expiry == nil {
		t.expiry = time.AfterFunc(t.owner.keepTime(), func() { t.owner.expire(t) })
	}
}

// statusRank orders the lifecycle, so a report that skipped a step (the relay
// was made from a record read a moment too early) still moves it forward.
func statusRank(status string) int {
	switch {
	case status == runnerwire.RunQueued:
		return 0
	case status == runnerwire.RunOffered:
		return 1
	case status == runnerwire.RunRunning:
		return 2
	case runnerwire.RunFinal(status):
		return 3
	}
	return -1
}

// attach adds a viewer, queueing the scrollback, the status and, for a viewer
// who may not type, the readonly notice ahead of any live output.
func (t *Terminal) attach(canType bool) (*TermViewer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gone {
		return nil, ErrTermGone
	}
	if len(t.viewers) >= MaxTermViewers {
		return nil, ErrTermBusy
	}
	v := &TermViewer{
		term: t, canType: canType,
		frames: make(chan TermFrame, termViewerQueueFrames),
		done:   make(chan struct{}),
	}
	if back := t.scrollback(); len(back) > 0 {
		v.push(TermFrame{Binary: true, Data: back})
	}
	v.push(controlFrame(runnerwire.TermControl{Type: "status", Status: t.status}))
	if !canType {
		v.push(controlFrame(runnerwire.TermControl{Type: "readonly"}))
	}
	t.viewers[v] = struct{}{}
	return v, nil
}

func (t *Terminal) detach(v *TermViewer) {
	t.mu.Lock()
	delete(t.viewers, v)
	t.mu.Unlock()
	v.close(TermEnded)
}

// Viewers counts the attached browsers.
func (t *Terminal) Viewers() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.viewers)
}

// Terminals holds the live relays, one per interactive run that has output
// or viewers.
type Terminals struct {
	mu   sync.Mutex
	runs map[string]*Terminal
	keep time.Duration
}

func newTerminals() *Terminals {
	return &Terminals{runs: map[string]*Terminal{}, keep: DefaultTermKeep}
}

// SetKeep changes how long an ended run's output stays readable, for runs
// that end from now on.
func (ts *Terminals) SetKeep(keep time.Duration) {
	ts.mu.Lock()
	ts.keep = keep
	ts.mu.Unlock()
}

func (ts *Terminals) keepTime() time.Duration {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.keep
}

// Get returns a run's relay, or nil.
func (ts *Terminals) Get(runID string) *Terminal {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.runs[runID]
}

// open returns the run's relay, making one from the record if there is none.
// The record may be stale by the time the relay is in the map (its status
// changed in between, and that change's report found no relay to tell), so
// the status is read again once the relay is reachable.
func (ts *Terminals) open(ctx context.Context, store *Store, r Record) *Terminal {
	ts.mu.Lock()
	t := ts.runs[r.ID]
	if t == nil {
		t = &Terminal{
			runID: r.ID, runnerID: r.RunnerID, owner: ts,
			status: r.Status, viewers: map[*TermViewer]struct{}{},
		}
		ts.runs[r.ID] = t
	}
	ts.mu.Unlock()
	if latest, err := store.Get(ctx, r.ID); err == nil {
		t.setStatus(latest.Status)
	}
	if runnerwire.RunFinal(r.Status) {
		t.setFinal(r.Status)
	}
	return t
}

// setFinal makes sure a relay made for a run that already ended expires.
func (t *Terminal) setFinal(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.expiry == nil && !t.gone {
		t.status = status
		t.expiry = time.AfterFunc(t.owner.keepTime(), func() { t.owner.expire(t) })
	}
}

// expire drops an ended run's relay and lets its viewers go.
func (ts *Terminals) expire(t *Terminal) {
	ts.mu.Lock()
	if ts.runs[t.runID] == t {
		delete(ts.runs, t.runID)
	}
	ts.mu.Unlock()
	t.mu.Lock()
	t.gone = true
	t.buf = nil
	viewers := t.viewers
	t.viewers = map[*TermViewer]struct{}{}
	t.mu.Unlock()
	for v := range viewers {
		v.close(TermEnded)
	}
}

// Terminals is the dispatcher's terminal relay.
func (d *Dispatcher) Terminals() *Terminals {
	d.termsOnce.Do(func() { d.terms = newTerminals() })
	return d.terms
}

// termStatus passes a run's new status to its relay, if it has one.
func (d *Dispatcher) termStatus(r Record) {
	t := d.Terminals().Get(r.ID)
	if t == nil {
		return
	}
	before := t.Status()
	t.setStatus(r.Status)
	if before == runnerwire.RunRunning || t.Status() != runnerwire.RunRunning {
		return
	}
	// Just started: pass on the size a viewer asked for while it waited.
	t.mu.Lock()
	cols, rows := t.cols, t.rows
	t.mu.Unlock()
	if cols > 0 && rows > 0 {
		_ = d.sendSize(t, cols, rows)
	}
}

// TermOutput applies a runner's term_out. The runner's ID comes from its
// authenticated socket; output for a run that is not that runner's, not
// interactive or not running is dropped, as is a repeated seq.
func (d *Dispatcher) TermOutput(ctx context.Context, runnerID, runID string, chunk runnerwire.TermChunk) error {
	t := d.Terminals().Get(runID)
	if t == nil {
		r, err := d.store.Get(ctx, runID)
		if err != nil {
			return err
		}
		if r.RunnerID != runnerID {
			return ErrNotFound
		}
		if r.Mode != runnerwire.ModeInteractive || r.Status != runnerwire.RunRunning {
			return ErrTransition
		}
		t = d.Terminals().open(ctx, d.store, r)
	}
	if !t.output(runnerID, chunk) {
		return ErrTransition
	}
	return nil
}

// TermAttach attaches a browser to an interactive run's terminal. A run that
// ended and whose output expired fails with ErrTermGone.
func (d *Dispatcher) TermAttach(ctx context.Context, r Record, canType bool) (*TermViewer, error) {
	if r.Mode != runnerwire.ModeInteractive {
		return nil, ErrPrecondition
	}
	t := d.Terminals().Get(r.ID)
	if t == nil {
		if runnerwire.RunFinal(r.Status) {
			return nil, ErrTermGone
		}
		t = d.Terminals().open(ctx, d.store, r)
	}
	return t.attach(canType)
}

// TermInput sends a typing viewer's keystrokes to the run's runner. Input
// from a viewer who may not type, or while the run is not running, is
// dropped.
func (d *Dispatcher) TermInput(v *TermViewer, data []byte) error {
	if !v.canType || len(data) == 0 {
		return ErrPrecondition
	}
	if len(data) > runnerwire.MaxTermChunk {
		data = data[:runnerwire.MaxTermChunk]
	}
	t := v.term
	if t.Status() != runnerwire.RunRunning {
		return ErrTransition
	}
	return d.reg.Send(t.runnerID, runnerwire.Envelope{
		Type: runnerwire.TypeTermIn, RunID: t.runID, Term: &runnerwire.TermChunk{Data: data},
	})
}

// ClampTermSize bounds a resize to what a terminal can be. A size below one
// cell is not a size and reports false.
func ClampTermSize(cols, rows int) (int, int, bool) {
	if cols < 1 || rows < 1 {
		return 0, 0, false
	}
	return min(cols, runnerwire.MaxTermCols), min(rows, runnerwire.MaxTermRows), true
}

// TermResize sends a typing viewer's terminal size to the runner, clamped.
// The last resize from anyone wins.
func (d *Dispatcher) TermResize(v *TermViewer, cols, rows int) error {
	if !v.canType {
		return ErrPrecondition
	}
	cols, rows, ok := ClampTermSize(cols, rows)
	if !ok {
		return ErrInvalid
	}
	t := v.term
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	running := t.status == runnerwire.RunRunning && !t.gone
	t.mu.Unlock()
	if !running {
		// Kept, and sent when the run starts: a browser that opened the
		// terminal while the run waited sizes it once, up front.
		return nil
	}
	return d.sendSize(t, cols, rows)
}

func (d *Dispatcher) sendSize(t *Terminal, cols, rows int) error {
	return d.reg.Send(t.runnerID, runnerwire.Envelope{
		Type: runnerwire.TypeTermResize, RunID: t.runID, Term: &runnerwire.TermChunk{Cols: cols, Rows: rows},
	})
}

// Audit records an entry on the audit trail, as merge approvals are.
func (d *Dispatcher) Audit(entry AuditEntry) { d.record(entry) }
