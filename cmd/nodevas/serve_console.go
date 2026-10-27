package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"nodevas/internal/logging"
	"nodevas/internal/project"
)

// serveLogMaxBytes is the size at which serve.log moves aside to serve.log.1.
const serveLogMaxBytes = 10 << 20

// serveLogs is where serve's log goes: the console as before, and a file
// under the catalog root that is still there when the console is gone.
type serveLogs struct {
	// writer is what logging.Setup writes to: the console and the file.
	writer io.Writer
	// path is the log file, or "" when it could not be opened.
	path    string
	stderr  *os.File
	console swapWriter
	async   *logging.AsyncWriter
	file    *logging.RotatingFile
}

// openServeLogs sets up serve's log sinks.
//
// The file is in addition to the console, never instead of it: a hosted hub
// in a container still writes its JSON to stderr for docker, unchanged, and
// gets the file only where the catalog root is writable.
//
// The runtime's crash output also goes to the file (debug.SetCrashOutput), so
// an unrecovered panic or a fatal error leaves its stack trace there even when
// the console window that showed it has closed.
func openServeLogs(stderr *os.File) *serveLogs {
	logs := &serveLogs{stderr: stderr}
	logs.console.set(stderr)
	logs.writer = &logs.console
	root, err := project.DefaultCatalogRoot()
	if err != nil {
		fmt.Fprintf(stderr, "no serve log file (%v); logging to the console only\n", err)
		return logs
	}
	path := filepath.Join(root, "logs", "serve.log")
	file, err := logging.OpenRotatingFile(path, serveLogMaxBytes, setCrashOutput)
	if err != nil {
		fmt.Fprintf(stderr, "no serve log file (%v); logging to the console only\n", err)
		return logs
	}
	logs.file, logs.path = file, path
	logs.writer = teeWriter{&logs.console, file}
	return logs
}

// consoleNonBlocking makes the console writer one that never blocks
// (logging.AsyncWriter), for a standalone hub once it is up. Selecting text in
// a Windows console pauses its output, and a paused log must not pause the
// HTTP handlers writing to it; the file keeps every line either way. Startup
// stays synchronous, so a log.Fatal there still reaches the console before
// the process exits.
func (l *serveLogs) consoleNonBlocking() {
	if l.async != nil {
		return
	}
	l.async = logging.NewAsyncWriter(l.stderr, 4096)
	l.console.set(l.async)
}

// fatal is log.Fatalf for after consoleNonBlocking: the line is flushed to
// the console first.
func (l *serveLogs) fatal(format string, args ...any) {
	log.Printf(format, args...)
	l.close()
	os.Exit(1)
}

// setCrashOutput points the runtime's crash output at f, or nowhere extra
// when f is nil.
func setCrashOutput(f *os.File) {
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "crash output: %v\n", err)
	}
}

// close flushes the console and closes the file. Only the last thing serve
// does: a log line after it reaches the console only.
func (l *serveLogs) close() {
	if l.async != nil {
		l.async.Flush(2 * time.Second)
		if dropped := l.async.Dropped(); dropped > 0 && l.file != nil {
			fmt.Fprintf(l.file, "%d log lines did not reach the console (it was paused or too slow)\n", dropped)
		}
		l.console.set(l.stderr)
	}
	if l.file != nil {
		setCrashOutput(nil)
		_ = l.file.Close()
	}
}

// swapWriter is a writer whose target can change while others write to it.
type swapWriter struct{ target atomic.Pointer[io.Writer] }

func (s *swapWriter) set(w io.Writer) { s.target.Store(&w) }

func (s *swapWriter) Write(p []byte) (int, error) { return (*s.target.Load()).Write(p) }

// teeWriter writes to every writer, whichever of them fails: io.MultiWriter
// stops at the first error, and a console that went away must not take the
// file down with it.
type teeWriter []io.Writer

func (t teeWriter) Write(p []byte) (int, error) {
	for _, w := range t {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// stopConfirmWindow is how long a first Ctrl+C waits for the second.
const stopConfirmWindow = 3 * time.Second

const stopConfirmPrompt = "press Ctrl+C again within 3 seconds to stop Nodevas"

// serveContext ends when serve should stop: on SIGTERM (which is also what a
// Windows console closing or logging off arrives as), and on Ctrl+C.
//
// With confirm (a standalone hub on a terminal) one Ctrl+C is not enough: the
// same terminal is where runs are accepted, and a Ctrl+C meant for a copy, or
// pressed once too often, would stop every run in flight. The first one says
// to press again; a second within stopConfirmWindow stops. A hosted hub, or one
// a launcher started without a terminal, stops on the first, as before.
func serveContext(confirm bool, stderr io.Writer) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		watchStopSignals(ctx, signals, confirm, stopConfirmWindow, time.Now, stderr)
		// Stopping now: a further Ctrl+C gets the default treatment, which
		// ends the process at once if the shutdown hangs.
		signal.Stop(signals)
		cancel()
	}()
	return ctx, cancel
}

// watchStopSignals returns when serve should stop or ctx ends.
func watchStopSignals(ctx context.Context, signals <-chan os.Signal, confirm bool, window time.Duration,
	now func() time.Time, stderr io.Writer) {
	var armed time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-signals:
			if sig != os.Interrupt || !confirm {
				return
			}
			if !armed.IsZero() && now().Sub(armed) <= window {
				return
			}
			armed = now()
			fmt.Fprintln(stderr, stopConfirmPrompt)
		}
	}
}

// localRunnerUnderServeEnv tells a runner that serve started it: serve owns
// Ctrl+C on the terminal they share, and stops the runner itself.
const localRunnerUnderServeEnv = "NODEVAS_RUNNER_UNDER_SERVE"

// runnerStopContext is the runner's lifetime: SIGTERM, and Ctrl+C unless
// serve started it. Under serve, a Ctrl+C reaches every process on the
// terminal, and the runner must not end on the first one serve asks to
// confirm; Notify (not Ignore, which on Windows lets the console's default
// handler end the process) swallows it.
func runnerStopContext() (context.Context, context.CancelFunc) {
	if os.Getenv(localRunnerUnderServeEnv) == "1" {
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		go func() {
			for range interrupts {
			}
		}()
		return signal.NotifyContext(context.Background(), syscall.SIGTERM)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
