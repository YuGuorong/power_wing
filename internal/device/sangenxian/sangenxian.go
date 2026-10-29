// Package sangenxian implements device.USBHub for the SanGenXian 12-port USB
// hub controller.
//
// Protocol (4800-8-N-1), reverse-engineered from the vendor SDK's usbctrl.cpp:
//
//	Query:  7E (0x80|REG) CRC8
//	Reply:  <payload> CRC8(request)      // status payload = 1 inverted byte
//	Write:  7E REG ~value CRC8
//
// CRC8 uses polynomial 0x07 with an initial value of 0, covering every byte
// that precedes it.
//
// Registers: P0=0x00, P1=0x01, P2=0x02, P4=0x04.  Register bits are active-low
// on the wire: a 0 bit means the port is ON.  Ports 1-8 map to P0 bits 0-7;
// ports 9-12 map to P1 bits 0, 1, 6 and 7.  The vendor DLL always writes the
// four registers P0/P1/P2/P4 (20 ms apart) so that the remaining P2/P4 bits,
// which belong to ports 13-30 on larger boards, are preserved.
package sangenxian

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/serialport"
)

const (
	defaultBaud = 4800
	// readTimeout doubles as the inter-byte gap: a Read returns available
	// bytes immediately, and the next Read blocks this long when the reply is
	// complete.  40 ms is comfortably above the ~4 ms it takes to shift a
	// 2-byte reply at 4800 baud.
	readTimeout    = 40 * time.Millisecond
	writeGap       = 20 * time.Millisecond
	deviceTypeName = "sangenxian"
	portCount      = 12
)

// Register addresses, in the order used by the wire protocol.
var registers = [4]byte{0x00, 0x01, 0x02, 0x04}

// portMap maps a 1-based port to its register index and bit position.
var portMap = [portCount]struct {
	reg int
	bit uint
}{
	{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5}, {0, 6}, {0, 7}, // ports 1-8
	{1, 0}, {1, 1}, {1, 6}, {1, 7}, // ports 9-12
}

// SangenXian drives a 12-port SanGenXian USB hub over its UART interface.
type SangenXian struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serialport.Port
	connected bool

	// wire holds the raw, active-low register bytes as read from the device.
	wire [4]byte
	// ports is the decoded 1-based port power state (true = ON).
	ports [portCount]bool

	warned bool // one-shot log when a status refresh fails
}

// New creates a new SanGenXian driver.  baud=0 uses 4800.
func New(id, name, port string, baud int) *SangenXian {
	if baud == 0 {
		baud = defaultBaud
	}
	return &SangenXian{
		id:   id,
		name: name,
		port: port,
		baud: baud,
		// Active-low registers: assume every port is OFF until read.
		wire: [4]byte{0xFF, 0xFF, 0xFF, 0xFF},
	}
}

func (s *SangenXian) ID() string   { return s.id }
func (s *SangenXian) Name() string { return s.name }
func (s *SangenXian) Type() string { return deviceTypeName }

func (s *SangenXian) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *SangenXian) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connected {
		return nil
	}
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	p, err := serialport.OpenFor(s.id, s.port, serialport.Mode(s.baud))
	if err != nil {
		return fmt.Errorf("sangenxian %s: connect: %w", s.id, err)
	}
	if err := p.SetReadTimeout(readTimeout); err != nil {
		p.Close()
		return fmt.Errorf("sangenxian %s: set timeout: %w", s.id, err)
	}
	// The vendor SDK enables both handshake lines when opening the port.
	_ = p.SetDTR(true)
	_ = p.SetRTS(true)
	_ = p.ResetInputBuffer()
	_ = p.ResetOutputBuffer()

	s.conn = p
	if err := s.probeLocked(); err != nil {
		p.Close()
		s.conn = nil
		return fmt.Errorf("sangenxian %s: probe: %w", s.id, err)
	}
	s.connected = true
	s.warned = false
	return nil
}

func (s *SangenXian) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected && s.conn == nil {
		return nil
	}
	s.connected = false
	var err error
	if s.conn != nil {
		err = s.conn.Close()
		s.conn = nil
	}
	return err
}

// ─── wire helpers ─────────────────────────────────────────────────────────────

