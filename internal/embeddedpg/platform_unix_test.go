//go:build !windows

package embeddedpg

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func assertOwnGroup(t *testing.T, attr *syscall.SysProcAttr) {
	t.Helper()
	if attr == nil || !attr.Setpgid {
		t.Fatal("postgres is not put in its own process group (Setpgid): a terminal Ctrl+C would reach it")
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("%s mode = %o, want 600", path, mode)
	}
}
