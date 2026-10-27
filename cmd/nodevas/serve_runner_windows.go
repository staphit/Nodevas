//go:build windows

package main

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// endWithServe has nothing to set before the start on Windows; tieToServe
// does the work once the process exists.
func endWithServe(*exec.Cmd) {}

// tieToServe puts the local runner in a kill-on-close job object whose only
// handle is serve's. A serve that is killed rather than stopped (a launcher's
// Kill, Task Manager, a crash) closes it on the way out, and Windows ends the
// runner and everything it started, instead of leaving a runner with this
// hub's token reconnecting on its own. A failure leaves the runner as it was
// before: stopped by serve's own shutdown only.
func tieToServe(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
	}
	// The job handle is never closed: it lives exactly as long as serve.
}
