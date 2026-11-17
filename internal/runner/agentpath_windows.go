//go:build windows

package runner

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// executableFile reports whether the regular file at path (info) is one this
// computer can start as an agent: a program (.exe) or a batch file (.cmd,
// .bat), as npm's shims are. A PowerShell script or a file with no ending is
// not: Windows does not start one on its own.
func executableFile(path string, info fs.FileInfo) bool {
	return info.Mode().IsRegular() && slices.Contains(agentPathExts, strings.ToLower(filepath.Ext(path)))
}
