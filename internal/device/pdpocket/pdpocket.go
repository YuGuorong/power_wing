// Package pdpocket implements device.PowerSupply for the PD Pocket UART PSU.
//
// Protocol (115200 8N1, commands + "\r\n"):
//
//	MEAS:VOLT?   → "5.2345\r\n"   measured voltage
//	MEAS:CURR?   → "1.2345\r\n"   measured current
//	MEAS:POW?    → "65\r\n"       measured power (W)
//	get vset     → "12.0000\r\n"  set voltage
//	get iset     → "3.0000\r\n"   set current limit
//	gettemp 1    → "24.7\r\n"     device temperature
//	VOLT <v>     – set output voltage (1–36 V)
//	CURR <a>     – set current limit  (0.3–8.5 A)
//	OUTP ON/OFF  – enable / disable output
//	setpower <w> – set max power (0–255 W)
//	SYST:VERS?   → "1.1.0\r\n"   firmware version
package pdpocket

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"

	"github.com/yuguorong/power_wing/internal/device"
)

const (
	defaultBaud    = 115200
	readTimeout    = 150 * time.Millisecond // per-Read call timeout; device responds in <10ms
	deviceTypeName = "pd_pocket"
	tempPollEvery  = 10 // query temperature only every N polls (~10 s at 1 s interval)
	maxCommErrors  = 3  // consecutive I/O errors before declaring disconnected
)

// PDPocket drives a PD Pocket power supply over its virtual COM port.
type PDPocket struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serial.Port
	connected bool

	// cached set-points (updated by SetVoltage / SetCurrent; only queried on first poll)
	voltSet       float64
	currSet       float64
	maxPow        float64
	outputOn      bool // last explicitly set output state
	outputOnKnown bool // true once SetOutput has been called at least once
	temperature   float64

	// poll bookkeeping
	initDone    bool // set-point read-back done on first GetState after connect
	pollCount   int  // incremented each GetState; used to throttle slow queries
	commErrCnt  int  // consecutive I/O errors; disconnect only after threshold
}

// New creates a new PDPocket driver.  baud=0 uses 115200.
func New(id, name, port string, baud int) *PDPocket {
	if baud == 0 {
		baud = defaultBaud
	}
	return &PDPocket{id: id, name: name, port: port, baud: baud}
}

func (p *PDPocket) ID() string   { return p.id }
func (p *PDPocket) Name() string { return p.name }
func (p *PDPocket) Type() string { return deviceTypeName }

func (p *PDPocket) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

func (p *PDPocket) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connected {
		return nil
	}
	// Close any stale handle left from a previous error-disconnect so that
	// the OS port is free to reopen (Windows: prevents "access denied").
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	mode := &serial.Mode{
		BaudRate: p.baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	sp, err := serial.Open(p.port, mode)
	if err != nil {
		return fmt.Errorf("pd_pocket %s: connect: %w", p.id, err)
	}
	if err := sp.SetReadTimeout(readTimeout); err != nil {
		sp.Close()
		return fmt.Errorf("pd_pocket %s: set timeout: %w", p.id, err)
	}
	p.conn = sp
	p.connected = true
	p.initDone = false // re-read set-points on next GetState
	p.pollCount = 0
	p.commErrCnt = 0
	// Flush any stale bytes left in the OS serial buffers from before the
	// disconnect. Without this a reconnected port may read garbage on the
	// first query and immediately hit maxCommErrors again.
	_ = sp.ResetInputBuffer()
	_ = sp.ResetOutputBuffer()
	return nil
}

func (p *PDPocket) Disconnect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.disconnectLocked()
}

