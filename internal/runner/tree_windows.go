//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procTree is an agent and everything it started. On Windows the agent is a
// .cmd shim that starts node, which starts shells and tools: a tree that
// killing the first process does not end. The runner puts the agent in a job
// object, which takes in every process it starts later, and kills the job.
type procTree struct {
	pid int
	job windows.Handle
}

// prepareTree gives the agent its own console process group, so a Ctrl+C at
// the runner's terminal does not reach it directly; the runner decides what
// happens to its runs.
func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// attachTree is called right after Start. The process is assigned to the job
// before it has had time to start node, so its children are in the job too.
// Kill-on-close means a runner that dies takes its agents with it.
func attachTree(cmd *exec.Cmd) *procTree { return attachPID(cmd.Process.Pid) }

// attachPID is attachTree for a process started some other way (in a
// pseudo-terminal).
func attachPID(pid int) *procTree {
	t := &procTree{pid: pid}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return t
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return t
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.pid))
	if err != nil {
		windows.CloseHandle(job)
		return t
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return t
	}
	t.job = job
	return t
}

// kill ends the whole tree: taskkill /T first, which walks the tree while its
// root is alive, then the job, which also holds whatever a dead parent left.
func (t *procTree) kill() {
	if t == nil {
		return
	}
	if t.pid > 0 {
		cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(t.pid))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Run()
	}
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

// close releases the job; with kill-on-close it also ends anything the agent
// left running in the background.
func (t *procTree) close() {
	if t != nil && t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
