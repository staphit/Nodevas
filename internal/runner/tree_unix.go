//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// procTree is an agent and everything it started. On Unix that is its process
// group: the agent leads a new one, and its children (node, shells, test
// runners) stay in it unless they deliberately leave.
type procTree struct{ pgid int }

// prepareTree makes the command lead its own process group. It also keeps a
// Ctrl+C at the runner's terminal from reaching the agent directly; the runner
// decides what happens to its runs.
func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// attachTree is called right after Start.
func attachTree(cmd *exec.Cmd) *procTree { return &procTree{pgid: cmd.Process.Pid} }

// kill ends the whole group. Killing only the leader would leave its children
// running, holding the worktree and the agent's stdout open.
func (t *procTree) kill() {
	if t == nil || t.pgid <= 0 {
		return
	}
	_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
}

// close runs after the agent exited: whatever it left running in the
// background goes too, as it does on Windows when the job closes.
func (t *procTree) close() { t.kill() }
