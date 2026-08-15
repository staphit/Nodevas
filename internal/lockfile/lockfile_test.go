package lockfile_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nodevas/internal/lockfile"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "sub", "workspace.lock")
}

func TestAcquireCreatesLockAndParentDirectory(t *testing.T) {
	path := lockPath(t)
	lock, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	if lock.Path() != path {
		t.Fatalf("Path() = %q, want %q", lock.Path(), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	owner, ok := lockfile.ReadOwner(path)
	if !ok {
		t.Fatalf("lock file carries no readable owner record")
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("owner pid = %d, want %d", owner.PID, os.Getpid())
	}
	if owner.Since == "" {
		t.Fatalf("owner record has no timestamp: %+v", owner)
	}
	if describe := lockfile.Describe(path); !strings.Contains(describe, "pid") {
		t.Fatalf("Describe = %q, want a pid mention", describe)
	}
}

func TestSecondAcquireReportsErrLocked(t *testing.T) {
	path := lockPath(t)
	first, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	second, err := lockfile.Acquire(path)
	if err == nil {
		_ = second.Release()
		t.Fatal("second Acquire succeeded while the first was held")
	}
	if !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("second Acquire error = %v, want ErrLocked", err)
	}
}

func TestReleaseAllowsReacquire(t *testing.T) {
	path := lockPath(t)
	first, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Release must be idempotent: shutdown paths call it beside a deferred one.
	if err := first.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}

	second, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("re-Acquire after Release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release second: %v", err)
	}
}

// TestStaleLockFileDoesNotBlock is the property that rules out a pid file: the
// lock lives on the descriptor, so a file left on disk by a previous run --
// even one naming a pid that is no longer ours -- must not block anyone.
func TestStaleLockFileDoesNotBlock(t *testing.T) {
	path := lockPath(t)
	first, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file should survive Release: %v", err)
	}

	second, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("stale lock file blocked Acquire: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireWaitReturnsOnceTheHolderReleases(t *testing.T) {
	path := lockPath(t)
	held, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = held.Release()
	}()

	lock, err := lockfile.AcquireWait(path, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireWait: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireWaitGivesUpWhileStillHeld(t *testing.T) {
	path := lockPath(t)
	held, err := lockfile.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	if _, err := lockfile.AcquireWait(path, 50*time.Millisecond); !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("AcquireWait error = %v, want ErrLocked", err)
	}
}

// holdLockEnv makes the test binary act as a helper process: it takes the lock
// at the named path, announces it, then blocks until killed.
const holdLockEnv = "NODEVAS_TEST_HOLD_LOCK"

func TestMain(m *testing.M) {
	if path := os.Getenv(holdLockEnv); path != "" {
		lock, err := lockfile.Acquire(path)
		if err != nil {
			os.Stderr.WriteString("helper: " + err.Error() + "\n")
			os.Exit(3)
		}
		_ = lock // held for the life of this process, never released
		os.Stdout.WriteString("locked\n")
		// No Release, no clean exit: the parent kills this process, which is
		// exactly the case under test. Sleeping rather than blocking forever
		// keeps Go's deadlock detector from ending the process for us.
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestKilledHolderReleasesLock is the guarantee the desktop app depends on. The
// Electron parent spawns and may hard-kill the backend; if a killed process
// left the workspace locked, the next launch would never start.
func TestKilledHolderReleasesLock(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary to re-invoke: %v", err)
	}
	path := lockPath(t)

	helper := exec.Command(executable)
	helper.Env = append(os.Environ(), holdLockEnv+"="+path)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		t.Skipf("cannot spawn a helper process on %s: %v", runtime.GOOS, err)
	}
	defer func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	}()

	ready := make(chan error, 1)
	go func() {
		buf := make([]byte, len("locked\n"))
		_, readErr := stdout.Read(buf)
		ready <- readErr
	}()
	select {
	case readErr := <-ready:
		if readErr != nil {
			t.Fatalf("helper never reported the lock: %v", readErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper did not acquire the lock in time")
	}

	// While the helper lives, we must be locked out.
	if _, err := lockfile.Acquire(path); !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("Acquire while helper holds the lock = %v, want ErrLocked", err)
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_, _ = helper.Process.Wait()

	// The OS releases the lock as the process dies; on Windows the handle
	// teardown is not instantaneous, so allow a brief settle.
	lock, err := lockfile.AcquireWait(path, 10*time.Second)
	if err != nil {
		t.Fatalf("lock not released by the killed holder: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
