//go:build windows

package runner

import (
	"sync"

	"golang.org/x/sys/windows"
)

var ptyCheck = sync.OnceValue(func() bool {
	// ConPTY arrived in Windows 10 1809. x/sys calls the API through a lazy
	// proc that panics when it is missing, so look before using it.
	return windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find() == nil
})

// ptySupported reports whether this Windows has ConPTY.
func ptySupported() bool { return ptyCheck() }
