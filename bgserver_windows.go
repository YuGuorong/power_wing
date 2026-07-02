//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func spawnBackground() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("cannot find executable: %v", err)
	}

	// Build args: copy all flags except --server, and add --no-browser.
	var args []string
	for _, a := range os.Args[1:] {
		if a == "--server" || a == "-server" {
			continue
		}
		if strings.HasPrefix(a, "--server=") || strings.HasPrefix(a, "-server=") {
			continue
		}
		args = append(args, a)
	}
	args = append(args, "--no-browser")

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		log.Fatalf("failed to start background server: %v", err)
	}
	// Release the process handle so the child survives the parent exiting.
	_ = cmd.Process.Release()
	fmt.Printf("PowerWing server started in background (PID: %d)\n", cmd.Process.Pid)
}
