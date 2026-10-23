//go:build !windows

package adapter

import "os/exec"

// wrapBatch is a no-op off Windows: arguments reach the program as they are.
func wrapBatch(*exec.Cmd) error { return nil }
