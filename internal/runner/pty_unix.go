//go:build !windows

package runner

import (
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

var ptyCheck = sync.OnceValue(func() bool {
	// A container without /dev/ptmx, or an OS go-pty does not cover.
	p, err := gopty.New()
	if err != nil {
		return false
	}
	_ = p.Close()
	return true
})

// ptySupported reports whether this machine can open a pseudo-terminal.
func ptySupported() bool { return ptyCheck() }
