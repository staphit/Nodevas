package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

// useConfigDir points os.UserConfigDir at a temporary directory on every OS.
func useConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	root, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// A hosted hub logs to stderr only, and says nothing more there; a standalone
// one also keeps serve.log under the catalog root.
func TestServeLogFileIsForAStandaloneHubOnly(t *testing.T) {
	root := useConfigDir(t)
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	hosted := openServeLogs(stderr, false)
	hosted.close()
	if hosted.path != "" || hosted.file != nil {
		t.Fatalf("hosted hub opened a log file: %q", hosted.path)
	}
	if _, err := os.Stat(filepath.Join(root, "nodevas", "logs")); !os.IsNotExist(err) {
		t.Fatalf("hosted hub created the logs directory: %v", err)
	}
	if got, _ := os.ReadFile(stderr.Name()); len(got) != 0 {
		t.Fatalf("hosted hub wrote to stderr: %q", got)
	}

	standalone := openServeLogs(stderr, true)
	want := filepath.Join(root, "nodevas", "logs", "serve.log")
	if standalone.path != want || standalone.file == nil {
		t.Fatalf("standalone log path = %q, want %q", standalone.path, want)
	}
	_, _ = standalone.writer.Write([]byte("hello\n"))
	standalone.close()
	if got, _ := os.ReadFile(want); string(got) != "hello\n" {
		t.Fatalf("serve.log = %q", got)
	}
}

// NUL and /dev/null are character devices, and not terminals: serve started
// with its stdin there (Electron, systemd) stops on the first Ctrl+C.
func TestNullDeviceIsNotATerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Fatalf("%s counted as a terminal", os.DevNull)
	}
	if stdinIsTerminal(null) {
		t.Fatalf("%s counted as a terminal stdin", os.DevNull)
	}
}

// The runner under serve drops the variable, so its agents and any runner
// they start do not inherit it.
func TestRunnerUnderServeDoesNotPassItOn(t *testing.T) {
	t.Setenv(localRunnerUnderServeEnv, "1")
	_, stop := runnerStopContext()
	defer stop()
	if _, set := os.LookupEnv(localRunnerUnderServeEnv); set {
		t.Fatal("the runner left NODEVAS_RUNNER_UNDER_SERVE for its children")
	}
}

func TestStopWhenParentChanges(t *testing.T) {
	var parent atomic.Int64
	parent.Store(100)
	getppid := func() int { return int(parent.Load()) }
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	stopWhenParentChanges(ctx, stop, getppid, 5*time.Millisecond)
	select {
	case <-ctx.Done():
		t.Fatal("stopped with serve still the parent")
	case <-time.After(50 * time.Millisecond):
	}
	parent.Store(1)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop when the parent changed")
	}
}
