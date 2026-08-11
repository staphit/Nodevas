//go:build unix

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openLockFile(path string) (*os.File, error) {
	// 0600: the lock file records a pid and a hostname and nothing else, but
	// it lives beside credentials, so it inherits their permissions.
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

// lockFile takes flock(LOCK_EX|LOCK_NB). flock is attached to the open file
// description, so it is released by the kernel when the process dies for any
// reason, and a second open() in this same process contends with the first --
// which is what makes the in-process test of ErrLocked meaningful.
func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EACCES):
		return ErrLocked
	default:
		return err
	}
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
