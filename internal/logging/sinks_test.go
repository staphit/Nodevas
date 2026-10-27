package logging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRotatingFileMovesAsideAtTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "serve.log")
	var opened []bool
	file, err := OpenRotatingFile(path, 32, func(f *os.File) { opened = append(opened, f != nil) })
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	first := strings.Repeat("a", 20) + "\n"
	second := strings.Repeat("b", 20) + "\n"
	for _, line := range []string{first, second} {
		if _, err := file.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(path + ".1"); string(got) != first {
		t.Fatalf("rotated file = %q, want %q", got, first)
	}
	if got, _ := os.ReadFile(path); string(got) != second {
		t.Fatalf("current file = %q, want %q", got, second)
	}
	// Opened, released for the rename, opened again.
	if want := []bool{true, false, true}; len(opened) != 3 || opened[0] != want[0] || opened[1] != want[1] || opened[2] != want[2] {
		t.Fatalf("onOpen calls = %v, want %v", opened, want)
	}
}

func TestRotatingFileAppendsAndRotatesAnOversizedFileAtOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenRotatingFile(path, 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("new\n"))
	_ = file.Close()
	if got, _ := os.ReadFile(path); string(got) != "old\nnew\n" {
		t.Fatalf("appended file = %q", got)
	}

	file, err = OpenRotatingFile(path, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if got, _ := os.ReadFile(path + ".1"); string(got) != "old\nnew\n" {
		t.Fatalf("an oversized file was not rotated at open: %q", got)
	}
}

// A rename that fails (another process holding the log open) keeps the old
// .1, keeps appending to the current file, and is not tried again on the very
// next write: only once another maxBytes has been written.
func TestRotatingFileBacksOffAfterAFailedRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(path+".1", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	file, err := OpenRotatingFile(path, 32, func(f *os.File) {
		if f != nil {
			opens++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	renames := 0
	failing := true
	file.rename = func(oldpath, newpath string) error {
		renames++
		if failing {
			return errors.New("held open")
		}
		return os.Rename(oldpath, newpath)
	}
	line := []byte(strings.Repeat("x", 9) + "\n") // 10 bytes
	write := func(times int) {
		t.Helper()
		for range times {
			if _, err := file.Write(line); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 30 bytes fit; the 4th line tries to rotate and fails.
	write(4)
	if renames != 1 || opens != 2 {
		t.Fatalf("after the first failure: renames=%d opens=%d, want 1 and 2", renames, opens)
	}
	// 30 + 32: no retry until a write would take the file past 62 bytes.
	write(2)
	if renames != 1 || opens != 2 {
		t.Fatalf("retried before another maxBytes: renames=%d opens=%d", renames, opens)
	}
	if got, _ := os.ReadFile(path + ".1"); string(got) != "older\n" {
		t.Fatalf("the old .1 was lost: %q", got)
	}
	if got, _ := os.ReadFile(path); len(got) != 60 {
		t.Fatalf("current file has %d bytes, want every line (60)", len(got))
	}
	failing = false
	write(1)
	if renames != 2 {
		t.Fatalf("renames=%d, want a retry past 62 bytes", renames)
	}
	if got, _ := os.ReadFile(path + ".1"); len(got) != 60 {
		t.Fatalf("rotated file has %d bytes, want 60", len(got))
	}
	if got, _ := os.ReadFile(path); string(got) != string(line) {
		t.Fatalf("current file = %q", got)
	}
	// Rotated for real: back to the plain limit.
	write(3)
	if renames != 3 {
		t.Fatalf("renames=%d, want a rotation at the plain limit again", renames)
	}
}

// On Windows a second handle on serve.log is what makes the rename fail.
func TestRotatingFileHeldOpenOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to rename an open file")
	}
	path := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(path+".1", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenRotatingFile(path, 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for range 6 {
		if _, err := file.Write([]byte(strings.Repeat("x", 9) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(path + ".1"); string(got) != "older\n" {
		t.Fatalf("the old .1 was lost: %q", got)
	}
	if got, _ := os.ReadFile(path); len(got) != 60 {
		t.Fatalf("current file has %d bytes, want 60", len(got))
	}
	file.mu.Lock()
	rotateAt := file.rotateAt
	file.mu.Unlock()
	if rotateAt != 30+32 {
		t.Fatalf("rotateAt = %d, want 62 (backed off from the failed rename at 30)", rotateAt)
	}
}

// blockingWriter stands in for a console paused by a selection.
type blockingWriter struct {
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *blockingWriter) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestAsyncWriterNeverBlocksAndCountsDrops(t *testing.T) {
	out := &blockingWriter{release: make(chan struct{})}
	writer := NewAsyncWriter(out, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			if n, err := writer.Write([]byte("line\n")); n != 5 || err != nil {
				t.Errorf("Write = %d, %v", n, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writes blocked on a paused console")
	}
	if writer.Dropped() == 0 {
		t.Fatal("no write was counted as dropped")
	}
	close(out.release)
	if !writer.Flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}
	_, _ = writer.Write([]byte("after\n"))
	if !writer.Flush(5 * time.Second) {
		t.Fatal("flush timed out")
	}
	got := out.String()
	if !strings.Contains(got, "log lines were dropped") || !strings.HasSuffix(got, "after\n") {
		t.Fatalf("console got %q", got)
	}
}
