//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// The executable is built as a GUI-subsystem binary (-H windowsgui), so
// double-clicking it never creates a console window.  When it is launched
// from an existing terminal we attach to that terminal's console and rebind
// the standard streams, which keeps CLI output working without opening a new
// shell window.
const attachParentProcess = 0xFFFFFFFF // (DWORD)-1

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// setupWindowsStdio must run before flag parsing and logging.
func setupWindowsStdio() {
	// MCP stdio and shell redirection hand us pipes/files: never touch those.
	if isStdRedirected(windows.STD_OUTPUT_HANDLE) || isStdRedirected(windows.STD_ERROR_HANDLE) {
		return
	}
	// No parent console (double-click / Explorer / service) -> attach fails
	// harmlessly and the process simply runs in the background.
	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	reopenConsoleStdio()
}

func isStdRedirected(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == windows.InvalidHandle {
		return false
	}
	t, err := windows.GetFileType(h)
	if err != nil {
		return false
	}
	return t == windows.FILE_TYPE_PIPE || t == windows.FILE_TYPE_DISK
}

func reopenConsoleStdio() {
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout = f
		os.Stderr = f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
}
