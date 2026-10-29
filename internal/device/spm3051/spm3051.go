// Package spm3051 implements device.PowerSupply for the OWON SPM3051 UART
// lab power supply.
//
// Protocol (verified against a USB/UART capture from firmware V2.0.0):
//
//	Queries are LF-terminated ("\n"); set commands are CRLF-terminated ("\r\n").
//	Every reply is a single line terminated with CRLF.
//
//	*IDN?                       → "OWON,SPM3051,24460467,FV:V2.0.0"
//	OUTP ON / OUTP OFF          – enable / disable the output
//	OUTP?                       → "ON" | "OFF"  (some firmware may reply "1" | "0")
//	VOLT <v>                    – set output voltage (V)
//	VOLT?                       → "5.400"
//	CURR <a>                    – set current limit (A)
//	CURR?                       → "4.900"
//	VOLT:LIM <v>                – set OVP trip voltage
//	VOLT:LIM?                   → "9.000"
//	CURR:LIM <a>                – set OCP trip current
//	CURR:LIM?                   → "5.000"
//	SYSTem:REMote / SYSTem:LOCal – take / release front-panel control
//	MEASure:ALL:INFO?           → "V,A,W,OVP,OCP,OTP,mode\r\n"
//	                              e.g. "5.010,0.000,0.000,OFF,OFF,OFF,1"
//	                              mode: 0 = standby, 1 = CV, 2 = CC, 3 = fault
//
// Front-panel (remote-control) policy: opening the port and polling does NOT
// lock the keys.  SYSTem:REMote is sent just before the first set command of a
// control session, and SYSTem:LOCal is sent again
//
//   - after remoteIdleRelease (2 s) without any set command, so remote mode is
//     only a short, temporary state and the knobs work while the app is merely
//     monitoring,
//   - on Disconnect / before the port is closed, so the PC never leaves the
//     power supply locked in remote mode, and
//   - on Connect, right after the identification/set-point reads, so the
//     panel starts unlocked (and a previous crashed session's lock is cleared).
//
// Set-points are cached and re-read from the device periodically so changes
// made on the front panel show up in the UI.
package spm3051

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/serialport"
)

const (
	defaultBaud    = 9600
	readTimeout    = 300 * time.Millisecond // device replies to a 7-field line in <50 ms at 9600
	deviceTypeName = "spm3051"
	maxCommErrors  = 3 // consecutive I/O errors before declaring disconnected

	// setpointResyncEvery re-reads VOLT?/CURR?/limits every N polls (~5 s at
	// the manager's 1 s poll interval) to pick up front-panel changes.
	setpointResyncEvery = 5
	// remoteIdleRelease hands front-panel control back this long after the
	// last set command, making remote mode a short, temporary state used only
	// while the app is actually driving the supply.  Polling/measurements
	// continue in local mode.
	remoteIdleRelease = 2 * time.Second

	maxLineLen = 256

	queryTerm = "\n"   // capture: "OUTP?\n", "MEASure:ALL:INFO?\n", ...
	cmdTerm   = "\r\n" // capture: "OUTP ON\r\n", "VOLT 5.0\r\n", ...
)

// Measurement mode values returned in the last MEASure:ALL:INFO? field.
const (
	modeStandby = 0
	modeCV      = 1
	modeCC      = 2
	modeFault   = 3
)

// SPM3051 drives an OWON SPM3051 power supply over a UART/USB-CDC port.
type SPM3051 struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serialport.Port
	connected bool

	// cached set-points (seeded from the device on connect, resynced every
	// setpointResyncEvery polls)
	voltSet  float64
	currSet  float64
	ovpLim   float64
	ocpLim   float64
	outputOn bool

	// remote-control bookkeeping
	remoteMode   bool        // true while SYSTem:REMote is active (panel locked)
	lastControl  time.Time   // last set command; drives remoteIdleRelease
	releaseTimer *time.Timer // fires remoteIdleRelease after the last set command

	// last good state, returned on a transient I/O error so the UI does not
	// drop to zero for a single poll
	last *device.PowerState

	pollCount  int
	commErrCnt int

	// rxBuf holds bytes read past the last newline.  serial.Read may return
	// several replies at once; leftovers belong to the next readLine.
	rxBuf []byte
}

