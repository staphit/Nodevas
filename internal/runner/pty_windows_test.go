//go:build windows

package runner

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// readAll collects a terminal's output until it closes.
func readAll(term Terminal) (func() string, chan struct{}) {
	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return func() string { mu.Lock(); defer mu.Unlock(); return out.String() }, done
}

func waitFor(t *testing.T, output func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", want, output())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConPTYEcho(t *testing.T) {
	if !ptySupported() {
		t.Skip("no ConPTY on this Windows")
	}
	term, err := startPTY(exec.Command("cmd", "/c", "echo hello"), 80, 25)
	if err != nil {
		t.Fatal(err)
	}
	output, done := readAll(term)
	code, err := term.Wait()
	if err != nil || code != 0 {
		t.Fatalf("exit %d: %v", code, err)
	}
	waitFor(t, output, "hello")
	_ = term.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not end after Close")
	}
}

func TestConPTYInputAndResize(t *testing.T) {
	if !ptySupported() {
		t.Skip("no ConPTY on this Windows")
	}
	term, err := startPTY(exec.Command("cmd", "/d", "/q", "/k"), 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	output, _ := readAll(term)
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := term.Write([]byte("echo typed-%OS%-ok\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, output, "typed-Windows_NT-ok") // expanded: it ran, not just echoed
	if _, err := term.Write([]byte("exit 3\r")); err != nil {
		t.Fatal(err)
	}
	exited := make(chan int, 1)
	go func() { code, _ := term.Wait(); exited <- code }()
	select {
	case code := <-exited:
		if code != 3 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(15 * time.Second):
		term.Kill()
		t.Fatal("cmd did not exit")
	}
}
