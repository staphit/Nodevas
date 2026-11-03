//go:build unix && !linux

package embeddedpg

import (
	"syscall"
)

// spawnAttrs is how the postmaster is started: its own process group (Setpgid
// on Unix, CREATE_NEW_PROCESS_GROUP|CREATE_NO_WINDOW on Windows), so a
// terminal Ctrl+C reaches serve and not PostgreSQL.
func spawnAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// launch starts the postmaster. There is no parent-death signal here: a
// postmaster orphaned by a killed serve is found and stopped by the next
// start (RecoverStalePID).
func launch(proc *postmaster) error {
	if err := proc.cmd.Start(); err != nil {
		return err
	}
	proc.pid = proc.cmd.Process.Pid
	go func() {
		proc.waitErr = proc.cmd.Wait()
		close(proc.done)
	}()
	return nil
}
