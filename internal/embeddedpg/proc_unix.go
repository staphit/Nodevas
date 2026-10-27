//go:build unix

package embeddedpg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// procExtra is nothing on Unix: the process group and (on Linux) the
// parent-death signal are set at spawn.
type procExtra struct{}

func binPath(binaries, program string) string {
	return filepath.Join(binaries, "bin", program)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killPID(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// requestFastShutdown is SIGINT: PostgreSQL's "fast" mode.
func requestFastShutdown(_ context.Context, proc *postmaster) error {
	return proc.cmd.Process.Signal(os.Interrupt)
}

// immediateShutdown is SIGQUIT: no checkpoint, crash recovery on the next
// start, but the server is gone.
func immediateShutdown(proc *postmaster) {
	_ = proc.cmd.Process.Signal(syscall.SIGQUIT)
}

func requestOrphanShutdown(_ context.Context, _, _ string, pid int) {
	_ = syscall.Kill(pid, syscall.SIGINT)
}

func releaseProc(*postmaster) {}

// WriteSecretFile writes data readable by the current user only: mode 0600 on
// Unix, a protected DACL naming only the current user's SID on Windows. The
// file is written beside the target and renamed over it, so a looser file
// already there is replaced rather than reused.
func WriteSecretFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("embeddedpg: write %s: %w", path, err)
	}
	return nil
}

// restrictDir makes a directory owner-only.
func restrictDir(dir string) error { return os.Chmod(dir, 0o700) }

// isElevated is false on Unix; running as root is refused separately.
func refuseRoot() error {
	if os.Geteuid() == 0 {
		return errors.New("embeddedpg: PostgreSQL refuses to run as root; run Nodevas as an ordinary user, " +
			"or set NODEVAS_DATABASE_URL to an existing server")
	}
	return nil
}
