//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// echoOff stops f, a terminal, from echoing what is typed; restore puts it
// back.
func echoOff(f *os.File) (restore func(), err error) {
	fd := int(f.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	quiet := *state
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &quiet); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, state) }, nil
}