// crc8 implements the vendor's CRC-8: polynomial 0x07, initial value 0.
func crc8(data []byte) byte {
	var crc byte
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 0x07
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func readFrame(reg byte) []byte {
	f := []byte{0x7E, 0x80 | reg, 0}
	f[2] = crc8(f[:2])
	return f
}

func writeFrame(reg, value byte) []byte {
	f := []byte{0x7E, reg, value, 0}
	f[3] = crc8(f[:3])
	return f
}

// parseReply strips the CRC echo and an optional 7E/register prefix from a
// device reply.  The firmware used by the 12-port board answers a query with
// "<payload> <CRC of request>"; larger firmware variants prepend "7E REG".
func parseReply(reqCRC byte, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("no response")
	}
	// A one-byte reply is payload-only; never mistake it for a CRC echo.
	if len(data) >= 2 && data[len(data)-1] == reqCRC {
		data = data[:len(data)-1]
	}
	if len(data) >= 2 && data[0] == 0x7E {
		data = data[2:]
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	return data, nil
}

// readReply accumulates bytes until the line goes idle for readTimeout.
// go.bug.st/serial returns as soon as any byte is available, so a single
// Read can yield a partial reply at 4800 baud.
func (s *SangenXian) readReply() []byte {
	var out []byte
	buf := make([]byte, 64)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil || n == 0 {
			return out
		}
	}
}

// queryRegisterLocked sends a read frame and returns the register byte.
// Must be called with s.mu held and s.conn open.
func (s *SangenXian) queryRegisterLocked(reg byte) (byte, error) {
	// Drop any bytes left over from a previous reply/echo.
	_ = s.conn.ResetInputBuffer()
	req := readFrame(reg)
	if _, err := s.conn.Write(req); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	payload, err := parseReply(req[2], s.readReply())
	if err != nil {
		return 0, err
	}
	return payload[len(payload)-1], nil
}

// probeLocked validates the device and seeds the register cache.  P0 and P1
// must answer (they control ports 1-12); P2/P4 are optional and default to
// 0xFF (all off) when the firmware does not expose them.
// Must be called with s.mu held and s.conn open.
func (s *SangenXian) probeLocked() error {
	s.wire = [4]byte{0xFF, 0xFF, 0xFF, 0xFF}
	p0, err := s.queryRegisterLocked(registers[0])
	if err != nil {
		return fmt.Errorf("P0: %w", err)
	}
	p1, err := s.queryRegisterLocked(registers[1])
	if err != nil {
		return fmt.Errorf("P1: %w", err)
	}
	s.wire[0], s.wire[1] = p0, p1
	if v, err := s.queryRegisterLocked(registers[2]); err == nil {
		s.wire[2] = v
	}
	if v, err := s.queryRegisterLocked(registers[3]); err == nil {
		s.wire[3] = v
	}
	s.refreshPortsLocked()
	return nil
}

// queryRegsLocked reads the given register indexes and refreshes the port
// cache.  A partial response is tolerated as long as at least one register
// answered.  Must be called with s.mu held and s.conn open.
func (s *SangenXian) queryRegsLocked(indexes ...int) error {
	ok := 0
	var firstErr error
	for _, i := range indexes {
		v, err := s.queryRegisterLocked(registers[i])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.wire[i] = v
		ok++
	}
	if ok == 0 {
		return firstErr
	}
	s.refreshPortsLocked()
	return nil
}

// refreshPortsLocked decodes the active-low register bytes into port states.
func (s *SangenXian) refreshPortsLocked() {
	for i, m := range portMap {
		s.ports[i] = s.wire[m.reg]&(1<<m.bit) == 0
	}
}

// writeAllLocked sends all four register frames exactly like the vendor DLL
// and drains the per-frame acknowledgement echoes.
// Must be called with s.mu held and s.conn open.
func (s *SangenXian) writeAllLocked() error {
	for i, reg := range registers {
		frame := writeFrame(reg, s.wire[i])
		n, err := s.conn.Write(frame)
		if err != nil {
			return fmt.Errorf("write register 0x%02X: %w", reg, err)
		}
		if n != len(frame) {
			return fmt.Errorf("write register 0x%02X: short write (%d/%d)", reg, n, len(frame))
		}
		time.Sleep(writeGap)
	}
	// Each frame is echoed back as "<value> <CRC>"; a missing echo is not a
	// protocol error, so a timeout is ignored.  Flush afterwards so a late
	// echo cannot be mistaken for the reply to the next query.
	_ = s.readReply()
	_ = s.conn.ResetInputBuffer()
	return nil
}

