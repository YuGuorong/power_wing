//go:build windows

// Package tray provides a Windows system-tray icon for PowerWing.
package tray

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"sync"

	"github.com/getlantern/systray"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/manager"
)

func isPSUType(t string) bool {
	return t == "spm3051" || t == "pd_pocket" || t == "power_supply"
}

var (
	once    sync.Once
	mgrRef  *manager.Manager
	hostRef string
	portRef int
	quitFn  func()
)

var iconData []byte

// Run starts the system-tray event loop (blocking).  Should be called in a
// dedicated goroutine.  onQuit is invoked after the Quit menu item is chosen
// so the process can shut down (there is no console to Ctrl+C).
func Run(mgr *manager.Manager, host string, port int, icon []byte, onQuit func()) {
	mgrRef = mgr
	hostRef = host
	portRef = port
	iconData = icon
	quitFn = onQuit
	once.Do(func() {
		systray.Run(onReady, onExit)
	})
}

func onReady() {
	if len(iconData) > 0 {
		systray.SetIcon(iconData)
	}
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

	url := fmt.Sprintf("http://%s", canonicalAddr(hostRef, portRef))

	for {
		select {
		case <-mOpen.ClickedCh:
			openBrowser(url)

		case <-mToggle.ClickedCh:
			// Toggle the first connected power supply.
			devs := mgrRef.Devices()
			for _, d := range devs {
				if !d.Connected || !isPSUType(d.Type) {
					continue
				}
				on := true
				if st, ok := mgrRef.LatestState(d.ID); ok {
					if ps, ok := st.(*device.PowerState); ok {
						on = !ps.OutputOn
					}
				}
				_ = mgrRef.SendCmd(manager.CmdRequest{
					DeviceID: d.ID,
					Command:  "set_outp",
					Params:   map[string]interface{}{"on": on},
				})
				break
			}

		case <-mQuit.ClickedCh:
			systray.Quit()
			if quitFn != nil {
				quitFn()
			}
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
		// GUI process: unlike `cmd /c start`, this never flashes a console.
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[tray] open browser: %v", err)
	}
}

// canonicalAddr rewrites wildcard bind hosts ("0.0.0.0", "::") to loopback so
// the tray's "Open UI" opens a browser-reachable URL.  The server itself keeps
// listening on the wildcard address.
func canonicalAddr(host string, port int) string {
	if host == "0.0.0.0" || host == "::" || host == "*" || host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
