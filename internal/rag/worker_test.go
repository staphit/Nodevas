package rag

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// Exercise the pipe/process lifecycle without requiring Python or model downloads.
func TestMiniLMHelperProcess(t *testing.T) {
	if os.Getenv("NODEVAS_EMBED_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var input []string
		if json.Unmarshal(scanner.Bytes(), &input) != nil {
			os.Exit(2)
		}
		switch input[0] {
		case "crash":
			os.Exit(3)
		case "hang":
			time.Sleep(time.Hour)
		case "bad":
			os.Stdout.WriteString("invalid JSON\n")
			continue
		}
		vectors := testVectors(input)
		for _, vector := range vectors {
			vector[0] = float64(os.Getpid())
		}
		if json.NewEncoder(os.Stdout).Encode(vectors) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func testWorker(t *testing.T) *MiniLMWorker {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := MiniLM("unused", nil)
	w.newCommand = func() *exec.Cmd {
		cmd := exec.Command(executable, "-test.run=^TestMiniLMHelperProcess$")
		cmd.Env = append(os.Environ(), "NODEVAS_EMBED_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestWorkerReusesProcessAndSerializesConcurrentCalls(t *testing.T) {
	w := testWorker(t)
	first, err := w.Embed(context.Background(), []string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectors, err := w.Embed(context.Background(), []string{"next", "another"})
			if err != nil {
				t.Error(err)
				return
			}
			if len(vectors) != 2 || vectors[0][0] != first[0][0] || vectors[1][0] != first[0][0] {
				t.Error("worker restarted or mixed responses")
			}
		}()
	}
	wg.Wait()
}

func TestWorkerRestartsAfterFailureAndCancellation(t *testing.T) {
	w := testWorker(t)
	for _, failure := range []string{"crash", "bad", "hang"} {
		if _, err := w.Embed(context.Background(), []string{"warm"}); err != nil {
			t.Fatal(err)
		}
		w.mu.Lock()
		process := w.process
		w.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := w.Embed(ctx, []string{failure})
		cancel()
		if err == nil {
			t.Fatalf("%s succeeded", failure)
		}
		if failure == "hang" && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancellation: %v", err)
		}
		select {
		case <-process.done:
		case <-time.After(3 * time.Second):
			t.Fatal("helper leaked")
		}
		if _, err := w.Embed(context.Background(), []string{"recovered"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerIdleAndCloseReleaseProcess(t *testing.T) {
	w := testWorker(t)
	w.idleTimeout = 50 * time.Millisecond
	if _, err := w.Embed(context.Background(), []string{"warm"}); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	process := w.process
	w.mu.Unlock()
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle helper leaked")
	}
	if _, err := w.Embed(context.Background(), []string{"restart"}); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	process = w.process
	w.mu.Unlock()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	default:
		t.Fatal("Close left helper running")
	}
	if _, err := w.Embed(context.Background(), []string{"closed"}); err == nil {
		t.Fatal("closed worker accepted work")
	}
}
