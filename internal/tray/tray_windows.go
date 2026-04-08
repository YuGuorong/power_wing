//go:build windows

// Package tray provides a Windows system-tray icon for PowerWing.
package tray

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"

	"github.com/getlantern/systray"

	"github.com/yuguorong/power_wing/internal/manager"
)

var (
	once    sync.Once
	mgrRef  *manager.Manager
	portRef int
)

// Run starts the system-tray event loop (blocking).  Should be called in a
// dedicated goroutine.
func Run(mgr *manager.Manager, port int) {
	mgrRef = mgr
	portRef = port
	once.Do(func() {
		systray.Run(onReady, onExit)
	})
}

func onReady() {
	// Provide a minimal icon – replace with a real ICO/PNG if available.
	systray.SetTitle("PowerWing")
	systray.SetTooltip("PowerWing – Power Supply Controller")

	mOpen := systray.AddMenuItem("Open UI", "Open web interface in browser")
	systray.AddSeparator()
	mToggle := systray.AddMenuItem("Toggle Output", "Toggle active power supply output")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit PowerWing")

	// Update tooltip with live status.
	sub := mgrRef.Subscribe()
	go func() {
		for upd := range sub {
			tip := fmt.Sprintf("PowerWing · %s", upd.DeviceName)
			systray.SetTooltip(tip)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", portRef)

	for {
		select {
		case <-mOpen.ClickedCh:
			openBrowser(url)

		case <-mToggle.ClickedCh:
			// Toggle the first connected power supply.
			devs := mgrRef.Devices()
			for _, d := range devs {
				if d.Type == "power_supply" && d.Connected {
					_ = mgrRef.SendCmd(manager.CmdRequest{
						DeviceID: d.ID,
						Command:  "set_outp",
						Params:   map[string]interface{}{"on": true}, // simplified toggle
					})
					break
				}
			}

		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func onExit() {
	log.Println("[tray] exiting")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[tray] open browser: %v", err)
	}
}
