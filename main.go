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
	"net"
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
	// Keep CLI output when launched from a terminal, and stay windowless when
	// double-clicked (the binary is built with -H windowsgui).
	setupWindowsStdio()
	setupLogging()

	// ── CLI flags ────────────────────────────────────────────────────────────
	fHost := flag.String("host", "0.0.0.0", "HTTP server bind host")
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
	fUSBPort := flag.Int("usb-port", -1, "USB hub port number (1-4, or 1-12 for SanGenXian)")
	fUSBState := flag.String("usb-state", "", "USB port state: on|off")

	// Info flags
	fListPorts := flag.Bool("list-ports", false, "List available serial ports and exit")
	fServer := flag.Bool("server", false, "Run as background server (spawn detached process, then CLI exits)")
	fAutoConnect := flag.Bool("auto-connect", false, "Open all configured serial ports at startup (disabled by default for safety)")
	fListDevices := flag.Bool("list-devices", false, "List configured devices and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: powerwing [options]\n\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nMCP is opt-in. To run MCP together with the Web UI and tray, use:\n")
		fmt.Fprintf(os.Stderr, "  powerwing --mcp=true --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876\n")
		fmt.Fprintf(os.Stderr, "Do NOT use --mcp-only if you also want the Web UI and tray.\n")
	}
	flag.Parse()

	// ── Background server mode ────────────────────────────────────────────────
	if *fServer {
		spawnBackground()
		return
	}

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
	// Default to lazy start in every mode: configured devices are registered
	// but no serial port is opened until the user explicitly connects a device
	// (web UI / HTTP) or sends a command to it.  This prevents stale COM
	// assignments from grabbing ports that belong to other equipment.
	// --auto-connect restores the old "open everything at startup" behaviour.
	if *fMCPOnly || *fMCP || !*fAutoConnect {
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

	url := fmt.Sprintf("http://%s", canonicalAddr(*fHost, *fPort))
	if !*fNoBrowser {
		openBrowser(url)
	} else {
		log.Println("PowerWing running at", url)
	}

	// System tray (Windows: real; others: no-op).  The callback lets the tray
	// Quit item stop the background process — there is no console to Ctrl+C
	// when the app was launched by double-click.
	trayQuit := make(chan struct{}, 1)
	go tray.Run(mgr, *fHost, *fPort, appIcon, func() {
		select {
		case trayQuit <- struct{}{}:
		default:
		}
	})

	// Voice commands
	if *fVoice {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go voice.Listen(ctx, mgr)
	}

	// Wait for OS signal or the tray Quit item.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		log.Println("Shutting down PowerWing…")
	case <-trayQuit:
		log.Println("Tray quit requested; shutting down…")
	}
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
		// rundll32 is a GUI process: no console window flashes when the
		// browser is launched from the tray or on startup.
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open browser: %v (navigate manually to %s)", err, url)
	}
}

// canonicalAddr returns a browser-reachable "host:port" for the given bind
// host.  Wildcard bind addresses ("0.0.0.0", "::", "*") are not valid browser
// destinations on Windows, so they are rewritten to loopback while the server
// keeps listening on the wildcard.
func canonicalAddr(host string, port int) string {
	if host == "0.0.0.0" || host == "::" || host == "*" || host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
