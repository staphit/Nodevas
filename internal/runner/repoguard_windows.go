//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/finalpath"
)

// defaultRepoGuard is this machine's guard (repoguard.go), with nodevas the
// runner's own folders.
func defaultRepoGuard(nodevas []string) *repoGuard {
	home, _ := os.UserHomeDir()
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	or := func(name, fallback string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return fallback
	}
	g := &repoGuard{
		fold: true,
		home: home,
		homeParents: withResolved(dedupe([]string{
			filepath.Join(drive+`\`, "Users"), parentOf(home),
		})),
		homeLike: withResolved(dedupe([]string{
			os.Getenv("OneDrive"), os.Getenv("OneDriveCommercial"), os.Getenv("OneDriveConsumer"),
		})),
		secret: withResolved(dedupe([]string{os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")})),
		system: withResolved(dedupe([]string{
			or("SystemRoot", drive+`\Windows`),
			or("ProgramFiles", drive+`\Program Files`),
			or("ProgramFiles(x86)", drive+`\Program Files (x86)`),
			os.Getenv("ProgramW6432"),
			or("ProgramData", drive+`\ProgramData`),
		})),
		systemTop: []string{"$Recycle.Bin", "System Volume Information"},
		nodevas:   withResolved(dedupe(nodevas)),
		isRoot:    isFilesystemRoot,
	}
	if resolved, err := resolveFolder(home); err == nil && home != "" && !strings.EqualFold(resolved, home) {
		// Home is a junction: its target is home as well.
		g.homeLike = append(g.homeLike, resolved)
	}
	return g
}

func parentOf(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Dir(dir)
}

// isFilesystemRoot reports a drive root (C:\) or a share root (\\server\share).
func isFilesystemRoot(path string) bool {
	volume := filepath.VolumeName(path)
	rest := path[len(volume):]
	return rest == "" || rest == `\` || rest == "/"
}

// resolveFolder is the folder path finally names, symlinks and junctions
// resolved: what Windows itself opens (finalpath.Resolve).
// filepath.EvalSymlinks leaves junctions as they are.
func resolveFolder(path string) (string, error) {
	return finalpath.Resolve(path)
}
