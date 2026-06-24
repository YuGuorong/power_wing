// Package serialport provides a cross-platform abstraction over serial port
// enumeration and configuration.  It wraps go.bug.st/serial so that all port
// listing and opening is done through a single import point.
package serialport

import (
	"fmt"

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
func Open(port string, mode *serial.Mode) (serial.Port, error) {
	p, err := serial.Open(port, mode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	return p, nil
}
