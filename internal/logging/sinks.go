// The two sinks a standalone hub logs to beside the console's JSON: a file that
// survives the console, and a console writer that cannot hold the server up.

package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RotatingFile is an append-only log file that moves itself aside to
// path+".1" (replacing the previous one) when it would grow past maxBytes, so
// a hub left running for months keeps at most two files' worth of log.
//
// A move that fails (on Windows, another process holding serve.log or
// serve.log.1 open) leaves both files as they were and the log appending to
// the current one; the next try waits until another maxBytes has been written,
// rather than closing and reopening the file on every line.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
	// rotateAt is the size past which the next write rotates: maxBytes, or
	// maxBytes past the size at a rotation that failed.
	rotateAt int64
	// rename is os.Rename; tests make it fail.
	rename func(oldpath, newpath string) error
	// onOpen is told about every file it opens, after a rotation too; serve
	// points the runtime's crash output at it.
	onOpen func(*os.File)
}

// OpenRotatingFile opens (creating the directory) path for appending. A file
// already past maxBytes is rotated first. onOpen may be nil.
func OpenRotatingFile(path string, maxBytes int64, onOpen func(*os.File)) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path, maxBytes: maxBytes, rotateAt: maxBytes, rename: os.Rename, onOpen: onOpen}
	movedAside := true
	if info, err := os.Stat(path); err == nil && maxBytes > 0 && info.Size() >= maxBytes {
		movedAside = r.rename(path, path+".1") == nil
	}
	if err := r.openLocked(); err != nil {
		return nil, err
	}
	if !movedAside {
		r.rotateAt = r.size + maxBytes
	}
	return r, nil
}

// Path is the file being written.
func (r *RotatingFile) Path() string { return r.path }

func (r *RotatingFile) openLocked() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	r.file, r.size = file, info.Size()
	if r.onOpen != nil {
		r.onOpen(file)
	}
	return nil
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.maxBytes > 0 && r.size > 0 && r.size+int64(len(p)) > r.rotateAt {
		r.rotateLocked()
	}
	if r.file == nil {
		return 0, os.ErrClosed
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// rotateLocked closes the file, moves it to .1 and opens a fresh one. Windows
// cannot rename an open file, and the crash output holds a duplicate of the
// handle, so onOpen(nil) lets that go first.
//
// The rename replaces an existing .1 on every OS (MoveFileEx with
// MOVEFILE_REPLACE_EXISTING on Windows); .1 is never deleted first, so a
// rename that fails loses nothing. The same path is then reopened for
// appending, and rotation waits for another maxBytes.
func (r *RotatingFile) rotateLocked() {
	if r.onOpen != nil {
		r.onOpen(nil)
	}
	_ = r.file.Close()
	r.file = nil
	moved := r.rename(r.path, r.path+".1") == nil
	// A failed reopen leaves the sink closed; the console still has the log.
	if err := r.openLocked(); err != nil {
		return
	}
	r.rotateAt = r.maxBytes
	if !moved {
		r.rotateAt = r.size + r.maxBytes
	}
}

// Close closes the file. Later writes fail.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// AsyncWriter hands writes to a goroutine through a bounded queue and never
// blocks the caller: when the queue is full the write is dropped and counted.
//
// It exists for the Windows console. Selecting text in a console window
// (QuickEdit) pauses every write to it until the selection ends; a log line
// written under the logger's lock from an HTTP handler would then hold up every
// other request behind it. The file sink keeps the complete log.
type AsyncWriter struct {
	out     io.Writer
	queue   chan asyncItem
	dropped atomic.Int64
	// unreported is dropped lines not yet announced on the console.
	unreported atomic.Int64
}

type asyncItem struct {
	data    []byte
	flushed chan struct{}
}

// NewAsyncWriter starts the goroutine that writes to out; depth is how many
// writes may wait.
func NewAsyncWriter(out io.Writer, depth int) *AsyncWriter {
	if depth <= 0 {
		depth = 1024
	}
	a := &AsyncWriter{out: out, queue: make(chan asyncItem, depth)}
	go a.drain()
	return a
}

func (a *AsyncWriter) drain() {
	for item := range a.queue {
		if item.flushed != nil {
			close(item.flushed)
			continue
		}
		if n := a.unreported.Swap(0); n > 0 {
			fmt.Fprintf(a.out, "(%d log lines were dropped here while the console was not accepting output; the log file has them)\n", n)
		}
		_, _ = a.out.Write(item.data)
	}
}

// Write queues a copy of p. It always reports success: a log line is not worth
// an error path in every caller.
func (a *AsyncWriter) Write(p []byte) (int, error) {
	select {
	case a.queue <- asyncItem{data: append([]byte(nil), p...)}:
	default:
		a.dropped.Add(1)
		a.unreported.Add(1)
	}
	return len(p), nil
}

// Dropped is how many writes were dropped since the writer started.
func (a *AsyncWriter) Dropped() int64 { return a.dropped.Load() }

// Flush waits, at most timeout, for what is queued to be written.
func (a *AsyncWriter) Flush(timeout time.Duration) bool {
	done := make(chan struct{})
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case a.queue <- asyncItem{flushed: done}:
	case <-deadline.C:
		return false
	}
	select {
	case <-done:
		return true
	case <-deadline.C:
		return false
	}
}
