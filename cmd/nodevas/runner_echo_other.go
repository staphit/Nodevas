//go:build !linux && !windows && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import (
	"errors"
	"os"
)

// echoOff is unsupported here: the prompt still works, it just echoes.
func echoOff(*os.File) (restore func(), err error) {
	return nil, errors.New("echo cannot be turned off on this system")
}
