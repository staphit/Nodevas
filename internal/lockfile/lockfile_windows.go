//go:build windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRegionOffset is a byte range far past any content the file will ever
// hold. Locking a dedicated region rather than the whole file keeps the
// diagnostic JSON at offset 0 readable by other processes -- on Windows a
// byte-range lock blocks reads too, so locking everything would hide exactly
// the "who owns this?" information the file exists to provide.
const lockRegionOffset = uint64(1) << 40

// openLockFile opens the file with FILE_SHARE_DELETE in addition to the usual
// read/write sharing. Without it a held lock file cannot be deleted on
// Windows, which would make an open workspace undeletable and would break
// t.TempDir cleanup in every test that leaves a manager running.
func openLockFile(path string) (*os.File, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		wide,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func lockRange() (offsetLow, offsetHigh, countLow, countHigh uint32) {
	offset := lockRegionOffset
	return uint32(offset & 0xFFFFFFFF), uint32(offset >> 32), 1, 0
}

// lockFile takes LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY).
// The lock belongs to the file handle, so Windows releases it when the process
// exits -- including a TerminateProcess from the Electron parent, which is the
// case that must never leave a workspace stuck.
func lockFile(file *os.File) error {
	offsetLow, offsetHigh, countLow, countHigh := lockRange()
	overlapped := windows.Overlapped{Offset: offsetLow, OffsetHigh: offsetHigh}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		countLow,
		countHigh,
		&overlapped,
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION),
		errors.Is(err, windows.ERROR_IO_PENDING),
		errors.Is(err, windows.ERROR_SHARING_VIOLATION):
		return ErrLocked
	default:
		return err
	}
}

func unlockFile(file *os.File) error {
	offsetLow, offsetHigh, countLow, countHigh := lockRange()
	overlapped := windows.Overlapped{Offset: offsetLow, OffsetHigh: offsetHigh}
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		countLow,
		countHigh,
		&overlapped,
	)
}
