package adapter

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// antigravityHookPath is exe as a hook command's program: forward slashes and
// nothing `cmd /c` would act on or agy would want quoted, using the 8.3 short
// name when the long one has such characters. "" when neither will do.
func antigravityHookPath(exe string) string {
	if exe == "" {
		return ""
	}
	if ok := hookPathSafe(exe); ok {
		return filepath.ToSlash(exe)
	}
	long, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return ""
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return ""
	}
	short := windows.UTF16ToString(buf[:n])
	if !hookPathSafe(short) {
		return ""
	}
	return filepath.ToSlash(short)
}

// hookPathSafe: printable ASCII with none of cmd.exe's special characters.
func hookPathSafe(p string) bool {
	for _, r := range p {
		if r <= ' ' || r >= 0x7f {
			return false
		}
	}
	return !strings.ContainsAny(p, "\"%^&|<>()!;,='`")
}
