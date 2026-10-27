package logging

import (
	"bytes"
	"os"
	"path/filepath"
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
