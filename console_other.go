//go:build !windows

package main

// setupWindowsStdio is a no-op outside Windows.
func setupWindowsStdio() {}
