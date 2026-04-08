// Package usbslim implements device.USBHub for the USB-Slim 4-port USB hub.
//
// Binary protocol over UART (baud configurable, default 9600):
//
//	Frame: 55 5A CMD PORT VAL [VAL2] CKSUM
//	CKSUM  = sum of all bytes after the 55 5A header.
//
// Port mask: PORT1=0x01  PORT2=0x02  PORT3=0x04  PORT4=0x08
//
// Commands:
//
//	00 0F 00 0F       – query all port states (hub replies with 4×6-byte frames)
//	01 <port> <0|1>   – port OFF / ON
//	06 00 <0|1>       – lock OFF / ON  (requires follow-up 07 00 00)
//	09 00 <0|1>       – HW keys disable/enable  (requires follow-up 0A 00 00)
//	0F 00 <0|1>       – auto-save disable/enable (requires follow-up 10 00 00)
//	0B 0F 01 01       – default ON for all ports
package usbslim

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.bug.st/serial"

	"github.com/yuguorong/power_wing/internal/device"
)

const (
	defaultBaud    = 9600
	ackTimeout     = 500 * time.Millisecond
	deviceTypeName = "usbslim"

	cmdQuery        byte = 0x00
	cmdPort         byte = 0x01
	cmdLock         byte = 0x06
	cmdLockConf     byte = 0x07
	cmdHWKey        byte = 0x09
	cmdHWKeyConf    byte = 0x0A
	cmdAutoSave     byte = 0x0F
	cmdAutoSaveConf byte = 0x10
	cmdDefault      byte = 0x0B
)

// USBSlim drives a USB-Slim 4-port hub over its UART interface.
type USBSlim struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serial.Port
	connected bool

	// locally tracked state (hub echoes every sent frame as ACK)
	ports           [4]bool
	locked          bool
	hwKeysEnabled   bool
	autoSaveEnabled bool
}

// New creates a new USBSlim driver.  baud=0 uses 9600.
func New(id, name, port string, baud int) *USBSlim {
	if baud == 0 {
		baud = defaultBaud
	}
	return &USBSlim{
		id:            id,
		name:          name,
		port:          port,
		baud:          baud,
		hwKeysEnabled: true,
	}
}

func (u *USBSlim) ID() string   { return u.id }
func (u *USBSlim) Name() string { return u.name }
func (u *USBSlim) Type() string { return deviceTypeName }

func (u *USBSlim) IsConnected() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.connected
}

func (u *USBSlim) Connect() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.connected {
		return nil
	}
	mode := &serial.Mode{
		BaudRate: u.baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	p, err := serial.Open(u.port, mode)
	if err != nil {
		return fmt.Errorf("usbslim %s: connect: %w", u.id, err)
	}
	if err := p.SetReadTimeout(ackTimeout); err != nil {
		p.Close()
		return fmt.Errorf("usbslim %s: set timeout: %w", u.id, err)
	}
	// Assert DTR so the USB-CDC adapter (CH340/CP210x) enables its TX path.
	// Without this many adapters silently drop all writes.
	if err := p.SetDTR(true); err != nil {
		p.Close()
		return fmt.Errorf("usbslim %s: set DTR: %w", u.id, err)
	}
	u.conn = p
	u.connected = true
	// Seed real port state from the device.
	_ = u.queryAllPorts()
	return nil
}

func (u *USBSlim) Disconnect() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return nil
	}
	u.connected = false
	return u.conn.Close()
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// buildFrame builds a 6-byte command frame: 55 5A CMD PORT VAL CKSUM
func buildFrame(cmd, port, val byte) []byte {
	cksum := cmd + port + val
	return []byte{0x55, 0x5A, cmd, port, val, cksum}
}

// buildFrame7 builds a 7-byte command frame: 55 5A CMD PORT V1 V2 CKSUM
func buildFrame7(cmd, port, v1, v2 byte) []byte {
	cksum := cmd + port + v1 + v2
	return []byte{0x55, 0x5A, cmd, port, v1, v2, cksum}
}

// send writes a frame and reads back the echo ACK (same frame reflected).
func (u *USBSlim) send(frame []byte) error {
	if _, err := u.conn.Write(frame); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	// drain the echo
	ack := make([]byte, len(frame))
	total := 0
	for total < len(frame) {
		n, err := u.conn.Read(ack[total:])
		if err != nil {
			return nil // timeout is OK – not all firmware versions echo
		}
		if n == 0 {
			break
		}
		total += n
	}
	return nil
}

