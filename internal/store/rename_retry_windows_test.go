//go:build windows

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A reader holding graph.yaml open for a moment must not fail a write that
// replaces it: on Windows the replace is refused until the reader closes.
func TestAtomicWriteWaitsOutAReaderOnWindows(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "graph.yaml")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		reader.Close()
		close(released)
	}()
	if err := WriteProjectFileAtomic(root, path, []byte("new\n")); err != nil {
		t.Fatalf("write while a reader held the file: %v", err)
	}
	<-released
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Fatalf("graph.yaml = %q, want the new content", got)
	}
}
