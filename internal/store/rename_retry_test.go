package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRenameWithRetryReturnsOtherErrorsAtOnce(t *testing.T) {
	calls := 0
	want := errors.New("disk full")
	err := renameWithRetry(func() error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err = %v after %d calls; want the error after one call", err, calls)
	}
}

// On Windows a target held open without FILE_SHARE_DELETE cannot be replaced
// until it is closed; the write succeeds once the other handle lets go.
func TestWriteProjectFileAtomicWaitsForAHeldTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("POSIX rename replaces an open target")
	}
	root := t.TempDir()
	path := filepath.Join(root, "graph.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		held.Close()
	}()
	if err := WriteProjectFileAtomic(root, "graph.yaml", []byte("new")); err != nil {
		t.Fatalf("write while briefly held: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("got %q, %v; want new", got, err)
	}
}
