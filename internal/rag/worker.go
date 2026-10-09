package rag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type miniLMProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *bufio.Scanner
	done   chan struct{}
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
	cmd.Stderr = w.stderr
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
	p := &miniLMProcess{cmd: cmd, stdin: stdin, output: bufio.NewScanner(stdout), done: make(chan struct{})}
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
		return nil, fmt.Errorf("MiniLM helper failed; install scripts/rag-requirements.txt in --rag-python's environment (diagnostics on stderr); retry to restart the worker")
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