// disconnectLocked closes the port and resets connection state.
// Must be called with p.mu held.
func (p *PDPocket) disconnectLocked() error {
	if !p.connected && p.conn == nil {
		return nil
	}
	p.connected = false
	p.commErrCnt = 0
	var err error
	if p.conn != nil {
		err = p.conn.Close()
		p.conn = nil
	}
	return err
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func (p *PDPocket) write(cmd string) error {
	_, err := p.conn.Write([]byte(cmd + "\r\n"))
	return err
}

// writeCmd sends a write-only command (VOLT/CURR/OUTP) and drains the echo
// reply that some PD Pocket firmware sends (e.g. "OK\r\n").  If the device
// does not echo, readLine will time out after readTimeout and the error is
// silently ignored, keeping the buffer clean either way.
func (p *PDPocket) writeCmd(cmd string) error {
	if err := p.write(cmd); err != nil {
		return err
	}
	p.readLine() // discard echo / "OK" response; ignore timeout error

	return nil
}

func (p *PDPocket) readLine() (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for len(buf) < 512 {
		n, err := p.conn.Read(b)
		if err != nil {
			return strings.TrimSpace(string(buf)), fmt.Errorf("read: %w", err)
		}
		if n == 0 {
			if len(buf) > 0 {
				break
			}
			return "", fmt.Errorf("read timeout – no data")
		}
		buf = append(buf, b[:n]...)
		if b[0] == '\n' {
			break
		}
	}
	return strings.TrimSpace(string(buf)), nil
}

func (p *PDPocket) query(cmd string) (string, error) {
	if err := p.write(cmd); err != nil {
		return "", err
	}
	resp, err := p.readLine()
	return resp, err
}

func parseF(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// ─── PowerSupply interface ────────────────────────────────────────────────────

func (p *PDPocket) GetState(ctx context.Context) (*device.PowerState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return &device.PowerState{Connected: false}, nil
	}

	// ── Core measurements (every poll, target <100 ms total) ────────────────
	voltMeasStr, err := p.query("MEAS:VOLT?")
	if err != nil {
		p.commErrCnt++
		if p.commErrCnt >= maxCommErrors {
			errCount := p.commErrCnt
			_ = p.disconnectLocked()
			return nil, fmt.Errorf("GetState VOLT (%d consecutive errors): %w", errCount, err)
		}
		return &device.PowerState{Connected: true, VoltSet: p.voltSet, CurrSet: p.currSet, OutputOn: p.outputOnKnown && p.outputOn}, nil
	}
	currMeasStr, err := p.query("MEAS:CURR?")
	if err != nil {
		p.commErrCnt++
		if p.commErrCnt >= maxCommErrors {
			errCount := p.commErrCnt
			_ = p.disconnectLocked()
			return nil, fmt.Errorf("GetState CURR (%d consecutive errors): %w", errCount, err)
		}
		return &device.PowerState{Connected: true, VoltSet: p.voltSet, CurrSet: p.currSet, OutputOn: p.outputOnKnown && p.outputOn}, nil
	}
	powMeasStr, err := p.query("MEAS:POW?")
	if err != nil {
		p.commErrCnt++
		if p.commErrCnt >= maxCommErrors {
			errCount := p.commErrCnt
			_ = p.disconnectLocked()
			return nil, fmt.Errorf("GetState POW (%d consecutive errors): %w", errCount, err)
		}
		return &device.PowerState{Connected: true, VoltSet: p.voltSet, CurrSet: p.currSet, OutputOn: p.outputOnKnown && p.outputOn}, nil
	}
	p.commErrCnt = 0 // successful round-trip resets the error counter

	// ── Set-point read-back: only once after connect ─────────────────────────
	// Afterwards voltSet / currSet are kept in sync by SetVoltage / SetCurrent.
	if !p.initDone {
		if s, e := p.query("get vset"); e == nil {
			p.voltSet = parseF(s)
		}
		if s, e := p.query("get iset"); e == nil {
			p.currSet = parseF(s)
		}
		p.initDone = true
	}

	// ── Temperature: slow query, refresh every tempPollEvery polls (~10 s) ───
	p.pollCount++
	if p.pollCount%tempPollEvery == 1 {
		if s, e := p.query("gettemp 1"); e == nil {
			p.temperature = parseF(strings.TrimRight(s, "r"))
		}
	}

	voltMeas := parseF(voltMeasStr)
	currMeas := parseF(currMeasStr)
	powMeas := parseF(powMeasStr)

	outputOn := p.outputOnKnown && p.outputOn ||
		!p.outputOnKnown && (powMeas > 0 || voltMeas > 0.1)

	return &device.PowerState{
		Connected:   true,
		VoltMeas:    voltMeas,
		CurrMeas:    currMeas,
		PowerMeas:   powMeas,
		VoltSet:     p.voltSet,
		CurrSet:     p.currSet,
		OutputOn:    outputOn,
		Temperature: p.temperature,
		CVMode:      currMeas < p.currSet*0.99,
	}, nil
}

func (p *PDPocket) SetVoltage(_ context.Context, volts float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return fmt.Errorf("pd_pocket %s: not connected", p.id)
	}
	if err := p.writeCmd(fmt.Sprintf("VOLT %.1f", volts)); err != nil {
		return err
	}
	p.voltSet = volts
	return nil
}

func (p *PDPocket) SetCurrent(_ context.Context, amps float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return fmt.Errorf("pd_pocket %s: not connected", p.id)
	}
	if err := p.writeCmd(fmt.Sprintf("CURR %.1f", amps)); err != nil {
		return err
	}
	p.currSet = amps
	return nil
}

func (p *PDPocket) SetOutput(_ context.Context, on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return fmt.Errorf("pd_pocket %s: not connected", p.id)
	}
	cmd := "OUTP OFF"
	if on {
		cmd = "OUTP ON"
	}
	if err := p.writeCmd(cmd); err != nil {
		return err
	}
	p.outputOn = on
	p.outputOnKnown = true
	return nil
}

// SetOVP is not a direct command – PD Pocket exposes setpower instead.
func (p *PDPocket) SetOVP(_ context.Context, _ float64) error {
	return nil // not supported
}

func (p *PDPocket) SetOCP(_ context.Context, amps float64) error {
	return p.SetCurrent(context.Background(), amps)
}
