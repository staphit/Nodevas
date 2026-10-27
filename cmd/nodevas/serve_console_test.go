package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// stopsAfter feeds signals to watchStopSignals at the given clock times and
// reports whether it returned (serve stops) and what it printed.
func stopsAfter(t *testing.T, confirm bool, signals []os.Signal, at []time.Duration) (bool, string) {
	t.Helper()
	// Written here while the watcher may still be reading the last value.
	var clock atomic.Int64
	ch := make(chan os.Signal)
	var out bytes.Buffer
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer close(done)
		watchStopSignals(ctx, ch, confirm, 3*time.Second, func() time.Time { return time.Unix(0, clock.Load()) }, &out)
	}()
	for i, sig := range signals {
		clock.Store(int64(at[i]))
		select {
		case ch <- sig:
		case <-done:
			t.Fatalf("stopped before signal %d", i)
		}
	}
	select {
	case <-done:
		return true, out.String()
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		return false, out.String()
	}
}

func TestStandaloneServeAsksForASecondCtrlC(t *testing.T) {
	stopped, out := stopsAfter(t, true, []os.Signal{os.Interrupt}, []time.Duration{0})
	if stopped || !strings.Contains(out, stopConfirmPrompt) {
		t.Fatalf("one Ctrl+C: stopped=%v out=%q", stopped, out)
	}
	if stopped, _ := stopsAfter(t, true, []os.Signal{os.Interrupt, os.Interrupt},
		[]time.Duration{0, 2 * time.Second}); !stopped {
		t.Fatal("a second Ctrl+C within the window did not stop serve")
	}
	stopped, out = stopsAfter(t, true, []os.Signal{os.Interrupt, os.Interrupt},
		[]time.Duration{0, 5 * time.Second})
	if stopped || strings.Count(out, stopConfirmPrompt) != 2 {
		t.Fatalf("a late second Ctrl+C should ask again: stopped=%v out=%q", stopped, out)
	}
	if stopped, _ := stopsAfter(t, true, []os.Signal{syscall.SIGTERM}, []time.Duration{0}); !stopped {
		t.Fatal("SIGTERM (or the console closing) did not stop serve at once")
	}
}

func TestHostedServeStopsOnTheFirstCtrlC(t *testing.T) {
	if stopped, out := stopsAfter(t, false, []os.Signal{os.Interrupt}, []time.Duration{0}); !stopped || out != "" {
		t.Fatalf("stopped=%v out=%q", stopped, out)
	}
}
