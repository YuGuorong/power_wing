// Package powerz implements device.PowerMonitor for ChargerLAB POWER-Z USB
// power meters (KM002C, KM003C and compatible models).
//
// Protocol (binary framing over the device's CDC-ACM virtual serial port):
//
//	Command header (4 bytes, little-endian bit fields):
//	    byte 0: type:7 | reserved:1        (0x02 Connect, 0x03 Disconnect, 0x0C GetData)
//	    byte 1: transaction id (rolling 0-255)
//	    byte 2-3: attribute:15 << 1 | unused:1
//
//	GetData for ADC:  0C <tid> 02 00
//	Response PutData: 41 <tid> <count:2> then one or more logical packets:
//	    <attr:15> <next:1> <chunk:6> <size:10>   followed by <size> payload bytes
//
// The ADC payload is 44 bytes: Vbus/Ibus and their filtered averages in µV/µA,
// temperature in 1/128 °C, CC1/CC2/D+/D-/VDD in 0.1 mV, then sample rate and
// flags and the averaged CC2/D+/D- values in mV.
//
// The device exposes the same command set over vendor-bulk (WinUSB), CDC and
// HID. This driver uses the CDC port: Windows 10/11 enumerate it as a COM port
// without any driver install, and the baud rate is ignored by the device.
// See docs/api_powerz.md for the full protocol notes.
package powerz

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/serialport"
)

const (
	deviceTypeName = "powerz"
	defaultBaud    = 115200 // ignored by the device; any value works
	defaultName    = "POWER-Z"

	// readTimeout bounds each individual serial Read; the transaction as a
	// whole is bounded by transactionTimeout below.
	readTimeout        = 100 * time.Millisecond
	transactionTimeout = 300 * time.Millisecond

	defaultIntervalMs = 500
	minIntervalMs     = 10
	maxIntervalMs     = 10000

	adcPayloadSize = 44
	maxPayloadSize = 8 * 1024 // protocol notes: size <= 1024-8, chunk*size <= 4080

	cmdConnect    = 0x02
	cmdDisconnect = 0x03
	cmdGetData    = 0x0C

	respAccept  = 0x05
	respReject  = 0x06
	respPutData = 0x41

	attADC = 0x0001
)

var (
	errNotConnected = errors.New("not connected")
	errTimeout      = errors.New("response timeout")
)

// adcSample is one decoded ADC measurement.
type adcSample struct {
	volt  float64 // V
	curr  float64 // A
	tempC float64 // °C
}

// PowerZ drives a POWER-Z power meter over its CDC virtual serial port.
type PowerZ struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serialport.Port
	connected bool
	tid       uint8

	// sampling loop
	loopRunning bool
	stopCh      chan struct{}
	intervalCh  chan struct{}
	wg          sync.WaitGroup
	intervalMs  int

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

	// sampleHandler is invoked (in the sampling goroutine) on every decoded
	// sample so the manager can push real-time updates.
	sampleHandler func(*device.MonitorState)
}

// New creates a POWER-Z driver. baud<=0 uses the device default (115200; the
// device ignores the value). An empty name falls back to "POWER-Z".
func New(id, name, port string, baud int) *PowerZ {
	if baud <= 0 {
		baud = defaultBaud
	}
	if name == "" {
		name = defaultName
	}
	return &PowerZ{
		id:         id,
		name:       name,
		port:       port,
		baud:       baud,
		intervalMs: defaultIntervalMs,
		intervalCh: make(chan struct{}, 1),
	}
}

func (p *PowerZ) ID() string   { return p.id }
func (p *PowerZ) Name() string { return p.name }
func (p *PowerZ) Type() string { return deviceTypeName }

func (p *PowerZ) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

func (p *PowerZ) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connected {
		return nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}

	port, err := serialport.OpenFor(p.id, p.port, serialport.Mode(p.baud))
	if err != nil {
		return fmt.Errorf("powerz %s: %w", p.id, err)
	}
	if err := port.SetReadTimeout(readTimeout); err != nil {
		_ = port.Close()
		return fmt.Errorf("powerz %s: set timeout: %w", p.id, err)
	}
	_ = port.ResetInputBuffer()
	_ = port.ResetOutputBuffer()

	p.conn = port
	p.connected = true

	// Open a session. Over CDC the device may not acknowledge Connect at all;
	// the first GetData confirms the link, so a missing reply is not an error.
	_ = p.writeLocked([]byte{cmdConnect, p.nextTIDLocked(), 0, 0})

	if !p.loopRunning {
		p.loopRunning = true
		p.stopCh = make(chan struct{})
		p.wg.Add(1)
		go p.pollLoop(p.stopCh)
	}
	return nil
}

