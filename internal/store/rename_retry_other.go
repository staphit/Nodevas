//go:build !windows

package store

// transientRenameError: a POSIX rename replaces an open file, so no rename
// error is a reader in the way.
func transientRenameError(error) bool { return false }
