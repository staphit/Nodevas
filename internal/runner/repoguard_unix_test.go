//go:build !windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/runnerwire"
)

// Every refused class on Linux and macOS, by the rules alone (no file
// system).
func TestRepoGuardUnix(t *testing.T) {
	g := &repoGuard{
		home:        "/home/me",
		homeParents: []string{"/home", "/Users"},
		secret:      []string{"/home/me/Library"},
		system: []string{"/bin", "/boot", "/dev", "/etc", "/proc", "/root", "/run", "/sbin", "/srv", "/sys",
			"/usr", "/var", "/System", "/Library", "/Applications", "/private"},
		systemTop:   []string{"lib*"},
		systemExact: []string{"/opt"},
		nodevas:     []string{"/home/me/.config/nodevas", "/data/runner-data"},
		isRoot:      isFilesystemRoot,
	}
	for path, want := range map[string]string{
		"/":                             runnerwire.RepoRefusedRoot,
		"/Volumes/Backup":               runnerwire.RepoRefusedRoot,
		"/mnt/c":                        runnerwire.RepoRefusedRoot,
		"/media/me/usb":                 runnerwire.RepoRefusedRoot,
		"/home/me":                      runnerwire.RepoRefusedHome,
		"/home":                         runnerwire.RepoRefusedHome,
		"/Users":                        runnerwire.RepoRefusedHome,
		"/home/me/.ssh":                 runnerwire.RepoRefusedSecret,
		"/home/me/.gnupg/private":       runnerwire.RepoRefusedSecret,
		"/home/me/.local/share":         runnerwire.RepoRefusedSecret,
		"/home/me/.claude":              runnerwire.RepoRefusedSecret,
		"/home/me/Library/Keychains":    runnerwire.RepoRefusedSecret,
		"/home/me/.config/nodevas/x":    runnerwire.RepoRefusedNodevas,
		"/data/runner-data/worktrees/r": runnerwire.RepoRefusedNodevas,
		"/data":                         runnerwire.RepoRefusedNodevas,
		"/etc":                          runnerwire.RepoRefusedSystem,
		"/usr/local/src/app":            runnerwire.RepoRefusedSystem,
		"/var/www":                      runnerwire.RepoRefusedSystem,
		"/lib64":                        runnerwire.RepoRefusedSystem,
		"/libexec/x":                    runnerwire.RepoRefusedSystem,
		"/opt":                          runnerwire.RepoRefusedSystem,
		"/private/tmp/x":                runnerwire.RepoRefusedSystem,
		"/home/me/code/app":             "",
		"/home/other/app":               "",
		"/opt/project":                  "",
		"/srvx/app":                     "",
		"/tmp/app":                      "",
	} {
		if got := g.check(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// A symlink in a project folder that leads into .ssh is refused: the folder
// is checked as resolved.
func TestRepoGuardSymlinkIntoSSH(t *testing.T) {
	home, err := resolveFolder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	code := filepath.Join(home, "code")
	for _, dir := range []string{ssh, code} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(code, "keys")
	if err := os.Symlink(ssh, link); err != nil {
		t.Fatal(err)
	}
	c := &Client{Config: &Config{}, Commands: SystemCommands{}, guard: &repoGuard{home: home, isRoot: isFilesystemRoot}}
	inspect, code2, err := c.inspectFolder(context.Background(), link)
	if code2 != "" || inspect.State != runnerwire.RepoStateRefused || inspect.Refused != runnerwire.RepoRefusedSecret {
		t.Fatalf("inspect symlink = %+v, %q, %v", inspect, code2, err)
	}
}
