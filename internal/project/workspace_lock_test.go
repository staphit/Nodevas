package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/lockfile"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

func lockTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// The manager stores the symlink-resolved path, which on macOS and Windows
	// is not the string t.TempDir hands back.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

func TestOpenWorkspaceHoldsCrossProcessLock(t *testing.T) {
	workspace := lockTestWorkspace(t)
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	defer pm.Close()

	path := filepath.Join(workspace, store.DataDir, WorkspaceLockName)
	if path != WorkspaceLockPath(workspace) {
		t.Fatalf("WorkspaceLockPath = %q, want %q", WorkspaceLockPath(workspace), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("workspace lock file: %v", err)
	}
	if _, err := lockfile.Acquire(path); !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("Acquire on an open workspace = %v, want ErrLocked", err)
	}
}

// TestSecondManagerOnTheSameWorkspaceIsRefused stands in for a second process:
// a second manager reaching the same workspace through a different catalog is
// exactly what happens when the desktop app and a CLI are both pointed at it.
func TestSecondManagerOnTheSameWorkspaceIsRefused(t *testing.T) {
	workspace := lockTestWorkspace(t)
	first, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}

	second, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err == nil {
		second.Close()
		first.Close()
		t.Fatal("a second manager opened a workspace that was already held")
	}
	var busy *WorkspaceBusyError
	if !errors.As(err, &busy) {
		first.Close()
		t.Fatalf("second NewManagerAt error = %v, want *WorkspaceBusyError", err)
	}
	if !WorkspacePathEqual(busy.Workspace, workspace) {
		t.Fatalf("busy error names %q, want %q", busy.Workspace, workspace)
	}
	if !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("busy error does not report as ErrLocked: %v", err)
	}

	// Closing the first manager must hand the workspace over cleanly; the lock
	// file itself is left behind and must not block anyone.
	first.Close()
	if _, err := os.Stat(WorkspaceLockPath(workspace)); err != nil {
		t.Fatalf("lock file should survive Close: %v", err)
	}
	third, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	third.Close()
}

func TestSwitchWorkspaceMovesTheLock(t *testing.T) {
	first := lockTestWorkspace(t)
	second := lockTestWorkspace(t)
	pm, err := NewManagerAt(first, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	defer pm.Close()

	if err := pm.SwitchWorkspace(second); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if _, err := lockfile.Acquire(WorkspaceLockPath(second)); !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("new workspace is not locked after a switch: %v", err)
	}
	released, err := lockfile.Acquire(WorkspaceLockPath(first))
	if err != nil {
		t.Fatalf("old workspace still locked after a switch: %v", err)
	}
	if err := released.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireWorkspaceLockReportsTheHolder(t *testing.T) {
	workspace := lockTestWorkspace(t)
	held, err := AcquireWorkspaceLock(workspace)
	if err != nil {
		t.Fatalf("AcquireWorkspaceLock: %v", err)
	}
	defer held.Release()

	_, err = AcquireWorkspaceLock(workspace)
	var busy *WorkspaceBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second AcquireWorkspaceLock = %v, want *WorkspaceBusyError", err)
	}
	if busy.Holder == "" {
		t.Fatalf("busy error carries no holder detail: %v", busy)
	}
}
