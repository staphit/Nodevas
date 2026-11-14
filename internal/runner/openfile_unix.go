//go:build !windows

package runner

import (
	"os"
	"syscall"
)

// openNoBlock opens path for reading without waiting on it: a FIFO an agent
// left in its worktree would otherwise block open(2) until something writes
// to it, and no context can end that wait. The caller checks the mode after.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
