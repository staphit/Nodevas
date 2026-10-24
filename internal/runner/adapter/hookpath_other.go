//go:build !windows

package adapter

import "strings"

// antigravityHookPath is exe as a hook command's program for `sh -c`: in
// single quotes, any single quote in it closed, escaped and reopened.
func antigravityHookPath(exe string) string {
	if exe == "" || strings.ContainsAny(exe, "\x00\n\r") {
		return ""
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
}
