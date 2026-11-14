//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The hub's own data is recognised however the folder is named: through a
// junction, with a \\?\ prefix, in other letter case, or as a folder that
// holds it; a folder beside it is not.
func TestHubDataFolderSeesThroughWindowsNames(t *testing.T) {
	s := serverForTest(t, projectManagerForTest(t), nil, nil)
	workspace := resolvedFolder(s.pm.Workspace())
	if err := os.MkdirAll(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "ws-link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, workspace).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	for _, path := range []string{workspace, link, filepath.Join(link, "sub"), `\\?\` + workspace, strings.ToUpper(workspace),
		filepath.Dir(workspace), resolvedFolder(s.pm.CatalogRoot())} {
		if !s.hubDataFolder(path) {
			t.Errorf("%s: not the hub's own data", path)
		}
	}
	if beside := t.TempDir(); s.hubDataFolder(beside) {
		t.Errorf("%s: a folder beside the workspace counts as the hub's own data", beside)
	}
}
