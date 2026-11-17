//go:build !windows

package runner

import "io/fs"

// executableFile reports whether the regular file at path (info) is one this
// computer can start: someone may execute it.
func executableFile(_ string, info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
