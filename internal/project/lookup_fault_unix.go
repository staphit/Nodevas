//go:build !windows

package project

import "syscall"

// diskFaultErrnos are the errors that say the disk could not be read rather
// than anything about the path (lookupFailure).
var diskFaultErrnos = []syscall.Errno{
	syscall.EIO, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM, syscall.ENOBUFS,
	syscall.EAGAIN, syscall.ESTALE, syscall.ETIMEDOUT,
}
