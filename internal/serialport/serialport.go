// Package serialport provides a cross-platform abstraction over serial port
// enumeration and configuration.  It wraps go.bug.st/serial so that all port
// listing and opening is done through a single import point.
package serialport

import (
	"fmt"
	"strings"
	"sync"

	"go.bug.st/serial"
)

// Port is the cross-platform serial port interface used by device drivers.
type Port = serial.Port

// List returns the names of all available serial ports on the host.
// On Windows these are "COM3", "COM4", etc.; on Linux "/dev/ttyUSB0",
// "/dev/ttyACM0", etc.
func List() ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("serial port enumeration: %w", err)
	}
	return ports, nil
}

// Mode returns a serial.Mode pre-configured for 8N1 at the given baud rate.
func Mode(baud int) *serial.Mode {
	return &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
}

// Open opens a serial port with the given mode.  The port name is
// platform-specific (COMn on Windows, /dev/ttyXXX on Linux).
//
// Every open/close is recorded in the in-memory serial log (see Log) so the
// Web UI can show which device currently owns which port.
func Open(port string, mode *serial.Mode) (serial.Port, error) {
	return OpenFor("", port, mode)
}

// OpenFor is Open with an owner label (normally the device ID or a probe name)
// that is included in the serial log.
func OpenFor(owner, port string, mode *serial.Mode) (serial.Port, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("open serial port: empty port name")
	}
	p, err := serial.Open(port, mode)
	if err != nil {
		addEvent(Event{
			Action: "open_failed",
			Owner:  owner,
			Port:   port,
			Baud:   baudOf(mode),
			Caller: callerAt(2),
			Error:  err.Error(),
		})
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	addEvent(Event{
		Action: "open",
		Owner:  owner,
		Port:   port,
		Baud:   baudOf(mode),
		Caller: callerAt(2),
	})
	return &loggedPort{Port: p, owner: owner, port: port, baud: baudOf(mode)}, nil
}

func baudOf(mode *serial.Mode) int {
	if mode == nil {
		return 0
	}
	return mode.BaudRate
}

// loggedPort forwards everything to the underlying port and records Close.
type loggedPort struct {
	serial.Port
	owner     string
	port      string
	baud      int
	closeOnce sync.Once
}

func (p *loggedPort) Close() error {
	// Capture the caller before entering sync.Once; inside the closure the
	// stack would point at sync.Once.Do instead of the code closing the port.
	caller := callerAt(2)
	err := p.Port.Close()
	p.closeOnce.Do(func() {
		ev := Event{
			Action: "close",
			Owner:  p.owner,
			Port:   p.port,
			Baud:   p.baud,
			Caller: caller,
		}
		if err != nil {
			ev.Error = err.Error()
		}
		addEvent(ev)
	})
	return err
}