// verifyLocked re-reads P0/P1 and compares them against the intended register
// values.  On mismatch the intended values are restored so a retry can resend
// the complete state.
func (s *SangenXian) verifyLocked() error {
	want := s.wire
	if err := s.queryRegsLocked(0, 1); err != nil {
		return err
	}
	if s.wire[0] != want[0] || s.wire[1] != want[1] {
		got := s.wire
		s.wire = want
		return fmt.Errorf("readback mismatch: P0 %02X (want %02X), P1 %02X (want %02X)",
			got[0], want[0], got[1], want[1])
	}
	return nil
}

// ─── USBHub interface ─────────────────────────────────────────────────────────

func (s *SangenXian) GetState(_ context.Context) (*device.HubState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected && s.conn != nil {
		// Only P0/P1 drive ports 1-12; P2/P4 (ports 13-30) are read once at
		// connect time and preserved on every write.
		if err := s.queryRegsLocked(0, 1); err != nil {
			if !s.warned {
				log.Printf("[sangenxian] %s: status refresh failed: %v", s.id, err)
				s.warned = true
			}
		} else {
			s.warned = false
		}
	}

	ports := make([]bool, portCount)
	copy(ports, s.ports[:])
	return &device.HubState{
		Connected: s.connected,
		Ports:     ports,
		PortCount: portCount,
	}, nil
}

func (s *SangenXian) SetPort(_ context.Context, port int, on bool) error {
	if port < 1 || port > portCount {
		return fmt.Errorf("sangenxian: invalid port %d (1-%d)", port, portCount)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.conn == nil {
		return fmt.Errorf("sangenxian %s: not connected", s.id)
	}

	m := portMap[port-1]
	if on {
		s.wire[m.reg] &^= 1 << m.bit // active-low: clear = ON
	} else {
		s.wire[m.reg] |= 1 << m.bit // set = OFF
	}
	if err := s.applyLocked(); err != nil {
		return fmt.Errorf("sangenxian %s: port %d not confirmed: %w", s.id, port, err)
	}
	s.ports[port-1] = on
	return nil
}

// SetAllPorts switches every port 1-12 ON or OFF in one register update.
func (s *SangenXian) SetAllPorts(_ context.Context, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.conn == nil {
		return fmt.Errorf("sangenxian %s: not connected", s.id)
	}
	for _, m := range portMap {
		if on {
			s.wire[m.reg] &^= 1 << m.bit
		} else {
			s.wire[m.reg] |= 1 << m.bit
		}
	}
	if err := s.applyLocked(); err != nil {
		return fmt.Errorf("sangenxian %s: all ports not confirmed: %w", s.id, err)
	}
	for i := range s.ports {
		s.ports[i] = on
	}
	return nil
}

// applyLocked writes all four registers, verifies, and retries once.  A
// 4800-baud controller can occasionally miss an update while the previous
// command is still being processed.
func (s *SangenXian) applyLocked() error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = s.writeAllLocked(); err != nil {
			return err
		}
		if err = s.verifyLocked(); err == nil {
			return nil
		}
	}
	return err
}

// SetLock is not supported by this controller.
func (s *SangenXian) SetLock(context.Context, bool) error {
	return fmt.Errorf("sangenxian: hardware lock is not supported")
}

// SetHWKeys is not supported by this controller.
func (s *SangenXian) SetHWKeys(context.Context, bool) error {
	return fmt.Errorf("sangenxian: hardware keys are not supported")
}

// SetAutoSave is not supported by this controller.
func (s *SangenXian) SetAutoSave(context.Context, bool) error {
	return fmt.Errorf("sangenxian: auto-save is not supported")
}

// SetDefaultOn is not supported by this controller.
func (s *SangenXian) SetDefaultOn(context.Context, byte) error {
	return fmt.Errorf("sangenxian: default-on mask is not supported")
}

// ensure SangenXian satisfies device.USBHub at compile time.
var _ device.USBHub = (*SangenXian)(nil)
