//go:build !windows && !linux

package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// endWithServe and tieToServe: this OS (macOS, the BSDs) has no parent-death
// signal or job object to set from serve's side. The runner watches instead
// (watchServeParent), so a serve killed with SIGKILL does not leave it running.
func endWithServe(*exec.Cmd) {}

func tieToServe(*exec.Cmd) {}

// watchServeParent ends a runner serve started when serve is gone: its parent
// pid changes when serve exits and the runner is reparented.
func watchServeParent(ctx context.Context, stop context.CancelFunc) {
	stopWhenParentChanges(ctx, stop, os.Getppid, time.Second)
}
