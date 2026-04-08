//go:build !windows

// Package voice is a no-op stub on non-Windows platforms.
package voice

import (
	"context"
	"log"

	"github.com/yuguorong/power_wing/internal/manager"
)

// Listen is a no-op on Linux/macOS.
func Listen(_ context.Context, _ *manager.Manager) {
	log.Println("[voice] voice commands are only supported on Windows")
}
