//go:build !windows

package main

// disableQuickEdit: only a Windows console pauses its writers while text is
// selected.
func disableQuickEdit() (restore func()) { return func() {} }
