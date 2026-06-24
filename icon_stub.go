//go:build !windows

package main

// appIcon is only used by the Windows tray implementation.  Non-Windows builds
// use a tray stub, so an empty icon is sufficient.
var appIcon []byte
