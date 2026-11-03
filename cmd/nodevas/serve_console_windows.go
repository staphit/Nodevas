//go:build windows

package main

import "golang.org/x/sys/windows"

// disableQuickEdit turns off the console's QuickEdit mode while serve runs and
// returns what puts it back. With QuickEdit on, one click in the window starts
// a selection, and while a selection is open Windows pauses every write to the
// console: the runner's prompt stops mid-question and, before the console log
// became non-blocking, every request behind a log line waited too. Nothing
// happens when stdin is not a console.
func disableQuickEdit() (restore func()) {
	handle, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return func() {}
	}
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil || mode&windows.ENABLE_QUICK_EDIT_MODE == 0 {
		return func() {}
	}
	// ENABLE_EXTENDED_FLAGS is what makes the QuickEdit bit count.
	if err := windows.SetConsoleMode(handle, (mode&^windows.ENABLE_QUICK_EDIT_MODE)|windows.ENABLE_EXTENDED_FLAGS); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(handle, mode|windows.ENABLE_EXTENDED_FLAGS) }
}
