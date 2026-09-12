//go:build windows

package rag

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureWorkerCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// Windows venv executables launch a child interpreter. Killing only the
// launcher leaves an in-flight model consuming CPU and holding our pipes open.
func terminateWorkerProcess(cmd *exec.Cmd) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kill := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = kill.Run()
	_ = cmd.Process.Kill()
}
