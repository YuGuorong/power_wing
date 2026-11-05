// Package ftdivbus implements device.VBusController for an FTDI FT230XQ board
// whose CBUS pin gates the DUT's VBUS.
//
// Why D2XX: the FT230X exposes its CBUS pins only through FTDI's D2XX driver
// (FT_SetBitMode mode 0x20, "CBUS bit-bang").  A VCP/COM port cannot drive
// them.  The CBUS pins must first be programmed as "GPIO" in the device EEPROM
// with FT_PROG; otherwise FT_SetBitMode rejects the request.
//
// The level is latched by the device: FT_Close does not reset it, so the state
// persists until another command changes it or the board re-enumerates (unplug,
// driver reload), which returns the pin to its EEPROM default.
//
// This driver has **no connection state and holds no handle**.  Every read and
// every switch is a one-shot open/act/close sequence: the board's VCP port is
// free again as soon as the call returns, and the operation never waits for a
// port held by another program and never retries.
package ftdivbus

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/ftd2xx"
)

const (
	// TypeName is the configuration type string for this driver.
	TypeName = "ftdi_vbus"

	// PinCount is the number of CBUS pins the FT230X exposes to bit-bang mode.
	PinCount = 4

	// ftdiSerialMax is the longest serial D2XX reports; a longer one carries
	// the Windows FTDIBUS interface-letter suffix that FT_OpenEx does not
	// accept.
	ftdiSerialMax = 8
)

// Board is one attached FTDI device that D2XX can currently name.
type Board struct {
	Serial      string `json:"serial"`
	Description string `json:"description"`
	Type        uint32 `json:"type"`
}

// Boards enumerates the FTDI boards D2XX can currently name.  A board whose
// VCP port is held by another program may appear as a placeholder with a
// blank serial and is therefore skipped.
//
// D2XX enumeration is serialised against every open/act/close: running
// FT_GetDeviceInfoList concurrently with FT_OpenEx can return a zero handle on
// this driver, which would leave the device held.
func Boards() ([]Board, error) {
	hwMu.Lock()
	defer hwMu.Unlock()
	return boardsLocked()
}

// boardsLocked is Boards without taking hwMu; callers must already hold it.
func boardsLocked() ([]Board, error) {
	infos, err := ftd2xx.List()
	if err != nil {
		return nil, err
	}
	out := make([]Board, 0, len(infos))
	for _, i := range infos {
		serial := strings.TrimSpace(i.Serial)
		if serial == "" {
			continue
		}
		out = append(out, Board{Serial: serial, Description: i.Description, Type: i.Type})
	}
	return out, nil
}

// normalizeSerial canonicalises a user-supplied serial for FT_OpenEx:
// trimmed, upper-cased, and with the Windows FTDIBUS interface-letter suffix
// removed (an FT230X with serial DK0MAQPC enumerates as DK0MAQPCA).
func normalizeSerial(serial string) string {
	s := strings.ToUpper(strings.TrimSpace(serial))
	if len(s) > ftdiSerialMax {
		s = s[:len(s)-1]
	}
	return s
}

// cbusMask builds a bit-bang mask for one pin: the upper nibble is direction
// (1 = output) and the lower nibble the output level, bit N mapping to CBUSN.
// Only the target pin's bits are set, so the other CBUS pins stay tri-stated.
func cbusMask(pin int, output, high bool) byte {
	var m byte
	if output {
		m |= 0x10 << uint(pin)
	}
	if high {
		m |= 0x01 << uint(pin)
	}
	return m
}

// levelFor maps a desired VBUS state to the CBUS logic level that produces it.
func levelFor(activeLow, vbusOn bool) bool { return vbusOn != activeLow }

// vbusFromLevel maps an observed CBUS level back to a VBUS state.
func vbusFromLevel(activeLow, high bool) bool { return high != activeLow }

// hwMu serialises every D2XX open/act/close across all controller instances.
// D2XX allows only one handle per device; two configured controllers must not
// race for the same board.
var hwMu sync.Mutex

// FTDI drives one FT230XQ board's CBUS pin over D2XX.
type FTDI struct {
	id        string
	name      string
	serial    string // configured default serial; empty = auto-select
	pin       int
	activeLow bool

	mu        sync.Mutex
	connected bool   // driver available (D2XX loaded); NOT a held handle
	selected  string // last board selected via ReadState/SetVBus
	vbusOn    bool
	known     bool
	lastErr   string
}

// New creates a new FTDI VBUS driver.  serial is the default board; pin must be
// 0-3; activeLow means pin low turns VBUS on.
func New(id, name, serial string, pin int, activeLow bool) *FTDI {
	return &FTDI{
		id:        id,
		name:      name,
		serial:    normalizeSerial(serial),
		pin:       pin,
		activeLow: activeLow,
	}
}

func (d *FTDI) ID() string   { return d.id }
func (d *FTDI) Name() string { return d.name }
func (d *FTDI) Type() string { return TypeName }

func (d *FTDI) IsConnected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}

// Connect only verifies that D2XX is usable.  It does not open a board and
// holds no handle: reads and switches are performed on demand by ReadState and
// SetVBus, each as a single open/act/close.
func (d *FTDI) Connect() error {
	hwMu.Lock()
	defer hwMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.connected {
		return nil
	}
	if _, err := ftd2xx.List(); err != nil {
		d.lastErr = err.Error()
		return fmt.Errorf("ftdivbus %s: %w", d.id, err)
	}
	d.connected = true
	return nil
}

