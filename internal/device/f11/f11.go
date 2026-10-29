// Package f11 implements device.PowerMonitor for the F-11 (AFU IoT)
// high-precision current/voltage meter.
//
// Protocol (ASCII over USB-Serial CH340, 38400-8-N-1):
//
//	Configuration commands (sent on connect):
//	    sys interval <ms>   – sample interval, 10-10000 ms
//	    sys setprint <0|1|2> – 0=off, 1=current only, 2=current+voltage
//	    sys baudrate <0|1|2> – 0=9600, 1=19200, 2=38400
//
//	Continuous measurement output (when setprint 2):
//	    <current>,<voltage>\n
//	    e.g. "0000000,00908"
//
// The first field is the current value and the second is the voltage value.
// Per the protocol skill, the exact physical scaling is not documented, so the
// driver applies a configurable scale factor.  The default assumes current is
// reported in µA (→ A, ÷1e6) and voltage in mV (→ V, ÷1000), which matches the
// F-11's setcal unit (µA) and typical meter output.  Adjust the scale
// constants if the device reports different units.
package f11

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/serialport"
)

const (
	defaultBaud    = 38400
	readTimeout    = 200 * time.Millisecond
	deviceTypeName = "f11"

	// Default sample interval for normal power-meter monitoring (simple UI).
	defaultIntervalMs = 500

	// Scale factors for raw integer fields.
	// current_raw is in µA → A (÷1e6); voltage_raw is in mV → V (÷1000).
	currentScale = 1e-6
	voltageScale = 1e-3
)

// F11 drives an F-11 power monitor meter over a USB serial port.
type F11 struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serialport.Port
	connected bool

	// reader lifecycle
	readerStop chan struct{}
	readerWG   sync.WaitGroup

	// latest decoded sample
	curr float64
	volt float64

	// monitor (average/peak) mode
	monitoring  bool
	avgCurr     float64
	avgVolt     float64
	peakCurr    float64
	peakVolt    float64
	sampleCount int

	intervalMs int

	// sampleHandler is invoked (in the reader goroutine) on every decoded
	// sample so the manager can push real-time updates.
	sampleHandler func(*device.MonitorState)
}

// New creates a new F11 driver.  baud=0 uses 38400.
func New(id, name, port string, baud int) *F11 {
	if baud == 0 {
		baud = defaultBaud
	}
	return &F11{
		id:         id,
		name:       name,
		port:       port,
		baud:       baud,
		intervalMs: defaultIntervalMs,
	}
}

func (f *F11) ID() string   { return f.id }
func (f *F11) Name() string { return f.name }
func (f *F11) Type() string { return deviceTypeName }

func (f *F11) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *F11) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connected {
		return nil
	}
	if f.conn != nil {
		_ = f.conn.Close()
		f.conn = nil
	}
	p, err := serialport.OpenFor(f.id, f.port, serialport.Mode(f.baud))
	if err != nil {
		return fmt.Errorf("f11 %s: connect: %w", f.id, err)
	}
	if err := p.SetReadTimeout(readTimeout); err != nil {
		p.Close()
		return fmt.Errorf("f11 %s: set timeout: %w", f.id, err)
	}
	f.conn = p
	f.connected = true
	_ = p.ResetInputBuffer()
	_ = p.ResetOutputBuffer()

	// Configure the device: sample interval + current+voltage printing.
	f.writeLocked(fmt.Sprintf("sys interval %d", f.intervalMs))
	f.writeLocked("sys setprint 2")

	// Start the continuous reader goroutine.
	f.readerStop = make(chan struct{})
	f.readerWG.Add(1)
	go f.readLoop()
	return nil
}

func (f *F11) Disconnect() error {
	f.mu.Lock()
	if f.readerStop != nil {
		close(f.readerStop)
		f.readerStop = nil
	}
	if !f.connected && f.conn == nil {
		f.mu.Unlock()
		f.readerWG.Wait()
		return nil
	}
	f.connected = false
	var err error
	if f.conn != nil {
		err = f.conn.Close()
		f.conn = nil
	}
	f.mu.Unlock()
	f.readerWG.Wait()
	return err
}

