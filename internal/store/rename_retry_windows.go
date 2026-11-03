//go:build windows

package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientRenameError: replacing a file another process holds open without
// FILE_SHARE_DELETE fails with access denied or a sharing violation until that
// process closes it.
func isTransientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
