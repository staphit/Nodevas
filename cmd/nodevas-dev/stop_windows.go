//go:build windows

package main

import (
	"io"
	"os"
)

// ungracefulStopNote is empty here too: the server is stopped through its
// stdin (serve --stop-on-stdin-close), which runs its whole shutdown path.
const ungracefulStopNote = ""

// terminateChild ends the server on Windows, which has no SIGTERM.
//
// cmd/nodevas hangs its graceful shutdown off signal.NotifyContext for
// os.Interrupt and SIGTERM. On Windows os.Process.Signal only implements Kill,
// and GenerateConsoleCtrlEvent needs both processes on one console, which
// does not hold when this launcher is started from a shortcut. So the server
// is spawned with --stop-on-stdin-close and stopped by closing its stdin: it
// drains connections, takes the final backup and stops its embedded
// PostgreSQL cleanly. Stop still kills it if it does not go in time.
func terminateChild(process *os.Process, stdin io.Closer) (graceful bool, err error) {
	if stdin != nil {
		if err := stdin.Close(); err == nil {
			return true, nil
		}
	}
	if err := process.Kill(); err != nil {
		return false, err
	}
	return false, nil
}
