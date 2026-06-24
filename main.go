// PowerWing – power supply & USB hub controller
//
// Usage (GUI mode, default):
//
//	powerwing [--host 0.0.0.0] [--port 8765] [--no-browser] [--voice]
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
//go:generate go run ./tools/genicon/main.go
// On Windows, also run: goversioninfo -o rsrc_windows.syso

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

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/manager"
	"github.com/yuguorong/power_wing/internal/mcp"
	"github.com/yuguorong/power_wing/internal/serialport"
	"github.com/yuguorong/power_wing/internal/server"
	"github.com/yuguorong/power_wing/internal/tray"
	"github.com/yuguorong/power_wing/internal/voice"
)

//go:embed web
var rawWebFS embed.FS

func main() {
	// ── CLI flags ────────────────────────────────────────────────────────────
	fHost := flag.String("host", "127.0.0.1", "HTTP server bind host; use 0.0.0.0 for network access")
	fPort := flag.Int("port", 8765, "HTTP server port")
	fNoBrowser := flag.Bool("no-browser", false, "Do not open browser on start")
	fVoice := flag.Bool("voice", false, "Enable voice command (Windows only)")
	fMCP := flag.Bool("mcp", false, "Enable MCP server for AI tool calling")
	fMCPOnly := flag.Bool("mcp-only", false, "Run only MCP server (no web UI)")
	fMCPTransport := flag.String("mcp-transport", "stdio", "MCP transport: stdio or tcp")
	fMCPHost := flag.String("mcp-host", "0.0.0.0", "MCP TCP bind host; use 0.0.0.0 for network access")
	fMCPPort := flag.Int("mcp-port", 9876, "MCP TCP server port")

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
	if *fMCPOnly || *fMCP {
		// MCP mode: lazy start — register devices but don't auto-connect.
		// Devices connect on-demand when AI calls send_command / reconnect_device.
		if err := mgr.LazyStart(); err != nil {
			log.Fatalf("manager lazy start: %v", err)
		}
	} else {
		if err := mgr.Start(); err != nil {
			log.Fatalf("manager start: %v", err)
		}
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

	if *fMCPOnly || *fMCP {
		mcpCtx, mcpCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

		switch *fMCPTransport {
		case "tcp":
			go func() {
				if err := mcp.ServeTCP(mcpCtx, mgr, *fMCPHost, *fMCPPort); err != nil {
					log.Printf("[mcp] TCP server stopped: %v", err)
				}
			}()
			if *fMCPOnly {
				// In mcp-only TCP mode, wait for signal to exit.
				<-mcpCtx.Done()
				mcpCancel()
				mgr.Stop()
				return
			}
		case "stdio":
			if *fMCPOnly {
				log.Println("[mcp] stdio server started (mcp-only mode)")
				if err := mcp.New(mgr, os.Stdin, os.Stdout).Serve(mcpCtx); err != nil {
					log.Printf("[mcp] stopped with error: %v", err)
				}
				mcpCancel()
				mgr.Stop()
				return
			}
			// stdio alongside web UI
			go func() {
				log.Println("[mcp] stdio server started")
				if err := mcp.New(mgr, os.Stdin, os.Stdout).Serve(mcpCtx); err != nil {
					log.Printf("[mcp] stopped with error: %v", err)
				}
			}()
		default:
			log.Fatalf("unknown mcp-transport %q; use stdio or tcp", *fMCPTransport)
		}

		// Don't cancel MCP context here; it stays alive until process shutdown.
	}

	// ── GUI / server mode ─────────────────────────────────────────────────────
	webFS, err := fs.Sub(rawWebFS, "web")
	if err != nil {
		log.Fatalf("web assets: %v", err)
	}

	srv := server.New(mgr, *fHost, *fPort, webFS)
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	url := fmt.Sprintf("http://%s:%d", *fHost, *fPort)
	if !*fNoBrowser {
		openBrowser(url)
	} else {
		log.Println("PowerWing running at", url)
	}

	// System tray (Windows: real; others: no-op)
	go tray.Run(mgr, *fHost, *fPort, appIcon)

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
	ports, err := serialport.List()
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
