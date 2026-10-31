//go:build !windows

package store

// isTransientRenameError: a POSIX rename replaces the target even while it is
// open, so no failure clears by waiting.
func isTransientRenameError(error) bool { return false }
