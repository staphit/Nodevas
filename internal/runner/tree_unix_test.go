//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether pid is a running process. A zombie (killed,
// not yet reaped by whoever inherited it) counts as dead.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no procfs (macOS): signal 0 is all there is
	}
	// pid (comm) state …; comm may contain spaces, so read after the last ')'.
	s := string(stat)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}

// An agent CLI asked for its version or its models may start children that
// outlive it and hold its output open (an update check, a local server). Run
// still ends at its timeout, or a moment after the CLI itself exits, and
// takes those children with it.
func TestSystemRunEndsWithItsWholeTree(t *testing.T) {
	childOf := func(pidFile string) int {
		t.Helper()
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		return pid
	}
	gone := func(pid int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Errorf("child %d outlived the command", pid)
		}
	}

	// It never answers: cut at the timeout, child and all.
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := SystemCommands{}.Run(ctx, "sh", "-c", `sleep 60 & echo $! > "$0"; wait`, pidFile)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("Run took %s with a 200ms timeout", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the timeout", err)
	}
	gone(childOf(pidFile))

	// It answers and exits, leaving a child that holds its output: the
	// answer counts, after a short wait for the rest of it.
	pidFile = filepath.Join(t.TempDir(), "pid")
	start = time.Now()
	out, err := SystemCommands{}.Run(context.Background(), "sh", "-c", `sleep 60 & echo $! > "$0"; echo 1.2.3`, pidFile)
	if took := time.Since(start); took > outputGrace+2*time.Second {
		t.Errorf("Run took %s for a command that exited at once", took)
	}
	if err != nil || out != "1.2.3\n" {
		t.Errorf("Run = %q, %v", out, err)
	}
	gone(childOf(pidFile))
}