// Disconnect clears the "driver available" flag.  Nothing is closed because no
// handle is ever held.
func (d *FTDI) Disconnect() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connected = false
	return nil
}

// GetState returns the cached state.  It never touches the hardware.
func (d *FTDI) GetState(_ context.Context) (*device.VBusState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stateLocked(), nil
}

// ReadState temporarily opens the selected board (or the one named in serial),
// reads the currently latched CBUS level, and closes the handle before
// returning.  It is the on-demand replacement for a polling connection.
func (d *FTDI) ReadState(ctx context.Context, serial string) (*device.VBusState, error) {
	hwMu.Lock()
	defer hwMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()

	target, err := d.resolveTarget(serial)
	if err != nil {
		d.lastErr = err.Error()
		return d.stateLocked(), err
	}
	d.selected = target

	dev, err := d.open(ctx, target)
	if err != nil {
		d.lastErr = err.Error()
		return d.stateLocked(), fmt.Errorf("ftdivbus %s: open %s: %w", d.id, target, err)
	}
	defer func() { _ = dev.Close() }()

	got, err := dev.BitMode()
	if err != nil {
		d.lastErr = err.Error()
		return d.stateLocked(), fmt.Errorf("ftdivbus %s: reading CBUS%d on %s: %w", d.id, d.pin, target, err)
	}

	high := got&(1<<uint(d.pin)) != 0
	d.vbusOn = vbusFromLevel(d.activeLow, high)
	d.known = true
	d.lastErr = ""
	return d.stateLocked(), nil
}

// SetVBus drives the CBUS pin of the selected board (or the one named in
// serial) and verifies it followed.  The open/act/close sequence is serialised
// so concurrent toggles cannot race.
func (d *FTDI) SetVBus(ctx context.Context, serial string, on bool) error {
	hwMu.Lock()
	defer hwMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()

	target, err := d.resolveTarget(serial)
	if err != nil {
		d.lastErr = err.Error()
		return err
	}
	d.selected = target

	dev, err := d.open(ctx, target)
	if err != nil {
		d.lastErr = err.Error()
		return fmt.Errorf("ftdivbus %s: open %s: %w", d.id, target, err)
	}
	defer func() { _ = dev.Close() }()

	high := levelFor(d.activeLow, on)
	mask := cbusMask(d.pin, true, high)
	if err := dev.SetBitMode(mask, ftd2xx.BitModeCBUSBitBang); err != nil {
		wrapped := fmt.Errorf("ftdivbus %s: driving CBUS%d on %s failed (are the CBUS pins programmed as GPIO in the EEPROM with FT_PROG?): %w",
			d.id, d.pin, target, err)
		d.lastErr = wrapped.Error()
		return wrapped
	}

	// Read back and verify, so a silent wiring failure is caught here rather
	// than in the test run.
	if got, err := dev.BitMode(); err == nil {
		want := byte(0)
		if high {
			want = 1 << uint(d.pin)
		}
		if got&(1<<uint(d.pin)) != want {
			wrapped := fmt.Errorf("ftdivbus %s: CBUS%d did not follow: wrote 0x%02X, read 0x%02X", d.id, d.pin, mask, got)
			d.lastErr = wrapped.Error()
			return wrapped
		}
	}

	d.vbusOn = on
	d.known = true
	d.lastErr = ""
	return nil
}

// resolveTarget turns a requested serial into the board to use.  An explicit
// serial wins (it is opened as given, so a stale selection fails fast); then
// the last selection, then the configured default, then the first FT-X board.
// Must be called with d.mu held.
func (d *FTDI) resolveTarget(serial string) (string, error) {
	if s := normalizeSerial(serial); s != "" {
		return s, nil
	}
	if d.selected != "" {
		return d.selected, nil
	}
	if d.serial != "" {
		return d.serial, nil
	}
	boards, err := boardsLocked()
	if err != nil {
		return "", err
	}
	for _, b := range boards {
		if b.Type == ftd2xx.DeviceTypeXSeries {
			return b.Serial, nil
		}
	}
	if len(boards) == 1 {
		return boards[0].Serial, nil
	}
	if len(boards) == 0 {
		return "", fmt.Errorf("ftdivbus %s: no FTDI board detected; connect the board and refresh", d.id)
	}
	return "", fmt.Errorf("ftdivbus %s: several FTDI boards attached; select one", d.id)
}

// stateLocked builds the cached state.  Must be called with d.mu held.
func (d *FTDI) stateLocked() *device.VBusState {
	serial := d.selected
	if serial == "" {
		serial = d.serial
	}
	return &device.VBusState{
		Connected: d.connected,
		VBusOn:    d.vbusOn,
		Known:     d.known,
		Serial:    serial,
		Pin:       d.pin,
		ActiveLow: d.activeLow,
		LastError: d.lastErr,
	}
}

// open makes exactly one attempt to open the board.  It never waits for a port
// held by another program and never retries: a read or toggle must not contend
// with a serial session, so failure is reported immediately.
func (d *FTDI) open(ctx context.Context, serial string) (ftd2xx.Device, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return ftd2xx.Open(serial)
}
