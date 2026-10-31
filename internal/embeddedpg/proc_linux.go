//go:build linux

package embeddedpg

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// spawnAttrs is how the postmaster is started: its own process group (Setpgid
// on Unix, CREATE_NEW_PROCESS_GROUP|CREATE_NO_WINDOW on Windows), so a
// terminal Ctrl+C reaches serve and not PostgreSQL.
func spawnAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// launch starts the postmaster from a goroutine that keeps its OS thread for
// the server's lifetime. Pdeathsig fires when the *thread* that forked the
// child exits, not the process; a thread Go might retire would take a healthy
// server with it.
func launch(proc *postmaster) error {
	proc.cmd.SysProcAttr.Pdeathsig = syscall.SIGQUIT
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with this goroutine
		if err := proc.cmd.Start(); err != nil {
			started <- err
			return
		}
		proc.pid = proc.cmd.Process.Pid
		started <- nil
		proc.waitErr = proc.cmd.Wait()
		close(proc.done)
	}()
	return <-started
}

// processImage is the executable's base name, "" when unknown.
func processImage(pid int) string {
	if exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil {
		return filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
	}
	if comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil {
		return strings.TrimSpace(string(comm))
	}
	return ""
}
