//go:build !windows

// Package tray is a no-op stub on non-Windows platforms.
package tray

import "github.com/yuguorong/power_wing/internal/manager"

// Run is a no-op on Linux/macOS.
func Run(_ *manager.Manager, _ int) {}