// portMask maps port index (0-based) to mask byte.
var portMasks = [4]byte{0x01, 0x02, 0x04, 0x08}

// queryAllPorts sends the status-query command and parses the hub's reply.
// The hub returns 4 concatenated 6-byte frames, one per port:
//
//	55 5A 00 <portMask> <state(0|1)> <cksum>
//
// Must be called with u.mu held and u.connected == true.
func (u *USBSlim) queryAllPorts() error {
	frame := buildFrame(cmdQuery, 0x0F, 0x00)
	if _, err := u.conn.Write(frame); err != nil {
		return fmt.Errorf("queryAllPorts write: %w", err)
	}
	// Read up to 4×6 = 24 bytes (one 6-byte frame per port).
	buf := make([]byte, 24)
	total := 0
	for total < len(buf) {
		n, err := u.conn.Read(buf[total:])
		total += n
		if err != nil {
			break // timeout or short read – parse whatever arrived
		}
		if n == 0 {
			break
		}
	}
	// Parse each 6-byte frame: 55 5A 00 <mask> <val> <cksum>
	for i := 0; i+5 < total; i += 6 {
		if buf[i] != 0x55 || buf[i+1] != 0x5A || buf[i+2] != cmdQuery {
			continue
		}
		mask := buf[i+3]
		state := buf[i+4] != 0x00
		for j, pm := range portMasks {
			if mask == pm {
				u.ports[j] = state
				break
			}
		}
	}
	return nil
}

// ─── USBHub interface ─────────────────────────────────────────────────────────

func (u *USBSlim) GetState(_ context.Context) (*device.HubState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return &device.HubState{
		Connected:       u.connected,
		Ports:           u.ports,
		Locked:          u.locked,
		HWKeysEnabled:   u.hwKeysEnabled,
		AutoSaveEnabled: u.autoSaveEnabled,
	}, nil
}

func (u *USBSlim) SetPort(_ context.Context, port int, on bool) error {
	if port < 1 || port > 4 {
		return fmt.Errorf("usbslim: invalid port %d (1-4)", port)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return fmt.Errorf("usbslim %s: not connected", u.id)
	}
	mask := portMasks[port-1]
	val := byte(0)
	if on {
		val = 0x01
	}
	if err := u.send(buildFrame(cmdPort, mask, val)); err != nil {
		return err
	}
	u.ports[port-1] = on
	return nil
}

func (u *USBSlim) SetLock(_ context.Context, locked bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return fmt.Errorf("usbslim %s: not connected", u.id)
	}
	val := byte(0)
	if locked {
		val = 0x01
	}
	if err := u.send(buildFrame(cmdLock, 0x00, val)); err != nil {
		return err
	}
	// confirmation packet
	_ = u.send(buildFrame(cmdLockConf, 0x00, 0x00))
	u.locked = locked
	return nil
}

func (u *USBSlim) SetHWKeys(_ context.Context, enabled bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return fmt.Errorf("usbslim %s: not connected", u.id)
	}
	val := byte(0)
	if enabled {
		val = 0x01
	}
	if err := u.send(buildFrame(cmdHWKey, 0x00, val)); err != nil {
		return err
	}
	_ = u.send(buildFrame(cmdHWKeyConf, 0x00, 0x00))
	u.hwKeysEnabled = enabled
	return nil
}

func (u *USBSlim) SetAutoSave(_ context.Context, enabled bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return fmt.Errorf("usbslim %s: not connected", u.id)
	}
	val := byte(0)
	if enabled {
		val = 0x01
	}
	if err := u.send(buildFrame(cmdAutoSave, 0x00, val)); err != nil {
		return err
	}
	_ = u.send(buildFrame(cmdAutoSaveConf, 0x00, 0x00))
	u.autoSaveEnabled = enabled
	return nil
}

// SetDefaultOn sends the "Default ON" command for all 4 ports (mask 0x0F).
func (u *USBSlim) SetDefaultOn(_ context.Context, portMask byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected {
		return fmt.Errorf("usbslim %s: not connected", u.id)
	}
	return u.send(buildFrame7(cmdDefault, portMask, 0x01, 0x01))
}
