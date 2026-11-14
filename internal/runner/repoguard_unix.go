//go:build !windows

package runner

import (
	"os"
	"path/filepath"
	"runtime"

	"nodevas/internal/finalpath"
)

// defaultRepoGuard is this machine's guard (repoguard.go), with nodevas the
// runner's own folders.
func defaultRepoGuard(nodevas []string) *repoGuard {
	home, _ := os.UserHomeDir()
	parents := []string{"/home", "/Users"}
	if home != "" {
		parents = append(parents, filepath.Dir(home))
	}
	secret := []string{os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_DATA_HOME"), os.Getenv("XDG_STATE_HOME")}
	if home != "" {
		secret = append(secret, filepath.Join(home, "Library"))
	}
	g := &repoGuard{
		fold:        runtime.GOOS == "darwin",
		home:        home,
		homeParents: withResolved(dedupe(parents)),
		secret:      withResolved(dedupe(secret)),
		system: withResolved([]string{"/bin", "/boot", "/dev", "/etc", "/proc", "/root", "/run", "/sbin", "/srv", "/sys",
			"/usr", "/var", "/System", "/Library", "/Applications", "/private"}),
		systemTop:   []string{"lib*"},
		systemExact: []string{"/opt"},
		nodevas:     withResolved(dedupe(nodevas)),
		isRoot:      isFilesystemRoot,
	}
	if resolved, err := resolveFolder(home); err == nil && home != "" && resolved != filepath.Clean(home) {
		// Home is a symlink: its target is home as well.
		g.homeLike = append(g.homeLike, resolved)
	}
	return g
}

// isFilesystemRoot reports / and the usual mount points of other disks:
// /Volumes/X (macOS), /mnt/X (WSL's drives among them), /media/X and
// /media/<user>/X.
func isFilesystemRoot(path string) bool {
	path = filepath.Clean(path)
	if path == "/" {
		return true
	}
	parent := filepath.Dir(path)
	return parent == "/Volumes" || parent == "/mnt" || parent == "/media" || filepath.Dir(parent) == "/media"
}

// resolveFolder is path with its symlinks resolved (finalpath.Resolve).
func resolveFolder(path string) (string, error) {
	return finalpath.Resolve(path)
}
