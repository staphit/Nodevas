package runner

import (
	"errors"
	"os/exec"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

// Terminal is an agent's TUI running in a pseudo-terminal: what an
// interactive run reads its screen from and types into. The real one is a
// ConPTY on Windows and a Unix pty elsewhere (github.com/aymanbagabas/go-pty);
// tests use a fake.
type Terminal interface {
	// Read returns the program's output. It returns an error once the
	// terminal is closed.
	Read(p []byte) (int, error)
	// Write types into the program.
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	// Wait waits for the program to exit and returns its exit code.
	Wait() (exitCode int, err error)
	// Kill ends the program and everything it started.
	Kill()
	// Close releases the terminal; a Read in progress returns.
	Close() error
}

// TerminalStarter starts cmd (built but not started, with no stdio set) in a
// new terminal of the given size.
type TerminalStarter func(cmd *exec.Cmd, cols, rows int) (Terminal, error)

// startPTY is the real TerminalStarter.
func startPTY(cmd *exec.Cmd, cols, rows int) (Terminal, error) {
	if cmd.Path == "" || len(cmd.Args) == 0 {
		return nil, errors.New("no program to start")
	}
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	p, err := gopty.New()
	if err != nil {
		return nil, err
	}
	if err := p.Resize(cols, rows); err != nil {
		_ = p.Close()
		return nil, err
	}
	c := p.Command(cmd.Path, cmd.Args[1:]...)
	c.Args = append([]string(nil), cmd.Args...)
	c.Dir = cmd.Dir
	c.Env = cmd.Env
	// On Windows this carries a .cmd shim's explicit cmd.exe command line
	// (adapter.wrapBatch), which go-pty passes to CreateProcess as it is.
	c.SysProcAttr = cmd.SysProcAttr
	if err := c.Start(); err != nil {
		_ = p.Close()
		return nil, err
	}
	return &ptyTerminal{pty: p, cmd: c, tree: attachPID(c.Process.Pid)}, nil
}

type ptyTerminal struct {
	pty  gopty.Pty
	cmd  *gopty.Cmd
	tree *procTree

	closeOnce sync.Once
	closeErr  error
}

func (t *ptyTerminal) Read(p []byte) (int, error)  { return t.pty.Read(p) }
func (t *ptyTerminal) Write(p []byte) (int, error) { return t.pty.Write(p) }
func (t *ptyTerminal) Resize(cols, rows int) error { return t.pty.Resize(cols, rows) }

func (t *ptyTerminal) Wait() (int, error) {
	err := t.cmd.Wait()
	code := -1
	if t.cmd.ProcessState != nil {
		code = t.cmd.ProcessState.ExitCode()
	}
	return code, err
}

func (t *ptyTerminal) Kill() {
	t.tree.kill()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

func (t *ptyTerminal) Close() error {
	t.closeOnce.Do(func() {
		t.closeErr = t.pty.Close()
		t.tree.close()
	})
	return t.closeErr
}

// terminalSupported says whether interactive runs can start here: the test's
// fake terminal, or a pseudo-terminal this OS provides.
func (c *Client) terminalSupported() bool {
	return c.NewTerminal != nil || ptySupported()
}

func (c *Client) startTerminal(cmd *exec.Cmd, cols, rows int) (Terminal, error) {
	if c.NewTerminal != nil {
		return c.NewTerminal(cmd, cols, rows)
	}
	return startPTY(cmd, cols, rows)
}
