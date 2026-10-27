//go:build windows

package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// transientRenameError is a rename refused because another handle has the
// file open; see renameReplacing.
func transientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
