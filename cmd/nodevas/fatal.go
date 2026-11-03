package main

import (
	"log"
	"os"
	"sync"
)

// atFatal runs before a fatal exit. A command that opened the workspace
// database sets it to close that handle: os.Exit skips deferred calls, and the
// last close is what stops an embedded PostgreSQL this process started,
// cleanly, instead of leaving it to be killed with the process.
var (
	atFatal     func()
	atFatalOnce sync.Once
)

func runAtFatal() {
	atFatalOnce.Do(func() {
		if atFatal != nil {
			atFatal()
		}
	})
}

// fatalf is log.Fatalf after atFatal.
func fatalf(format string, args ...any) {
	runAtFatal()
	log.Fatalf(format, args...)
}

// fatal is log.Fatal after atFatal.
func fatal(args ...any) {
	runAtFatal()
	log.Fatal(args...)
}

// exit is os.Exit after atFatal.
func exit(code int) {
	runAtFatal()
	os.Exit(code)
}
