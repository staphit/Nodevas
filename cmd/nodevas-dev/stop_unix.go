//go:build !windows

package main

import (
	"io"
	"os"
	"syscall"
)

// ungracefulStopNote is empty here because a Unix stop is graceful.
const ungracefulStopNote = ""

// terminateChild asks the server to shut down. On Unix that is SIGTERM, which
// is one of the two signals cmd/nodevas's signal.NotifyContext waits for, so
// the child runs its own shutdown path: it closes the realtime hub, drains
// connections, and then takes up to thirty seconds for the final remote
// backup and stops its embedded PostgreSQL. Nothing is lost. (stdin, which
// Windows closes instead, is left alone.)
func terminateChild(process *os.Process, _ io.Closer) (graceful bool, err error) {
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return false, err
	}
	return true, nil
}
