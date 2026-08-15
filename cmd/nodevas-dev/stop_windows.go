//go:build windows

package main

import "os"

// ungracefulStopNote is shown in the log pane on every Windows stop, because
// the operator should know which stop they got.
const ungracefulStopNote = "stopped with TerminateProcess: Windows has no SIGTERM, so the server's " +
	"shutdown path did not run. The final remote backup was skipped and the SQLite WAL was not " +
	"checkpointed (the -wal sidecar stays on disk and is recovered the next time the workspace opens). " +
	"Committed data is not lost; the most recent backup is older than it would be after a Unix stop."

// terminateChild ends the server on Windows, which has no SIGTERM.
//
// cmd/nodevas hangs its graceful shutdown off signal.NotifyContext for
// os.Interrupt and SIGTERM. On Windows os.Process.Signal only implements Kill;
// sending os.Interrupt returns "not supported by windows". The alternative
// would be GenerateConsoleCtrlEvent with CTRL_BREAK, which needs the child
// spawned into its own process group and both processes attached to the same
// console -- neither of which holds when this launcher is started from a
// shortcut or a GUI terminal, and getting it wrong sends the break to our own
// group. So this stop is deliberately ungraceful, and it says so rather than
// pretending otherwise: see ungracefulStopNote for what that costs.
//
// The workspace lock is held on an open file descriptor, so the kernel drops
// it when the process dies here just as it would after a clean exit; a killed
// server never wedges the workspace. That is why an ungraceful stop is
// acceptable for a dev tool at all.
func terminateChild(process *os.Process) (graceful bool, err error) {
	if err := process.Kill(); err != nil {
		return false, err
	}
	return false, nil
}