// New creates a new SPM3051 driver.  baud=0 uses the hardware default (9600).
func New(id, name, port string, baud int) *SPM3051 {
	if baud == 0 {
		baud = defaultBaud
	}
	return &SPM3051{id: id, name: name, port: port, baud: baud}
}

func (s *SPM3051) ID() string   { return s.id }
func (s *SPM3051) Name() string { return s.name }
func (s *SPM3051) Type() string { return deviceTypeName }

func (s *SPM3051) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *SPM3051) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connected {
		return nil
	}
	// Close any stale handle so the OS port is free to reopen (Windows:
	// prevents "access denied").
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	p, err := serialport.OpenFor(s.id, s.port, serialport.Mode(s.baud))
	if err != nil {
		return fmt.Errorf("spm3051 %s: %w", s.id, err)
	}
	if err := p.SetReadTimeout(readTimeout); err != nil {
		p.Close()
		return fmt.Errorf("spm3051 %s: set timeout: %w", s.id, err)
	}
	s.conn = p
	s.connected = true
	s.remoteMode = false
	s.pollCount = 0
	s.commErrCnt = 0
	s.rxBuf = s.rxBuf[:0]
	s.last = nil

	// Flush stale OS buffer bytes from before the disconnect so the first
	// query cannot read garbage.
	_ = p.ResetInputBuffer()
	_ = p.ResetOutputBuffer()

	if err := s.initLocked(); err != nil {
		_ = s.disconnectLocked()
		return fmt.Errorf("spm3051 %s: init: %w", s.id, err)
	}
	s.last = s.lastStateLocked()
	return nil
}

func (s *SPM3051) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disconnectLocked()
}

