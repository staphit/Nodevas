package rag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type miniLMProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *bufio.Scanner
	done   chan struct{}
	// stderr keeps the helper's last words, so a failure can say why
	// instead of guessing.
	stderr *tailBuffer
}

// tailBufferSize is how much of the helper's stderr a failure can quote.
const tailBufferSize = 4 << 10

// tailBuffer keeps the last tailBufferSize bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - tailBufferSize; extra > 0 {
		t.buf = append(t.buf[:0:0], t.buf[extra:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// helperFailure turns the helper's stderr into the error a user can act on.
// Only a missing module earns the install advice; anything else quotes the
// helper's own last line.
func helperFailure(stderr string) error {
	if strings.Contains(stderr, "ModuleNotFoundError") || strings.Contains(stderr, "ImportError") ||
		strings.Contains(stderr, "No module named") {
		return fmt.Errorf("MiniLM helper failed; install scripts/rag-requirements.txt in --rag-python's environment (diagnostics on stderr); retry to restart the worker")
	}
	lines := strings.Split(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > 300 {
			line = string(runes[:300]) + "…"
		}
		return fmt.Errorf("MiniLM helper failed: %s; retry to restart the worker", line)
	}
	return fmt.Errorf("MiniLM helper failed (no diagnostics); retry to restart the worker")
}

// MiniLMWorker owns one lazy Python process. It serializes requests, closes on
// cancellation/failure, and releases the model after a minute without work.
type MiniLMWorker struct {
	newCommand  func() *exec.Cmd
	stderr      io.Writer
	gate        chan struct{}
	mu          sync.Mutex
	process     *miniLMProcess
	idle        *time.Timer
	idleTimeout time.Duration
	generation  uint64
	closed      bool
}

func (w *MiniLMWorker) startLocked() (*miniLMProcess, error) {
	if p := w.process; p != nil {
		select {
		case <-p.done:
			w.stopLocked()
		default:
			return p, nil
		}
	}
	cmd := w.newCommand()
	configureWorkerCommand(cmd)
	tail := &tailBuffer{}
	if w.stderr != nil {
		cmd.Stderr = io.MultiWriter(w.stderr, tail)
	} else {
		cmd.Stderr = tail
	}
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start MiniLM: %w; set --rag-python to a Python environment with chromadb installed", err)
	}
	p := &miniLMProcess{cmd: cmd, stdin: stdin, output: bufio.NewScanner(stdout), done: make(chan struct{}), stderr: tail}
	// A 32-vector response is normally under 300 KiB. Reject runaway output.
	p.output.Buffer(make([]byte, 4096), 2<<20)
	go func() { _ = cmd.Wait(); close(p.done) }()
	w.process = p
	return p, nil
}

func (w *MiniLMWorker) stopLocked() {
	if w.idle != nil {
		w.idle.Stop()
		w.idle = nil
	}
	if p := w.process; p != nil {
		select {
		case <-p.done:
		default:
			terminateWorkerProcess(p.cmd)
		}
		_ = p.stdin.Close()
		<-p.done
		w.process = nil
	}
}

func (w *MiniLMWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked()
	return nil
}

func (w *MiniLMWorker) Embed(ctx context.Context, input []string) ([][]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) == 0 {
		return [][]float64{}, nil
	}
	if len(input) > embeddingBatchSize {
		return nil, fmt.Errorf("MiniLM accepts at most %d texts per batch", embeddingBatchSize)
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, fmt.Errorf("MiniLM worker is closed")
	}
	if w.idle != nil {
		w.idle.Stop()
	}
	w.generation++
	generation := w.generation
	p, err := w.startLocked()
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}
	type response struct {
		vectors [][]float64
		err     error
	}
	reply := make(chan response, 1)
	go func() {
		var result response
		if result.err = json.NewEncoder(p.stdin).Encode(input); result.err == nil {
			if p.output.Scan() {
				result.err = json.Unmarshal(p.output.Bytes(), &result.vectors)
			} else {
				result.err = io.ErrUnexpectedEOF
			}
		}
		reply <- result
	}()
	var result response
	select {
	case result = <-reply:
	case <-ctx.Done():
		w.mu.Lock()
		if w.process == p {
			w.stopLocked()
		}
		w.mu.Unlock()
		<-reply // A killed helper cannot leave an exchange goroutine behind.
		return nil, ctx.Err()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if result.err != nil || len(result.vectors) != len(input) {
		if w.process == p {
			w.stopLocked()
		}
		// Wait (bounded by the command's WaitDelay) for the helper to be
		// reaped, so its stderr has been copied in full before quoting it.
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
		return nil, helperFailure(p.stderr.String())
	}
	if !w.closed && w.process == p {
		w.idle = time.AfterFunc(w.idleTimeout, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.generation == generation && w.process == p {
				w.stopLocked()
			}
		})
	}
	return result.vectors, nil
}
