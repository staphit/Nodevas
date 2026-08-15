//go:build !unix && !windows

package lockfile

import (
	"errors"
	"os"
)

// This build has no advisory file locking available. Refusing loudly is the
// only honest option: silently running unlocked would reintroduce exactly the
// lost-update window the package exists to close.
var errUnsupported = errors.New("lockfile: file locking is not supported on this platform")

func openLockFile(path string) (*os.File, error) {
	return nil, errUnsupported
}

func lockFile(file *os.File) error { return errUnsupported }

func unlockFile(file *os.File) error { return errUnsupported }
