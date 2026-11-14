//go:build windows

package finalpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A junction, a \\?\ or \\.\ path and a path in other letter case all
// resolve to the folder Windows opens.
func TestResolve(t *testing.T) {
	target, err := Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(target, `\\`) {
		t.Fatalf("Resolve kept the prefix: %q", target)
	}
	link := filepath.Join(t.TempDir(), "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	for _, path := range []string{link, `\\?\` + target, `\\.\` + target, strings.ToUpper(target)} {
		got, err := Resolve(path)
		if err != nil || !strings.EqualFold(got, target) {
			t.Errorf("Resolve(%q) = %q, %v; want %q", path, got, err, target)
		}
	}
	if _, err := Resolve(filepath.Join(target, "missing")); !os.IsNotExist(err) {
		t.Errorf("a missing folder: %v", err)
	}
}
