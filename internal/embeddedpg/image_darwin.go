//go:build darwin

package embeddedpg

import (
	"golang.org/x/sys/unix"
)

// processImage is the executable's name, "" when unknown.
func processImage(pid int) string {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return ""
	}
	name := info.Proc.P_comm[:]
	for i, b := range name {
		if b == 0 {
			name = name[:i]
			break
		}
	}
	return string(name)
}
