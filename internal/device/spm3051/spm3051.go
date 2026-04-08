// Package spm3051 implements device.PowerSupply for the SPM3051 UART lab PSU.
//
// Protocol summary (SCPI-like, all commands terminated with \r\n):
//
//	OUTP ON / OUTP OFF          – enable / disable output
//	VOLT <v>                    – set output voltage (V)
//	CURR <a>                    – set output current limit (A)
//	VOLT:LIM <v>                – set OVP trip voltage
//	CURR:LIM <a>                – set OCP current threshold
//	MEASure:ALL:INFO?\n         – query: "V,A,W,OUT,OVP,OCP,x\r\n"
package spm3051

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
	defaultBaud    = 9600
	readTimeout    = 2 * time.Second
	deviceTypeName = "spm3051"
)

// SPM3051 drives a SPM3051 power supply over a UART/USB-CDC port.
type SPM3051 struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serial.Port
	connected bool

	// cached set-points (device has no query command for these)
	voltSet float64
	currSet float64
	ovpLim  float64
	ocpLim  float64
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
	mode := &serial.Mode{
		BaudRate: s.baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	p, err := serial.Open(s.port, mode)
	if err != nil {
		return fmt.Errorf("spm3051 %s: connect: %w", s.id, err)
	}
	if err := p.SetReadTimeout(readTimeout); err != nil {
		p.Close()
		return fmt.Errorf("spm3051 %s: set timeout: %w", s.id, err)
	}
	s.conn = p
	s.connected = true
	return nil
}

func (s *SPM3051) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return nil
	}
	s.connected = false
	return s.conn.Close()
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func (s *SPM3051) write(data string) error {
	_, err := s.conn.Write([]byte(data))
	return err
}

// readLine reads bytes until '\n' or timeout, strips whitespace.
func (s *SPM3051) readLine() (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for len(buf) < 512 {
		n, err := s.conn.Read(b)
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

// query sends cmd (no terminator needed) and returns the response line.
func (s *SPM3051) query(cmd string) (string, error) {
	if err := s.write(cmd + "\n"); err != nil {
		return "", err
	}
	return s.readLine()
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
		s.connected = false
		return nil, fmt.Errorf("GetState: %w", err)
	}
	// Expected: "V,A,W,OUT,OVP,OCP,x"
	parts := strings.Split(resp, ",")
	if len(parts) < 6 {
		return nil, fmt.Errorf("GetState: unexpected response: %q", resp)
	}
	f := func(i int) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
		return v
	}
	b := func(i int) bool { return strings.EqualFold(strings.TrimSpace(parts[i]), "ON") }

	return &device.PowerState{
		Connected:    true,
		VoltMeas:     f(0),
		CurrMeas:     f(1),
		PowerMeas:    f(2),
		OutputOn:     b(3),
		OVPTriggered: b(4),
		OCPTriggered: b(5),
		// local cache
		VoltSet:  s.voltSet,
		CurrSet:  s.currSet,
		OVPLimit: s.ovpLim,
		OCPLimit: s.ocpLim,
		CVMode:   f(1) < s.currSet*0.99,
	}, nil
}

func (s *SPM3051) SetVoltage(_ context.Context, volts float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.write(fmt.Sprintf("VOLT %.3f\r\n", volts)); err != nil {
		return err
	}
	s.voltSet = volts
	return nil
}

func (s *SPM3051) SetCurrent(_ context.Context, amps float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.write(fmt.Sprintf("CURR %.3f\r\n", amps)); err != nil {
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
	cmd := "OUTP OFF\r\n"
	if on {
		cmd = "OUTP ON\r\n"
	}
	return s.write(cmd)
}

func (s *SPM3051) SetOVP(_ context.Context, volts float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.write(fmt.Sprintf("VOLT:LIM %.3f\r\n", volts)); err != nil {
		return err
	}
	s.ovpLim = volts
	return nil
}

func (s *SPM3051) SetOCP(_ context.Context, amps float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return fmt.Errorf("spm3051 %s: not connected", s.id)
	}
	if err := s.write(fmt.Sprintf("CURR:LIM %.3f\r\n", amps)); err != nil {
		return err
	}
	s.ocpLim = amps
	return nil
}
