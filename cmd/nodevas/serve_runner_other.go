//go:build !windows && !linux

package main

import "os/exec"

// endWithServe and tieToServe: this OS has no parent-death signal or job
// object, so the local runner ends with serve's own shutdown (Ctrl+C, SIGTERM)
// only; a serve killed with SIGKILL leaves it running.
func endWithServe(*exec.Cmd) {}

func tieToServe(*exec.Cmd) {}
