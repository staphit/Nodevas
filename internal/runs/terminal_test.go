package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func testTerminal(status string) *Terminal {
	return &Terminal{
		runID: "run-1", runnerID: "runner-1", owner: newTerminals(),
		status: status, viewers: map[*TermViewer]struct{}{},
	}
}

func nextFrame(t *testing.T, v *TermViewer) TermFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	frame, err := v.Next(ctx)
	if err != nil {
		t.Fatalf("next frame: %v", err)
	}
	return frame
}

func control(t *testing.T, frame TermFrame) runnerwire.TermControl {
	t.Helper()
	if frame.Binary {
		t.Fatalf("got output %q, want a control frame", frame.Data)
	}
	var c runnerwire.TermControl
	if err := json.Unmarshal(frame.Data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTerminalOutputRules(t *testing.T) {
	term := testTerminal(runnerwire.RunRunning)
	v, err := term.attach(true)
	if err != nil {
		t.Fatal(err)
	}
	if c := control(t, nextFrame(t, v)); c.Type != "status" || c.Status != runnerwire.RunRunning {
		t.Fatalf("first frame = %+v", c)
	}
	chunk := func(seq int64, data string) runnerwire.TermChunk {
		return runnerwire.TermChunk{Seq: seq, Data: []byte(data)}
	}
	if !term.output("runner-1", chunk(1, "one ")) {
		t.Fatal("first chunk dropped")
	}
	for name, ok := range map[string]bool{
		"repeat seq":   term.output("runner-1", chunk(1, "again")),
		"older seq":    term.output("runner-1", chunk(0, "zero")),
		"other runner": term.output("runner-2", chunk(5, "evil")),
		"empty":        term.output("runner-1", chunk(6, "")),
	} {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if !term.output("runner-1", chunk(3, "three")) {
		t.Fatal("a gap in seq is fine")
	}
	if got := nextFrame(t, v); !got.Binary || string(got.Data) != "one " {
		t.Fatalf("frame = %q", got.Data)
	}
	if got := nextFrame(t, v); string(got.Data) != "three" {
		t.Fatalf("frame = %q", got.Data)
	}

	// An over-long chunk is cut to MaxTermChunk.
	term.output("runner-1", runnerwire.TermChunk{Seq: 4, Data: bytes.Repeat([]byte("x"), runnerwire.MaxTermChunk+10)})
	if got := nextFrame(t, v); len(got.Data) != runnerwire.MaxTermChunk {
		t.Fatalf("chunk of %d bytes", len(got.Data))
	}

	// Once the run ends, output is dropped and viewers hear the status.
	term.setStatus(runnerwire.RunSucceeded)
	if c := control(t, nextFrame(t, v)); c.Status != runnerwire.RunSucceeded {
		t.Fatalf("status frame = %+v", c)
	}
	if term.output("runner-1", chunk(9, "late")) {
		t.Fatal("output after the end accepted")
	}
	// A stale report does not drag it back.
	term.setStatus(runnerwire.RunRunning)
	if term.Status() != runnerwire.RunSucceeded {
		t.Fatalf("status = %s", term.Status())
	}
}

func TestTerminalStatusSkipsAhead(t *testing.T) {
	term := testTerminal(runnerwire.RunQueued)
	term.setStatus(runnerwire.RunRunning)
	if term.Status() != runnerwire.RunRunning {
		t.Fatalf("queued → running (offered missed): %s", term.Status())
	}
	term.setStatus(runnerwire.RunOffered)
	if term.Status() != runnerwire.RunRunning {
		t.Fatalf("running → offered accepted: %s", term.Status())
	}
}

func TestTerminalScrollback(t *testing.T) {
	term := testTerminal(runnerwire.RunRunning)
	line := strings.Repeat("y", 1000) + "\n"
	seq := int64(0)
	for written := 0; written < 3*runnerwire.TermScrollbackBytes; written += len(line) {
		seq++
		term.output("runner-1", runnerwire.TermChunk{Seq: seq, Data: []byte(line)})
	}
	if len(term.buf) > 2*runnerwire.TermScrollbackBytes {
		t.Fatalf("buffer grew to %d", len(term.buf))
	}
	v, err := term.attach(false)
	if err != nil {
		t.Fatal(err)
	}
	back := nextFrame(t, v)
	if !back.Binary || len(back.Data) > runnerwire.TermScrollbackBytes || len(back.Data) < runnerwire.TermScrollbackBytes-len(line) {
		t.Fatalf("scrollback of %d bytes", len(back.Data))
	}
	if !bytes.HasPrefix(back.Data, []byte("yyy")) || !bytes.HasSuffix(back.Data, []byte("y\n")) {
		t.Fatal("scrollback does not start at a line")
	}
	if c := control(t, nextFrame(t, v)); c.Type != "status" {
		t.Fatalf("after scrollback: %+v", c)
	}
	if c := control(t, nextFrame(t, v)); c.Type != "readonly" {
		t.Fatalf("read-only viewer told %+v", c)
	}
}

// A viewer that reads nothing is let go; the output path never waits on it.
func TestTerminalSlowViewerDropped(t *testing.T) {
	term := testTerminal(runnerwire.RunRunning)
	slow, _ := term.attach(true)
	fast, _ := term.attach(true)
	nextFrame(t, fast) // status
	data := bytes.Repeat([]byte("z"), runnerwire.MaxTermChunk)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for seq := int64(1); seq <= 64; seq++ {
			term.output("runner-1", runnerwire.TermChunk{Seq: seq, Data: data})
			if _, err := fast.Next(context.Background()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("output blocked on a slow viewer")
	}
	if slow.Reason() != TermSlow {
		t.Fatalf("slow viewer reason = %v", slow.Reason())
	}
	if fast.Reason() != TermOpen {
		t.Fatalf("fast viewer reason = %v", fast.Reason())
	}
}

func TestTerminalViewerCapAndExpiry(t *testing.T) {
	ts := newTerminals()
	ts.SetKeep(20 * time.Millisecond)
	term := &Terminal{runID: "run-1", runnerID: "runner-1", owner: ts, status: runnerwire.RunRunning, viewers: map[*TermViewer]struct{}{}}
	ts.runs["run-1"] = term
	var viewers []*TermViewer
	for range MaxTermViewers {
		v, err := term.attach(false)
		if err != nil {
			t.Fatal(err)
		}
		viewers = append(viewers, v)
	}
	if _, err := term.attach(false); err != ErrTermBusy {
		t.Fatalf("over the cap: %v", err)
	}
	viewers[0].Detach()
	if _, err := term.attach(false); err != nil {
		t.Fatalf("after a detach: %v", err)
	}
	term.setStatus(runnerwire.RunFailed)
	deadline := time.Now().Add(2 * time.Second)
	for ts.Get("run-1") != nil {
		if time.Now().After(deadline) {
			t.Fatal("terminal never expired")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if viewers[1].Reason() != TermEnded {
		t.Fatalf("viewer reason after expiry = %v", viewers[1].Reason())
	}
	if _, err := term.attach(true); err != ErrTermGone {
		t.Fatalf("attach after expiry: %v", err)
	}
}

func TestClampTermSize(t *testing.T) {
	for _, tc := range []struct{ cols, rows, wantCols, wantRows int }{
		{80, 24, 80, 24},
		{9999, 9999, runnerwire.MaxTermCols, runnerwire.MaxTermRows},
	} {
		cols, rows, ok := ClampTermSize(tc.cols, tc.rows)
		if !ok || cols != tc.wantCols || rows != tc.wantRows {
			t.Errorf("clamp(%d, %d) = %d, %d, %v", tc.cols, tc.rows, cols, rows, ok)
		}
	}
	if _, _, ok := ClampTermSize(0, 24); ok {
		t.Error("zero columns accepted")
	}
}
