//go:build linux

package main

import (
	"context"
	"os/exec"
	"syscall"
)

// endWithServe has the kernel send the local runner SIGTERM when serve exits,
// killed or not, so no runner with this hub's token outlives it.
func endWithServe(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}

func tieToServe(*exec.Cmd) {}

// watchServeParent: Pdeathsig (endWithServe) already ends the runner with serve.
func watchServeParent(context.Context, context.CancelFunc) {}
