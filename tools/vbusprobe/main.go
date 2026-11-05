// Command vbusprobe is a small hardware bring-up tool for the ftdi_vbus
// driver. It exercises the same code path the manager uses: enumerate D2XX,
// connect, read the latched CBUS level, optionally drive it, then print state.
//
// Usage:
//
//	go run ./tools/vbusprobe -serial DKYGR02 -pin 0
//	go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -set on
//	go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -set off -active-low=false
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/device/ftdivbus"
	"github.com/yuguorong/power_wing/internal/ftd2xx"
	"github.com/yuguorong/power_wing/internal/manager"
	"github.com/yuguorong/power_wing/internal/serialport"
)

func main() {
	serial := flag.String("serial", "", "FTDI board serial (empty = first FT-X board)")
	pin := flag.Int("pin", 0, "CBUS pin driving VBUS (0-3)")
	activeLow := flag.Bool("active-low", true, "true = pin low turns VBUS on")
	set := flag.String("set", "", "optional: on | off")
	viaManager := flag.Bool("manager", false, "drive the device through manager + set_vbus instead of the driver directly")
	checkPort := flag.String("check-port", "", "after the command, try to open this VCP port (e.g. COM43) from the same process to prove the D2XX handle was released")
	hold := flag.Duration("hold", 0, "keep the manager alive this long before the release check (lets the poll loop run)")
	flag.Parse()

	boards, err := ftdivbus.Boards()
	if err != nil {
		fmt.Fprintf(os.Stderr, "enumerate: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("D2XX boards: %d\n", len(boards))
	for _, b := range boards {
		fmt.Printf("  %-14s serial=%s %q\n", typeName(b.Type), b.Serial, b.Description)
	}

	if *viaManager {
		runViaManager(*serial, *pin, *activeLow, *set, *checkPort, *hold)
		return
	}

	d := ftdivbus.New("probe", "vbusprobe", *serial, *pin, *activeLow)
	if err := d.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer d.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect only checks D2XX availability; the board is NOT opened.  ReadState
	// performs one transient open/read/close.
	st, err := d.ReadState(ctx, *serial)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read_state: %v\n", err)
	}
	printState("after transient read", st)
	checkReleased(*checkPort)

	switch *set {
	case "":
		// read-only
	case "on", "off":
		on := *set == "on"
		if err := d.SetVBus(ctx, *serial, on); err != nil {
			fmt.Fprintf(os.Stderr, "set_vbus(%v): %v\n", on, err)
			os.Exit(1)
		}
		st, _ = d.GetState(ctx)
		printState("after set_vbus "+*set, st)
	default:
		fmt.Fprintf(os.Stderr, "-set must be on or off\n")
		os.Exit(2)
	}

	checkReleased(*checkPort)
}

// checkReleased proves the D2XX handle was released: opening the board's VCP
// port from the same process fails while a D2XX handle is still held.  The
// port is opened and closed immediately.
func checkReleased(port string) {
	if port == "" {
		return
	}
	p, err := serialport.OpenFor("vbusprobe-check", port, serialport.Mode(9600))
	if err != nil {
		fmt.Fprintf(os.Stderr, "RELEASE CHECK FAILED: %s still occupied: %v\n", port, err)
		os.Exit(1)
	}
	_ = p.Close()
	fmt.Printf("RELEASE CHECK OK: %s opened and closed immediately (no D2XX handle retained)\n", port)
}

func printState(label string, st interface{}) {
	fmt.Printf("%s: %+v\n", label, st)
}

// runViaManager exercises the production path: config -> makeDevice ->
// lazy connect -> set_vbus -> pollDevice -> LatestState.  It uses an in-memory
// config, so the real %AppData%\PowerWing\config.json is never touched.
func runViaManager(serial string, pin int, activeLow bool, set, checkPort string, hold time.Duration) {
	cfg := &config.Config{Devices: []config.DeviceConfig{{
		ID: "vbus1", Name: "VBUS probe", Type: "ftdi_vbus",
		Serial: serial, Pin: pin, ActiveLow: &activeLow, Enabled: true,
	}}}
	mgr := manager.New(cfg)
	if err := mgr.LazyStart(); err != nil {
		fmt.Fprintf(os.Stderr, "manager start: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Stop()

	for _, d := range mgr.Devices() {
		fmt.Printf("registered: id=%s type=%s serial=%s pin=%d\n", d.ID, d.Type, d.Serial, d.Pin)
	}

	if set != "on" && set != "off" {
		fmt.Fprintln(os.Stderr, "-manager requires -set on|off")
		os.Exit(2)
	}
	if st, err := mgr.ReadVBusState("vbus1", serial); err != nil {
		fmt.Fprintf(os.Stderr, "read via manager: %v\n", err)
	} else {
		fmt.Printf("transient read via manager: %+v\n", st)
	}
	checkReleased(checkPort)

	on := set == "on"
	err := mgr.SendCmd(manager.CmdRequest{
		DeviceID: "vbus1",
		Command:  "set_vbus",
		Params:   map[string]interface{}{"on": on, "serial": serial},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "set_vbus(%v) via manager: %v\n", on, err)
		os.Exit(1)
	}
	// set_vbus schedules pollDevice in a goroutine; give it a moment.
	time.Sleep(300 * time.Millisecond)

	st, ok := mgr.LatestState("vbus1")
	fmt.Printf("manager state (ok=%v): %+v\n", ok, st)
	for _, d := range mgr.Devices() {
		fmt.Printf("connected=%v id=%s\n", d.Connected, d.ID)
	}
	// Let the 1s poll/reconnect loops run while the manager stays alive.
	if hold > 0 {
		time.Sleep(hold)
		fmt.Printf("held manager alive for %s (poll loop ran); checking %s\n", hold, checkPort)
	}
	// Manager is still running and the driver still reports connected; if the
	// D2XX handle were held, this VCP open would fail.
	checkReleased(checkPort)
}

func typeName(t uint32) string {
	return ftd2xx.DeviceTypeName(t)
}
