// PowerWing – power supply & USB hub controller
//
// Usage (GUI mode, default):
//
//	powerwing [--port 8765] [--no-browser] [--voice]
//
// Usage (CLI mode – executes command then exits):
//
//	powerwing --device <id> --volt <v>
//	powerwing --device <id> --curr <a>
//	powerwing --device <id> --outp on|off
//	powerwing --device <id> --ovp <v>
//	powerwing --device <id> --ocp <a>
//	powerwing --device <id> --usb-port <1-4> --usb-state on|off
//	powerwing --list-ports
//	powerwing --list-devices
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"go.bug.st/serial"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/manager"
	"github.com/yuguorong/power_wing/internal/server"
	"github.com/yuguorong/power_wing/internal/tray"
	"github.com/yuguorong/power_wing/internal/voice"
)

//go:embed web
var rawWebFS embed.FS

func main() {
	// ── CLI flags ────────────────────────────────────────────────────────────
	fPort := flag.Int("port", 8765, "HTTP server port")
	fNoBrowser := flag.Bool("no-browser", false, "Do not open browser on start")
	fVoice := flag.Bool("voice", false, "Enable voice command (Windows only)")

	// Device command flags
	fDevice := flag.String("device", "", "Device ID for CLI control")
	fVolt := flag.Float64("volt", -1, "Set output voltage (V)")
	fCurr := flag.Float64("curr", -1, "Set output current (A)")
	fOutp := flag.String("outp", "", "Set output ON or OFF")
	fOVP := flag.Float64("ovp", -1, "Set OVP limit (V)")
	fOCP := flag.Float64("ocp", -1, "Set OCP limit (A)")
	fUSBPort := flag.Int("usb-port", -1, "USB hub port number (1-4)")
	fUSBState := flag.String("usb-state", "", "USB port state: on|off")

	// Info flags
	fListPorts := flag.Bool("list-ports", false, "List available serial ports and exit")
	fListDevices := flag.Bool("list-devices", false, "List configured devices and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: powerwing [options]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// ── Load config ──────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: config load failed (%v); using defaults", err)
		cfg = config.Default()
	}

	// ── Info commands (no manager needed) ────────────────────────────────────
	if *fListPorts {
		listPorts()
		return
	}
	if *fListDevices {
		listDevices(cfg)
		return
	}

	// ── Initialise manager ───────────────────────────────────────────────────
	mgr := manager.New(cfg)
	if err := mgr.Start(); err != nil {
		log.Fatalf("manager start: %v", err)
	}

	// ── CLI device control mode ───────────────────────────────────────────────
	if *fDevice != "" {
		err := cliControl(mgr, *fDevice, *fVolt, *fCurr, *fOutp, *fOVP, *fOCP, *fUSBPort, *fUSBState)
		mgr.Stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	}

	// ── GUI / server mode ─────────────────────────────────────────────────────
	webFS, err := fs.Sub(rawWebFS, "web")
	if err != nil {
		log.Fatalf("web assets: %v", err)
	}

	srv := server.New(mgr, *fPort, webFS)
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", *fPort)
	if !*fNoBrowser {
		openBrowser(url)
	} else {
		fmt.Println("PowerWing running at", url)
	}

	// System tray (Windows: real; others: no-op)
	go tray.Run(mgr, *fPort)

	// Voice commands
	if *fVoice {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go voice.Listen(ctx, mgr)
	}

	// Wait for OS signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down PowerWing…")
	mgr.Stop()
}

// ─── CLI helpers ──────────────────────────────────────────────────────────────

func cliControl(mgr *manager.Manager, id string, volt, curr float64, outp string,
	ovp, ocp float64, usbPort int, usbState string) error {

	send := func(cmd string, params map[string]interface{}) error {
		return mgr.SendCmd(manager.CmdRequest{DeviceID: id, Command: cmd, Params: params})
	}

	if volt >= 0 {
		if err := send("set_volt", map[string]interface{}{"value": volt}); err != nil {
			return err
		}
		fmt.Printf("Voltage set to %.3fV\n", volt)
	}
	if curr >= 0 {
		if err := send("set_curr", map[string]interface{}{"value": curr}); err != nil {
			return err
		}
		fmt.Printf("Current set to %.3fA\n", curr)
	}
	if outp != "" {
		on := strings.EqualFold(outp, "on")
		if err := send("set_outp", map[string]interface{}{"on": on}); err != nil {
			return err
		}
		fmt.Printf("Output %s\n", strings.ToUpper(outp))
	}
	if ovp >= 0 {
		if err := send("set_ovp", map[string]interface{}{"value": ovp}); err != nil {
			return err
		}
		fmt.Printf("OVP limit set to %.3fV\n", ovp)
	}
	if ocp >= 0 {
		if err := send("set_ocp", map[string]interface{}{"value": ocp}); err != nil {
			return err
		}
		fmt.Printf("OCP limit set to %.3fA\n", ocp)
	}
	if usbPort >= 1 && usbState != "" {
		on := strings.EqualFold(usbState, "on")
		if err := send("set_usb_port", map[string]interface{}{"port": usbPort, "on": on}); err != nil {
			return err
		}
		fmt.Printf("USB port %d set to %s\n", usbPort, strings.ToUpper(usbState))
	}
	return nil
}

func listPorts() {
	ports, err := serial.GetPortsList()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return
	}
	if len(ports) == 0 {
		fmt.Println("No serial ports found")
		return
	}
	fmt.Println("Available serial ports:")
	for _, p := range ports {
		fmt.Println(" ", p)
	}
}

func listDevices(cfg *config.Config) {
	if len(cfg.Devices) == 0 {
		fmt.Println("No devices configured. Use the UI or --help to set them up.")
		return
	}
	fmt.Println("Configured devices:")
	for _, d := range cfg.Devices {
		enabled := "disabled"
		if d.Enabled {
			enabled = "enabled"
		}
		fmt.Printf("  %-20s  type=%-12s  port=%-12s  baud=%s  [%s]\n",
			d.ID, d.Type, d.Port, strconv.Itoa(d.Baud), enabled)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open browser: %v (navigate manually to %s)", err, url)
	}
}