func (p *PowerZ) Disconnect() error {
	p.mu.Lock()
	if p.stopCh != nil {
		close(p.stopCh)
		p.stopCh = nil
	}
	if p.connected && p.conn != nil {
		// Best effort: end the session before the port disappears.
		_ = p.writeLocked([]byte{cmdDisconnect, p.nextTIDLocked(), 0, 0})
	}
	p.connected = false
	conn := p.conn
	p.conn = nil
	p.mu.Unlock()

	var err error
	if conn != nil {
		err = conn.Close()
	}
	p.wg.Wait()
	return err
}

// nextTIDLocked returns the next transaction id. Must be called with p.mu held.
func (p *PowerZ) nextTIDLocked() uint8 {
	p.tid++
	return p.tid
}

// writeLocked sends raw command bytes. Must be called with p.mu held.
func (p *PowerZ) writeLocked(b []byte) error {
	if p.conn == nil {
		return errNotConnected
	}
	_, err := p.conn.Write(b)
	return err
}

// pollLoop samples the meter at the configured interval until stop is closed.
func (p *PowerZ) pollLoop(stop <-chan struct{}) {
	defer p.wg.Done()
	defer func() {
		p.mu.Lock()
		p.loopRunning = false
		p.mu.Unlock()
	}()

	first := true
	for {
		p.mu.Lock()
		iv := p.intervalMs
		p.mu.Unlock()
		if iv < minIntervalMs {
			iv = minIntervalMs
		}
		delay := time.Duration(iv) * time.Millisecond
		if first {
			delay = 0
			first = false
		}
		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-p.intervalCh:
			timer.Stop()
			continue
		case <-timer.C:
		}
		p.sampleOnce()
	}
}

// sampleOnce performs a single GetData transaction and updates the cached
// sample, statistics, and the registered sample handler.
func (p *PowerZ) sampleOnce() {
	s, err := p.readADC()
	if err != nil {
		if errors.Is(err, errNotConnected) || errors.Is(err, errTimeout) {
			return
		}
		// An intentional Disconnect closes the port underneath a pending
		// read; don't report that as a device failure.
		p.mu.Lock()
		stillConnected := p.connected
		p.mu.Unlock()
		if !stillConnected {
			return
		}
		log.Printf("[powerz] %s: read: %v", p.id, err)
		p.markDisconnected()
		return
	}

	p.mu.Lock()
	p.curr = s.curr
	p.volt = s.volt
	if p.monitoring {
		p.sampleCount++
		n := float64(p.sampleCount)
		p.avgCurr += (s.curr - p.avgCurr) / n
		p.avgVolt += (s.volt - p.avgVolt) / n
		if s.curr > p.peakCurr {
			p.peakCurr = s.curr
		}
		if s.volt > p.peakVolt {
			p.peakVolt = s.volt
		}
	}
	st := p.snapshotLocked()
	handler := p.sampleHandler
	p.mu.Unlock()

	if handler != nil {
		handler(st)
	}
}

// readADC sends a GetData(ADC) request and decodes the first ADC payload.
func (p *PowerZ) readADC() (adcSample, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected || p.conn == nil {
		return adcSample{}, errNotConnected
	}

	attr := uint16(attADC << 1) // attribute lives in bits 17-31 of the header
	tid := p.nextTIDLocked()
	cmd := []byte{cmdGetData, tid, byte(attr), byte(attr >> 8)}
	if err := p.writeLocked(cmd); err != nil {
		return adcSample{}, err
	}
	return p.readADCLocked(time.Now().Add(transactionTimeout))
}

// readADCLocked reads packets until an ADC payload arrives or the deadline
// expires. Must be called with p.mu held.
func (p *PowerZ) readADCLocked(deadline time.Time) (adcSample, error) {
	for {
		hdr, err := p.readNLocked(4, deadline)
		if err != nil {
			return adcSample{}, err
		}
		switch hdr[0] & 0x7F {
		case respPutData:
			for {
				extB, err := p.readNLocked(4, deadline)
				if err != nil {
					return adcSample{}, err
				}
				ext := binary.LittleEndian.Uint32(extB)
				attr := int(ext & 0x7FFF)
				next := ext&0x8000 != 0
				size := int(ext >> 22)
				if size > maxPayloadSize {
					return adcSample{}, fmt.Errorf("bad payload size %d", size)
				}
				payload, err := p.readNLocked(size, deadline)
				if err != nil {
					return adcSample{}, err
				}
				if attr == attADC {
					if s, ok := parseADC(payload); ok {
						return s, nil
					}
				}
				if !next {
					break
				}
			}
		case respReject:
			return adcSample{}, errors.New("GetData rejected by device")
		default:
			// Control replies (e.g. Accept for Connect) carry no payload;
			// keep waiting for the PutData that answers this request.
		}
	}
}

