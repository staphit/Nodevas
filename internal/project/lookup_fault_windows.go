package project

import "syscall"

// diskFaultErrnos are the errors that say the disk could not be read rather
// than anything about the path (lookupFailure). Windows reports its own
// codes; the syscall package's invented POSIX values are listed too.
var diskFaultErrnos = []syscall.Errno{
	syscall.EIO, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM,
	4,    // ERROR_TOO_MANY_OPEN_FILES
	8,    // ERROR_NOT_ENOUGH_MEMORY
	14,   // ERROR_OUTOFMEMORY
	21,   // ERROR_NOT_READY
	23,   // ERROR_CRC
	25,   // ERROR_SEEK
	27,   // ERROR_SECTOR_NOT_FOUND
	30,   // ERROR_READ_FAULT
	31,   // ERROR_GEN_FAILURE
	59,   // ERROR_UNEXP_NET_ERR
	64,   // ERROR_NETNAME_DELETED
	121,  // ERROR_SEM_TIMEOUT
	1117, // ERROR_IO_DEVICE
	1167, // ERROR_DEVICE_NOT_CONNECTED
	1450, // ERROR_NO_SYSTEM_RESOURCES
	1453, // ERROR_WORKING_SET_QUOTA
	1460, // ERROR_TIMEOUT
}
