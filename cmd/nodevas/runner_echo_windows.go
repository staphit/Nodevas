//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// echoOff stops f, a console, from echoing what is typed; restore puts it
// back.
func echoOff(f *os.File) (restore func(), err error) {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	quiet := (mode &^ windows.ENABLE_ECHO_INPUT) | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT
	if err := windows.SetConsoleMode(handle, quiet); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }, nil
}
