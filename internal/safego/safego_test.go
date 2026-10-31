package safego

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureLog points slog at a buffer for the length of a test. The tests in
// this file share slog.Default, so none of them runs in parallel.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buffer := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestGoRecoversAndLogsWithStack(t *testing.T) {
	logs := captureLog(t)
	done := make(chan struct{})
	Go("exploder", func() {
		defer close(done)
		var m map[string]int
		m["boom"] = 1
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine did not finish")
	}
	// The log line is written after fn's own defers; wait for it.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "exploder") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := logs.String()
	for _, want := range []string{"background goroutine panicked", "goroutine=exploder", "nil map", "safego_test.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}

func TestCallReportsPanic(t *testing.T) {
	captureLog(t)
	if Call("fine", func() {}) {
		t.Fatal("a normal return reported a panic")
	}
	if !Call("bad", func() { panic("nope") }) {
		t.Fatal("a panic was not reported")
	}
}

func TestRecoverInTimerCallback(t *testing.T) {
	logs := captureLog(t)
	fired := make(chan struct{})
	time.AfterFunc(time.Millisecond, func() {
		defer close(fired)
		defer Recover("timer")
		panic("timer boom")
	})
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not fire")
	}
	if !strings.Contains(logs.String(), "timer boom") {
		t.Fatalf("log %q lacks the panic", logs.String())
	}
}

func TestRunLoopRestartsWithBackoffUntilItReturns(t *testing.T) {
	captureLog(t)
	MinBackoff, MaxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { MinBackoff, MaxBackoff = time.Second, time.Minute })

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunLoop(context.Background(), "flaky", func(context.Context) {
			if calls.Add(1) < 4 {
				panic("again")
			}
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return")
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("fn ran %d times, want 4 (three panics, then a normal return)", got)
	}
}

func TestRunLoopStopsWhenContextEnds(t *testing.T) {
	captureLog(t)
	MinBackoff, MaxBackoff = time.Hour, time.Hour
	t.Cleanup(func() { MinBackoff, MaxBackoff = time.Second, time.Minute })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunLoop(ctx, "always", func(context.Context) { panic("always") })
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop kept waiting after its context ended")
	}
}

func TestLoopUntilStopsWhenStopCloses(t *testing.T) {
	captureLog(t)
	MinBackoff, MaxBackoff = time.Hour, time.Hour
	t.Cleanup(func() { MinBackoff, MaxBackoff = time.Second, time.Minute })

	stop := make(chan struct{})
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(stop, "until", func() {
			calls.Add(1)
			panic("until")
		})
	}()
	time.Sleep(20 * time.Millisecond)
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop kept waiting after stop closed")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times during an hour's backoff, want 1", got)
	}
}
