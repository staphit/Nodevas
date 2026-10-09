//go:build !windows

package rag

import "os/exec"

func configureWorkerCommand(cmd *exec.Cmd) {}

func terminateWorkerProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
