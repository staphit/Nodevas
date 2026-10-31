package embeddedpg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// postmaster is one started PostgreSQL server process.
type postmaster struct {
	cmd      *exec.Cmd
	pid      int
	binaries string
	dataDir  string
	output   *tail
	done     chan struct{}
	waitErr  error
	extra    procExtra
}

// exited reports whether the process has ended.
func (p *postmaster) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// waitExit waits up to d for the process to end.
func (p *postmaster) waitExit(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

// tail keeps the last bytes a process wrote, for an error message. It never
// holds a credential: nothing Nodevas passes PostgreSQL on its command line or
// environment is secret.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailBytes = 16 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailBytes:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// pgEnv is the environment PostgreSQL's programs run with: this process's,
// minus the libpq and server variables that could point them elsewhere.
func pgEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "PG") || strings.HasPrefix(upper, "NODEVAS_DATABASE") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// command builds a PostgreSQL program invocation: its own process group, no
// console window, a scrubbed environment.
func command(ctx context.Context, binaries, program string, args ...string) *exec.Cmd {
	path := binPath(binaries, program)
	var cmd *exec.Cmd
	if ctx == nil {
		cmd = exec.Command(path, args...)
	} else {
		cmd = exec.CommandContext(ctx, path, args...)
	}
	cmd.Env = pgEnv()
	cmd.SysProcAttr = spawnAttrs()
	return cmd
}

// run runs a PostgreSQL program to completion and returns its output in the
// error.
func run(ctx context.Context, binaries, program string, args ...string) error {
	cmd := command(ctx, binaries, program, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", program, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// startPostmaster spawns postgres with args. Its output goes to a pipe this
// process drains, never to a file descriptor PostgreSQL's children could keep
// a Wait waiting on.
func startPostmaster(binaries, dataDir string, args []string) (*postmaster, error) {
	cmd := command(nil, binaries, "postgres", args...)
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = writer, writer
	proc := &postmaster{cmd: cmd, binaries: binaries, dataDir: dataDir, output: &tail{}, done: make(chan struct{})}
	if err := launch(proc); err != nil {
		reader.Close()
		writer.Close()
		return nil, fmt.Errorf("start postgres: %w", err)
	}
	writer.Close()
	go func() {
		_, _ = io.Copy(proc.output, reader)
		reader.Close()
	}()
	return proc, nil
}

// stopPostmaster shuts a server down fast (open transactions roll back, a
// shutdown checkpoint is written), escalating if it does not go.
func stopPostmaster(ctx context.Context, proc *postmaster) error {
	if proc == nil || proc.exited() {
		releaseProc(proc)
		return nil
	}
	err := requestFastShutdown(ctx, proc)
	if proc.waitExit(15 * time.Second) {
		releaseProc(proc)
		return nil
	}
	immediateShutdown(proc)
	if proc.waitExit(5 * time.Second) {
		releaseProc(proc)
		return errors.Join(err, errors.New("embeddedpg: the server needed an immediate shutdown"))
	}
	_ = proc.cmd.Process.Kill()
	proc.waitExit(5 * time.Second)
	releaseProc(proc)
	return errors.Join(err, errors.New("embeddedpg: the server had to be killed"))
}

// stopOrphan stops a postmaster this process did not start (an orphan of a
// crashed owner) and waits for it to end.
func stopOrphan(ctx context.Context, binaries, dataDir string, pid int) error {
	requestOrphanShutdown(ctx, binaries, dataDir, pid)
	if waitPIDGone(pid, 15*time.Second) {
		return nil
	}
	killPID(pid)
	if waitPIDGone(pid, 5*time.Second) {
		return nil
	}
	return fmt.Errorf("embeddedpg: orphaned postmaster %d did not stop", pid)
}

func waitPIDGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !pidAlive(pid)
}
