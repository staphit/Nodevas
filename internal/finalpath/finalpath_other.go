//go:build !windows

package finalpath

import "path/filepath"

// Resolve is path with its symbolic links resolved, clean. The path must
// exist.
func Resolve(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