// writeLocked sends a command line.  Must be called with f.mu held.
func (f *F11) writeLocked(cmd string) error {
	if !f.connected || f.conn == nil {
		return fmt.Errorf("not connected")
	}
	_, err := f.conn.Write([]byte(cmd + "\r\n"))
	return err
}

// readLoop continuously reads measurement lines from the device and updates
// the latest sample plus average/peak statistics.
func (f *F11) readLoop() {
	defer f.readerWG.Done()
	// Hold a local reference: Disconnect may nil out f.conn concurrently, but
	// reading from the closed port simply returns an error which we handle.
	f.mu.Lock()
	conn := f.conn
	stop := f.readerStop
	f.mu.Unlock()
	if conn == nil {
		return
	}
	buf := make([]byte, 256)
	line := ""
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := conn.Read(buf)
		if err != nil {
			// Timeout is expected between samples; only bail when stopped.
			select {
			case <-stop:
				return
			default:
			}
			continue
		}
		if n == 0 {
			continue
		}
		line += string(buf[:n])
		for {
			idx := strings.IndexByte(line, '\n')
			if idx < 0 {
				break
			}
			raw := strings.TrimSpace(line[:idx])
			line = line[idx+1:]
			if raw != "" {
				f.handleSample(raw)
			}
		}
	}
}

// handleSample parses one "<current>,<voltage>" line and updates state.
func (f *F11) handleSample(raw string) {
	parts := strings.Split(raw, ",")
	if len(parts) < 2 {
		return
	}
	curRaw, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	volRaw, err2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	cur := float64(curRaw) * currentScale
	vol := float64(volRaw) * voltageScale

	f.mu.Lock()
	f.curr = cur
	f.volt = vol
	if f.monitoring {
		f.sampleCount++
		// Running average
		n := float64(f.sampleCount)
		f.avgCurr = f.avgCurr + (cur-f.avgCurr)/n
		f.avgVolt = f.avgVolt + (vol-f.avgVolt)/n
		// Peak
		if cur > f.peakCurr {
			f.peakCurr = cur
		}
		if vol > f.peakVolt {
			f.peakVolt = vol
		}
	}
	st := f.snapshotLocked()
	handler := f.sampleHandler
	f.mu.Unlock()

	if handler != nil {
		handler(st)
	}
}

// snapshotLocked builds a MonitorState from current fields.
// Must be called with f.mu held.
func (f *F11) snapshotLocked() *device.MonitorState {
	return &device.MonitorState{
		Connected:   f.connected,
		Current:     f.curr,
		Voltage:     f.volt,
		Monitoring:  f.monitoring,
		AvgCurrent:  f.avgCurr,
		AvgVoltage:  f.avgVolt,
		PeakCurrent: f.peakCurr,
		PeakVoltage: f.peakVolt,
		SampleCount: f.sampleCount,
		IntervalMs:  f.intervalMs,
	}
}

// ─── PowerMonitor interface ───────────────────────────────────────────────────

func (f *F11) GetState(_ context.Context) (*device.MonitorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotLocked(), nil
}

func (f *F11) SetInterval(_ context.Context, ms int) error {
	if ms < 10 {
		ms = 10
	}
	if ms > 10000 {
		ms = 10000
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intervalMs = ms
	if f.connected {
		return f.writeLocked(fmt.Sprintf("sys interval %d", ms))
	}
	return nil
}

func (f *F11) SetPrint(_ context.Context, mode int) error {
	if mode < 0 {
		mode = 0
	}
	if mode > 2 {
		mode = 2
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connected {
		return f.writeLocked(fmt.Sprintf("sys setprint %d", mode))
	}
	return nil
}

// StartMonitor begins accumulating average/peak statistics.
func (f *F11) StartMonitor(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.monitoring = true
	f.sampleCount = 0
	f.avgCurr = 0
	f.avgVolt = 0
	f.peakCurr = 0
	f.peakVolt = 0
	return nil
}

// StopMonitor stops accumulating average/peak statistics.
func (f *F11) StopMonitor(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.monitoring = false
	return nil
}

func (f *F11) SetSampleHandler(fn func(*device.MonitorState)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sampleHandler = fn
}

// ensure F11 satisfies device.PowerMonitor at compile time.
var _ device.PowerMonitor = (*F11)(nil)