// disconnectLocked releases front-panel control (best effort) and closes the
// port.  Must be called with s.mu held.
func (s *SPM3051) disconnectLocked() error {
	if !s.connected && s.conn == nil {
		return nil
	}
	// Always hand the panel back, even on the error-driven path: a PC that
	// only closes the USB port leaves the PSU locked in remote mode.
	// Drain() makes sure the command has left the host before Close(), or the
	// PSU may never see it.
	if s.conn != nil {
		_ = s.write("SYSTem:LOCal")
		_ = s.conn.Drain()
	}
	if s.releaseTimer != nil {
		s.releaseTimer.Stop()
		s.releaseTimer = nil
	}
	s.remoteMode = false
	s.connected = false
	s.commErrCnt = 0
	s.rxBuf = s.rxBuf[:0]
	var err error
	if s.conn != nil {
		err = s.conn.Close()
		s.conn = nil
	}
	return err
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// initLocked identifies the device and seeds the cached set-points.  Merely
// connecting must not lock the front panel, so no SYSTem:REMote is sent here.
// Must be called with s.mu held and s.connected == true.
func (s *SPM3051) initLocked() error {
	idn, idnErr := s.queryWithRetry("*IDN?", 3)
	if idnErr == nil {
		if strings.Contains(idn, "OWON") {
			log.Printf("[spm3051] %s: %s", s.id, idn)
		} else {
			log.Printf("[spm3051] %s: unexpected identification %q", s.id, idn)
		}
	}

	s.resyncSetpointsLocked()
	out, outErr := s.query("OUTP?")
	if outErr == nil {
		if on, ok := parseBool(out); ok {
			s.outputOn = on
		}
	}
	// Some compatible units may not implement *IDN?; require at least one
	// query to answer so a wrong port fails here instead of polling garbage.
	if idnErr != nil && outErr != nil {
		return fmt.Errorf("device not responding (*IDN? and OUTP? both failed)")
	}

	// Reading is done: hand the panel back.  Polling continues in local mode;
	// the device only enters remote mode temporarily around control commands.
	// This also clears a remote state left by a previous crashed session.
	_ = s.write("SYSTem:LOCal")
	s.remoteMode = false
	return nil
}

// queryWithRetry runs a query up to attempts times, retrying only on I/O
// errors (a parsed reply is returned as-is).
func (s *SPM3051) queryWithRetry(cmd string, attempts int) (string, error) {
	var (
		resp string
		err  error
	)
	for i := 0; i < attempts; i++ {
		if resp, err = s.query(cmd); err == nil {
			return resp, nil
		}
		if i+1 < attempts {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return "", err
}

// resyncSetpointsLocked re-reads the set-points from the device.  Failures are
// ignored: a stale cache is better than dropping a poll because one optional
// read timed out.  Must be called with s.mu held.
func (s *SPM3051) resyncSetpointsLocked() {
	if v, ok := s.queryFloat("VOLT?"); ok {
		s.voltSet = v
	}
	if v, ok := s.queryFloat("CURR?"); ok {
		s.currSet = v
	}
	if v, ok := s.queryFloat("VOLT:LIM?"); ok {
		s.ovpLim = v
	}
	if v, ok := s.queryFloat("CURR:LIM?"); ok {
		s.ocpLim = v
	}
}

func (s *SPM3051) queryFloat(cmd string) (float64, bool) {
	resp, err := s.query(cmd)
	if err != nil {
		return 0, false
	}
	return parseFloat(resp)
}

// query sends a query command (LF-terminated) and returns the reply line.
func (s *SPM3051) query(cmd string) (string, error) {
	if s.conn == nil {
		return "", fmt.Errorf("port is not open")
	}
	if _, err := s.conn.Write([]byte(cmd + queryTerm)); err != nil {
		return "", fmt.Errorf("write %q: %w", cmd, err)
	}
	resp, err := s.readLine()
	if err != nil {
		// Drop bytes that may arrive late; otherwise the next reply would be
		// read as the answer to the next command (permanent desync).
		_ = s.conn.ResetInputBuffer()
		return "", err
	}
	return resp, nil
}

// write sends a set command (CRLF-terminated, no reply expected).
func (s *SPM3051) write(cmd string) error {
	if s.conn == nil {
		return fmt.Errorf("port is not open")
	}
	_, err := s.conn.Write([]byte(cmd + cmdTerm))
	return err
}

// readLine returns the next '\n'-terminated reply line.  Reads are done in
// chunks; bytes past the newline are kept for the next call.
func (s *SPM3051) readLine() (string, error) {
	var scratch [128]byte
	for {
		if i := bytes.IndexByte(s.rxBuf, '\n'); i >= 0 {
			line := strings.TrimSpace(string(s.rxBuf[:i]))
			n := copy(s.rxBuf, s.rxBuf[i+1:])
			s.rxBuf = s.rxBuf[:n]
			return line, nil
		}
		if len(s.rxBuf) > maxLineLen {
			s.rxBuf = s.rxBuf[:0]
			_ = s.conn.ResetInputBuffer()
			return "", fmt.Errorf("reply exceeds %d bytes", maxLineLen)
		}
		n, err := s.conn.Read(scratch[:])
		if n > 0 {
			s.rxBuf = append(s.rxBuf, scratch[:n]...)
		}
		if err != nil {
			s.rxBuf = s.rxBuf[:0]
			return "", fmt.Errorf("read: %w", err)
		}
		if n == 0 {
			// Read timeout (the serial layer returns 0, nil on timeout).
			partial := len(s.rxBuf) > 0
			s.rxBuf = s.rxBuf[:0]
			_ = s.conn.ResetInputBuffer()
			if partial {
				return "", fmt.Errorf("read timeout: incomplete reply")
			}
			return "", fmt.Errorf("read timeout: no data")
		}
	}
}

// ensureRemoteLocked takes front-panel control before a set command.
func (s *SPM3051) ensureRemoteLocked() error {
	if s.remoteMode {
		return nil
	}
	if err := s.write("SYSTem:REMote"); err != nil {
		return fmt.Errorf("spm3051 %s: enter remote mode: %w", s.id, err)
	}
	s.remoteMode = true
	s.lastControl = time.Now()
	return nil
}

// releaseLocked hands the front panel back to the operator.  The port stays
// open and queries keep working, so monitoring continues.
func (s *SPM3051) releaseLocked() error {
	if s.releaseTimer != nil {
		s.releaseTimer.Stop()
		s.releaseTimer = nil
	}
	if !s.remoteMode {
		return nil
	}
	if err := s.write("SYSTem:LOCal"); err != nil {
		return err
	}
	s.remoteMode = false
	return nil
}

// armReleaseLocked (re)starts the post-operation timer: unless another set
// command arrives first, control returns to the panel remoteIdleRelease after
// the last one.  Must be called with s.mu held.
func (s *SPM3051) armReleaseLocked() {
	if s.releaseTimer != nil {
		s.releaseTimer.Stop()
	}
	s.releaseTimer = time.AfterFunc(remoteIdleRelease, s.releaseAfterIdle)
}

// releaseAfterIdle is the timer callback.
func (s *SPM3051) releaseAfterIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || !s.remoteMode {
		return
	}
	if time.Since(s.lastControl) < remoteIdleRelease {
		// A newer command arrived and re-armed its own timer.
		return
	}
	_ = s.releaseLocked()
}

// maybeReleaseLocked is a poll-time fallback in case the timer's LOC write
// failed; it retries on the next poll.
func (s *SPM3051) maybeReleaseLocked() {
	if !s.remoteMode || time.Since(s.lastControl) < remoteIdleRelease {
		return
	}
	_ = s.releaseLocked()
}

// ReleaseControl implements device.ControlReleaser: it unlocks the front panel
// immediately without disconnecting, so the UI keeps polling measurements.  The
// next SetVoltage/SetCurrent/SetOutput/SetOVP/SetOCP re-enters remote mode.
func (s *SPM3051) ReleaseControl(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.releaseLocked(); err != nil {
		return fmt.Errorf("spm3051 %s: release control: %w", s.id, err)
	}
	return nil
}

// applySetLocked enters remote mode when needed, validates v, and sends
// "<cmd> <v>".  Must be called with s.mu held.
func (s *SPM3051) applySetLocked(cmd, what string, v float64) error {
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	switch {
	case math.IsNaN(v), math.IsInf(v, 0):
		return fmt.Errorf("spm3051 %s: %s must be a finite number", s.id, what)
	case v < 0:
		return fmt.Errorf("spm3051 %s: %s must be >= 0 (got %v)", s.id, what, v)
	}
	if err := s.ensureRemoteLocked(); err != nil {
		return err
	}
	if err := s.write(fmt.Sprintf("%s %.3f", cmd, v)); err != nil {
		return fmt.Errorf("spm3051 %s: set %s: %w", s.id, what, err)
	}
	s.lastControl = time.Now()
	s.armReleaseLocked()
	return nil
}

// lastStateLocked returns a copy of the last good state with the current
// cached flags, or a set-point-only state before the first measurement.
func (s *SPM3051) lastStateLocked() *device.PowerState {
	st := &device.PowerState{Connected: true}
	if s.last != nil {
		*st = *s.last
	}
	st.Connected = true
	st.VoltSet = s.voltSet
	st.CurrSet = s.currSet
	st.OVPLimit = s.ovpLim
	st.OCPLimit = s.ocpLim
	st.OutputOn = s.outputOn
	return st
}

// failedLocked accounts for a communication error.  Transient failures return
// the last good state; after maxCommErrors in a row the device is closed so
// the manager's reconnect loop can retry.
func (s *SPM3051) failedLocked(op string, err error) (*device.PowerState, error) {
	s.commErrCnt++
	if s.commErrCnt >= maxCommErrors {
		n := s.commErrCnt
		_ = s.disconnectLocked()
		return nil, fmt.Errorf("spm3051 %s: %s (%d consecutive errors): %w", s.id, op, n, err)
	}
	return s.lastStateLocked(), nil
}

// ─── reply parsers ────────────────────────────────────────────────────────────

type measurement struct {
	volt, curr, power float64
	ovp, ocp, otp     bool
	mode              int
	hasMode           bool
}

// parseMeasure decodes "V,A,W,OVP,OCP,OTP,mode" (comma-separated on SPM3051
// firmware; the manual's space-separated example is accepted too).
func parseMeasure(resp string) (measurement, error) {
	var m measurement
	f := splitFields(resp)
	if len(f) < 3 {
		return m, fmt.Errorf("expected V,A,W – got %q", resp)
	}
	var okV, okA, okW bool
	m.volt, okV = parseFloat(f[0])
	m.curr, okA = parseFloat(f[1])
	m.power, okW = parseFloat(f[2])
	if !okV || !okA || !okW {
		return m, fmt.Errorf("non-numeric measurement %q", resp)
	}
	if len(f) > 3 {
		m.ovp, _ = parseBool(f[3])
	}
	if len(f) > 4 {
		m.ocp, _ = parseBool(f[4])
	}
	if len(f) > 5 {
		m.otp, _ = parseBool(f[5])
	}
	if len(f) > 6 {
		m.mode, m.hasMode = parseInt(f[6])
	}
	return m, nil
}

// splitFields splits a reply on commas when present, otherwise on whitespace.
func splitFields(line string) []string {
	if !strings.ContainsRune(line, ',') {
		return strings.Fields(line)
	}
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil
}

func parseInt(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	return v, err == nil
}

// parseBool accepts both the capture's "ON"/"OFF" and the manual's "1"/"0".
func parseBool(s string) (value, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ON", "1", "TRUE":
		return true, true
	case "OFF", "0", "FALSE":
		return false, true
	}
	return false, false
}

// ─── PowerSupply interface ────────────────────────────────────────────────────

func (s *SPM3051) GetState(_ context.Context) (*device.PowerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return &device.PowerState{Connected: false}, nil
	}

	resp, err := s.query("MEASure:ALL:INFO?")
	if err != nil {
		return s.failedLocked("MEASure:ALL:INFO?", err)
	}
	m, err := parseMeasure(resp)
	if err != nil {
		return s.failedLocked("MEASure:ALL:INFO? reply", err)
	}
	s.commErrCnt = 0 // successful round-trip resets the error counter

	// Output on/off is NOT part of the MEAS reply; OUTP? is authoritative.
	// Poll it every tick so front-panel (local-mode) changes are reflected.
	if out, err := s.query("OUTP?"); err == nil {
		if on, ok := parseBool(out); ok {
			s.outputOn = on
		}
	}

	// Pick up front-panel set-point changes every few polls.
	s.pollCount++
	if s.pollCount%setpointResyncEvery == 0 {
		s.resyncSetpointsLocked()
	}

	// Give the keys back once the app has stopped driving the supply.
	s.maybeReleaseLocked()

	cvMode := m.curr < s.currSet*0.99 // fallback when the reply has no mode field
	if m.hasMode {
		cvMode = m.mode == modeCV
	}

	st := &device.PowerState{
		Connected:    true,
		VoltMeas:     m.volt,
		CurrMeas:     m.curr,
		PowerMeas:    m.power,
		VoltSet:      s.voltSet,
		CurrSet:      s.currSet,
		OVPLimit:     s.ovpLim,
		OCPLimit:     s.ocpLim,
		OutputOn:     s.outputOn,
		OVPTriggered: m.ovp,
		OCPTriggered: m.ocp,
		OTPTriggered: m.otp,
		CVMode:       cvMode,
		// exposed so the UI can offer "release control" only while locked
		RemoteControl: s.remoteMode,
	}
	s.last = st
	return st, nil
}

func (s *SPM3051) SetVoltage(_ context.Context, volts float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applySetLocked("VOLT", "voltage", volts); err != nil {
		return err
	}
	s.voltSet = volts
	return nil
}

func (s *SPM3051) SetCurrent(_ context.Context, amps float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applySetLocked("CURR", "current", amps); err != nil {
		return err
	}
	s.currSet = amps
	return nil
}

func (s *SPM3051) SetOutput(_ context.Context, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.ensureRemoteLocked(); err != nil {
		return err
	}
	arg := "OFF"
	if on {
		arg = "ON"
	}
	if err := s.write("OUTP " + arg); err != nil {
		return fmt.Errorf("spm3051 %s: set output: %w", s.id, err)
	}
	s.outputOn = on
	s.lastControl = time.Now()
	s.armReleaseLocked()
	return nil
}

func (s *SPM3051) SetOVP(_ context.Context, volts float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applySetLocked("VOLT:LIM", "OVP limit", volts); err != nil {
		return err
	}
	s.ovpLim = volts
	return nil
}

func (s *SPM3051) SetOCP(_ context.Context, amps float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applySetLocked("CURR:LIM", "OCP limit", amps); err != nil {
		return err
	}
	s.ocpLim = amps
	return nil
}
