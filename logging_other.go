//go:build !windows

package main

// setupLogging keeps the normal stderr logging on non-Windows platforms.
func setupLogging() {}