// readNLocked reads exactly n bytes or fails at the deadline. Must be called
// with p.mu held.
func (p *PowerZ) readNLocked(n int, deadline time.Time) ([]byte, error) {
	buf := make([]byte, n)
	off := 0
	for off < n {
		if p.conn == nil {
			return nil, errNotConnected
		}
		if time.Now().After(deadline) {
			return nil, errTimeout
		}
		m, err := p.conn.Read(buf[off:])
		if err != nil {
			return nil, err
		}
		off += m
	}
	return buf, nil
}

// parseADC decodes the 44-byte ADC payload.
//
// Offsets (from the protocol reference, confirmed on a KM002C):
//
//	0 Vbus (µV)            4 Ibus (µA, signed)
//	8 Vbus_avg             12 Ibus_avg
//	16 Vbus_ori_avg        20 Ibus_ori_avg
//	24 Temp (1/128 °C)     26 Vcc1 (0.1 mV)
//	28 Vcc2                30 Vdp
//	32 Vdm                 34 Vdd
//	36 sample rate index   37 flags
//	38 CC2_avg (mV)        40 D+_avg        42 D-_avg
func parseADC(p []byte) (adcSample, bool) {
	if len(p) < adcPayloadSize {
		return adcSample{}, false
	}
	vbus := int32(binary.LittleEndian.Uint32(p[0:]))
	ibus := int32(binary.LittleEndian.Uint32(p[4:]))
	temp := int16(binary.LittleEndian.Uint16(p[24:]))
	return adcSample{
		volt:  float64(vbus) / 1e6,
		curr:  float64(ibus) / 1e6,
		tempC: float64(temp) / 128.0,
	}, true
}

// markDisconnected closes the port after an unrecoverable I/O error so the
// manager's reconnect loop can reopen it.
func (p *PowerZ) markDisconnected() {
	p.mu.Lock()
	p.connected = false
	conn := p.conn
	p.conn = nil
	p.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// snapshotLocked builds a MonitorState. Must be called with p.mu held.
func (p *PowerZ) snapshotLocked() *device.MonitorState {
	return &device.MonitorState{
		Connected:   p.connected,
		Current:     p.curr,
		Voltage:     p.volt,
		Monitoring:  p.monitoring,
		AvgCurrent:  p.avgCurr,
		AvgVoltage:  p.avgVolt,
		PeakCurrent: p.peakCurr,
		PeakVoltage: p.peakVolt,
		SampleCount: p.sampleCount,
		IntervalMs:  p.intervalMs,
	}
}

// ─── PowerMonitor interface ───────────────────────────────────────────────────

func (p *PowerZ) GetState(_ context.Context) (*device.MonitorState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked(), nil
}

// SetInterval changes the host sampling interval. The POWER-Z streams on
// request rather than pushing continuously, so this controls our GetData rate.
func (p *PowerZ) SetInterval(_ context.Context, ms int) error {
	if ms < minIntervalMs {
		ms = minIntervalMs
	}
	if ms > maxIntervalMs {
		ms = maxIntervalMs
	}
	p.mu.Lock()
	p.intervalMs = ms
	p.mu.Unlock()
	// Wake the sampling loop so the new interval applies immediately.
	select {
	case p.intervalCh <- struct{}{}:
	default:
	}
	return nil
}

// SetPrint is not part of the POWER-Z protocol; accepted as a no-op so the
// shared monitor UI can call it on every meter.
func (p *PowerZ) SetPrint(_ context.Context, _ int) error { return nil }

// StartMonitor begins accumulating average/peak statistics.
func (p *PowerZ) StartMonitor(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.monitoring = true
	p.sampleCount = 0
	p.avgCurr = 0
	p.avgVolt = 0
	p.peakCurr = 0
	p.peakVolt = 0
	return nil
}

// StopMonitor stops accumulating average/peak statistics.
func (p *PowerZ) StopMonitor(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.monitoring = false
	return nil
}

func (p *PowerZ) SetSampleHandler(fn func(*device.MonitorState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sampleHandler = fn
}

// ensure PowerZ satisfies device.PowerMonitor at compile time.
var _ device.PowerMonitor = (*PowerZ)(nil)
