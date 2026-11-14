//go:build windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"nodevas/internal/runnerwire"
)

// Every refused class on Windows, by the rules alone (no file system).
func TestRepoGuardWindows(t *testing.T) {
	g := &repoGuard{
		fold:        true,
		home:        `C:\Users\me`,
		homeParents: []string{`C:\Users`},
		homeLike:    []string{`C:\Users\me\OneDrive`},
		secret:      []string{`C:\Users\me\AppData\Roaming`, `C:\Users\me\AppData\Local`},
		system:      []string{`C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`},
		systemTop:   []string{"$Recycle.Bin", "System Volume Information"},
		nodevas:     []string{`C:\Users\me\AppData\Roaming\nodevas`, `D:\work\runner-data`},
		isRoot:      isFilesystemRoot,
	}
	for path, want := range map[string]string{
		`C:\`:                                 runnerwire.RepoRefusedRoot,
		`D:\`:                                 runnerwire.RepoRefusedRoot,
		`\\server\share`:                      runnerwire.RepoRefusedRoot,
		`\\server\share\`:                     runnerwire.RepoRefusedRoot,
		`C:\Users\me`:                         runnerwire.RepoRefusedHome,
		`c:\USERS\ME`:                         runnerwire.RepoRefusedHome,
		`C:\Users`:                            runnerwire.RepoRefusedHome,
		`C:\Users\me\OneDrive`:                runnerwire.RepoRefusedHome,
		`C:\Users\me\.ssh`:                    runnerwire.RepoRefusedSecret,
		`C:\Users\me\.ssh\keys`:               runnerwire.RepoRefusedSecret,
		`C:\Users\me\.claude\projects`:        runnerwire.RepoRefusedSecret,
		`C:\Users\me\.config`:                 runnerwire.RepoRefusedSecret,
		`C:\Users\me\AppData`:                 runnerwire.RepoRefusedSecret,
		`C:\Users\me\AppData\Local\Temp\app`:  runnerwire.RepoRefusedSecret,
		`C:\Windows\System32`:                 runnerwire.RepoRefusedSystem,
		`C:\Program Files\App`:                runnerwire.RepoRefusedSystem,
		`c:\program files (x86)`:              runnerwire.RepoRefusedSystem,
		`C:\ProgramData\x`:                    runnerwire.RepoRefusedSystem,
		`D:\$Recycle.Bin\S-1`:                 runnerwire.RepoRefusedSystem,
		`E:\System Volume Information`:        runnerwire.RepoRefusedSystem,
		`C:\Users\me\AppData\Roaming\nodevas`: runnerwire.RepoRefusedNodevas,
		`D:\work\runner-data\worktrees\r1`:    runnerwire.RepoRefusedNodevas,
		`D:\work`:                             runnerwire.RepoRefusedNodevas,
		`C:\Users\me\code\app`:                "",
		`C:\Users\me\OneDrive\Documents\app`:  "",
		`C:\Users\other\app`:                  "",
		`D:\src\app`:                          "",
		`C:\Windowsish\app`:                   "",
		`D:\work2\app`:                        "",
	} {
		if got := g.check(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// A junction in a project folder that leads into .ssh is refused: the folder
// is checked as Windows finally opens it, and filepath.EvalSymlinks leaves
// junctions alone.
func TestRepoGuardJunctionIntoSSH(t *testing.T) {
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
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, ssh).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	if resolved, err := resolveFolder(link); err != nil || !samePath(resolved, ssh) {
		t.Fatalf("resolveFolder(junction) = %q, %v; want %q", resolved, err, ssh)
	}
	c := &Client{Config: &Config{}, Commands: SystemCommands{}, guard: &repoGuard{fold: true, home: home, isRoot: isFilesystemRoot}}
	inspect, code2, err := c.inspectFolder(context.Background(), link)
	if code2 != "" || inspect.State != runnerwire.RepoStateRefused || inspect.Refused != runnerwire.RepoRefusedSecret {
		t.Fatalf("inspect junction = %+v, %q, %v", inspect, code2, err)
	}
}

// Git for Windows walks into a junction as into a folder: a folder that is
// no Git project yet and holds a junction into .ssh is refused, not offered
// a first version that would save the keys.
func TestRepoAdminInitRefusesAJunctionInside(t *testing.T) {
	e := newAdminEnv(t)
	ssh := filepath.Join(e.home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "id_work"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := e.folder(t, "app", map[string]string{"main.go": "package main\n"})
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(app, "keys"), ssh).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: app})
	if a.done.Code != "" || a.done.Inspect == nil || a.done.Inspect.State != runnerwire.RepoStateRefused ||
		a.done.Inspect.Refused != runnerwire.RepoRefusedSecret || a.done.Inspect.Init != nil {
		t.Fatalf("inspect = %+v", a.done)
	}
	add := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app, InitPlan: "any"})
	if add.done.Code != runnerwire.RepoAdminErrSensitive || add.changed {
		t.Fatalf("init_add = %+v", add.done)
	}
	if _, err := os.Stat(filepath.Join(app, ".git")); err == nil {
		t.Fatal("init ran on a folder with a junction into .ssh")
	}
}
