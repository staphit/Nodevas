//go:build !windows

package runner

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive reports whether pid is a running process. A zombie (killed,
// not yet reaped by whoever inherited it) counts as dead.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no procfs (macOS): signal 0 is all there is
	}
	// pid (comm) state …; comm may contain spaces, so read after the last ')'.
	s := string(stat)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}
