// Package safego starts the server's background goroutines so that a panic in
// one of them is logged instead of ending the process.
//
// net/http and gin recover a panicking handler; nothing recovers a bare
// goroutine. A nil map in a notifier, a backup loop or a timer callback would
// otherwise take every open tab, run and terminal down with it, and leave only
// the stack on a console nobody may be watching. Each helper here logs the
// panic with its stack through slog (which also reaches the serve log file) and
// lets the rest of the server carry on.
package safego

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// Backoff bounds for Loop. Variables so tests can shorten them.
var (
	MinBackoff = time.Second
	MaxBackoff = time.Minute
)

// Go runs fn on a new goroutine. A panic is logged and ends that goroutine.
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}

// Recover logs a panic in progress. It must be deferred directly, at the top
// of a goroutine or timer callback the caller starts itself:
//
//	defer safego.Recover("name")
func Recover(name string) {
	if recovered := recover(); recovered != nil {
		LogPanic(name, recovered)
	}
}

// Call runs fn and reports whether it panicked; the panic is logged. It keeps
// one bad pass of a loop from ending the loop.
func Call(name string, fn func()) (panicked bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			LogPanic(name, recovered)
			panicked = true
		}
	}()
	fn()
	return false
}

// Loop runs fn on a new goroutine and starts it again after a panic, until fn
// returns or ctx ends. The wait between restarts doubles from MinBackoff to
// MaxBackoff, so a loop that panics on every pass does not spin, and it starts
// over at MinBackoff once fn has run for longer than MaxBackoff.
func Loop(ctx context.Context, name string, fn func(context.Context)) {
	go RunLoop(ctx, name, fn)
}

// RunLoop is Loop on the caller's goroutine: it returns when fn returns
// normally or ctx ends.
func RunLoop(ctx context.Context, name string, fn func(context.Context)) {
	runLoop(ctx.Done(), name, func() { fn(ctx) })
}

// LoopUntil is Loop for a loop whose owner signals shutdown by closing stop
// rather than with a context.
func LoopUntil(stop <-chan struct{}, name string, fn func()) {
	go runLoop(stop, name, fn)
}

func runLoop(done <-chan struct{}, name string, fn func()) {
	wait := MinBackoff
	for {
		started := time.Now()
		if !Call(name, fn) {
			return
		}
		if time.Since(started) > MaxBackoff {
			wait = MinBackoff
		}
		slog.Warn("background loop restarting after a panic",
			slog.String("goroutine", name), slog.Duration("backoff", wait))
		timer := time.NewTimer(wait)
		select {
		case <-done:
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = min(wait*2, MaxBackoff)
	}
}

// LogPanic logs a recovered panic with the current stack, for a caller that
// recovers itself because it has to clean up after one.
func LogPanic(name string, recovered any) {
	slog.Error("background goroutine panicked",
		slog.String("goroutine", name),
		slog.Any("panic", recovered),
		slog.String("error.stack_trace", string(debug.Stack())))
}
