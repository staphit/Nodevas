//go:build windows

package runner

import "os"

// openNoBlock opens path for reading. Windows has no FIFO in a directory
// tree to wait on; a named pipe lives under \\.\pipe\, never in a worktree.
func openNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
