//go:build windows

package finalpath

import (
	"io/fs"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve is the folder path finally names, symbolic links and junctions
// resolved: what Windows itself opens (GetFinalPathNameByHandle), as a drive
// letter path or \\server\share\…. filepath.EvalSymlinks leaves junctions as
// they are. The path must exist.
func Resolve(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: path, Err: err}
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
	if err != nil {
		return "", &fs.PathError{Op: "resolve", Path: path, Err: err}
	}
	final := windows.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(final, `\\?\UNC\`):
		final = `\\` + final[len(`\\?\UNC\`):]
	case strings.HasPrefix(final, `\\?\`):
		final = final[len(`\\?\`):]
	}
	return filepath.Clean(final), nil
}

// volumeNameDOS asks GetFinalPathNameByHandle for a drive-letter path
// (VOLUME_NAME_DOS | FILE_NAME_NORMALIZED, both 0).
const volumeNameDOS = 0
