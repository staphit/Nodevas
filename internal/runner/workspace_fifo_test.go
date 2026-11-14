//go:build !windows

package runner

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// A FIFO the agent left in its worktree is listed as other and refused at
// once, never opened to wait for a writer.
func TestRunReadRefusesFIFO(t *testing.T) {
	env := newWSEnv(t, nil)
	if err := syscall.Mkfifo(filepath.Join(env.wt.Path, "notes.md"), 0o644); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan runnerwire.RepoDone, 1)
	go func() { done <- env.browse(runnerwire.RepoOpRunRead, "notes.md") }()
	select {
	case got := <-done:
		if got.Code != runnerwire.RepoErrBadPath {
			t.Fatalf("read = %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	listing := env.browse(runnerwire.RepoOpRunList, "")
	if listing.Listing == nil {
		t.Fatalf("list = %+v", listing)
	}
	for _, e := range listing.Listing.Entries {
		if e.Name == "notes.md" && e.Type != "other" {
			t.Fatalf("FIFO listed as %q", e.Type)
		}
	}
}
