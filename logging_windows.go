//go:build windows

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/yuguorong/power_wing/internal/version"
)

const maxLogSize = 5 << 20 // 5 MiB

// setupLogging mirrors the process log to %AppData%\PowerWing\power_wing.log.
// This is essential in GUI-subsystem mode, where double-clicking shows no
// console: the log file is the only place startup/connection errors appear.
func setupLogging() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dir = filepath.Join(dir, "PowerWing")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	path := filepath.Join(dir, "power_wing.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return
	}
	// File first: if the console handle is invalid (double-click), the file
	// write still succeeds and log just ignores the second writer's error.
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.Printf("[main] PowerWing %s starting (pid=%d, log=%s)", version.Version, os.Getpid(), path)
}
