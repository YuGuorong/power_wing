package powerz

import (
	"context"
	"os"
	"testing"
	"time"

	"go.bug.st/serial"
)

// capturedADC is the ADC payload captured from a KM002C (firmware reporting
// ~4.26 V, 48 µA, 29.45 °C) after sending 0C 01 02 00.
var capturedADC = []byte{
	0x26, 0x04, 0x41, 0x00, // Vbus   4260902 µV
	0x30, 0x00, 0x00, 0x00, // Ibus   48 µA
	0x30, 0x05, 0x41, 0x00, // Vbus_avg
	0x73, 0x00, 0x00, 0x00, // Ibus_avg
	0xFC, 0x0A, 0x41, 0x00, // Vbus_ori_avg
	0x15, 0x00, 0x00, 0x00, // Ibus_ori_avg
	0xBA, 0x0E, // Temp 3770 / 128 = 29.45 °C
	0xA5, 0x01, // Vcc1 42.1 mV
	0x98, 0x01, // Vcc2 40.8 mV
	0x8D, 0x01, // Vdp 39.7 mV
	0x86, 0x01, // Vdm 39.0 mV
	0x17, 0x86, // Vdd 3432.7 mV
	0x00,       // sample rate index
	0x80,       // flags
	0x29, 0x00, // CC2_avg 41 mV
	0x27, 0x00, // D+_avg 39 mV
	0x26, 0x00, // D-_avg 38 mV
}

func TestParseADC(t *testing.T) {
	s, ok := parseADC(capturedADC)
	if !ok {
		t.Fatal("parseADC rejected a valid payload")
	}
	if want := 4.260902; s.volt != want {
		t.Errorf("volt = %v, want %v", s.volt, want)
	}
	if want := 0.000048; s.curr != want {
		t.Errorf("curr = %v, want %v", s.curr, want)
	}
	if want := 29.453125; s.tempC != want {
		t.Errorf("temp = %v, want %v", s.tempC, want)
	}
	if _, ok := parseADC(capturedADC[:20]); ok {
		t.Error("parseADC accepted a short payload")
	}
}

// TestReadADCResponse verifies framing: a stray Accept reply before PutData is
// skipped, the extended header's size field delimits the payload, and the ADC
// attribute selects the right logical packet.
func TestReadADCResponse(t *testing.T) {
	response := []byte{
		0x05, 0x01, 0x00, 0x00, // Accept for Connect (no payload)
		0x41, 0x02, 0x80, 0x02, // PutData, tid=2, object count (ignored)
		0x01, 0x00, 0x00, 0x0B, // ext: attr=ADC, next=0, size=44
	}
	response = append(response, capturedADC...)

	p := New("test", "", "FAKE", 0)
	p.conn = &fakePort{rx: response}
	p.connected = true

	s, err := p.readADCLocked(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("readADCLocked: %v", err)
	}
	if want := 4.260902; s.volt != want {
		t.Errorf("volt = %v, want %v", s.volt, want)
	}
}

func TestReadADCTimeout(t *testing.T) {
	p := New("test", "", "FAKE", 0)
	p.conn = &fakePort{} // no response
	p.connected = true
	if _, err := p.readADCLocked(time.Now().Add(20 * time.Millisecond)); err != errTimeout {
		t.Fatalf("err = %v, want errTimeout", err)
	}
}

// TestLive polls a real device. Set POWERZ_PORT to its CDC COM port, e.g.
//
//	POWERZ_PORT=COM65 go test ./internal/device/powerz -run TestLive -v
func TestLive(t *testing.T) {
	port := os.Getenv("POWERZ_PORT")
	if port == "" {
		t.Skip("set POWERZ_PORT to run against hardware")
	}
	p := New("live", "POWER-Z", port, 0)
	if err := p.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer p.Disconnect()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := p.GetState(context.Background())
		if st.Voltage != 0 || st.Current != 0 {
			t.Logf("voltage=%.6f V current=%.6f A sample_count=%d",
				st.Voltage, st.Current, st.SampleCount)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no sample received")
}

// fakePort implements serial.Port from a fixed rx buffer.
type fakePort struct {
	rx []byte
}

func (f *fakePort) SetMode(*serial.Mode) error         { return nil }
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) SetDTR(bool) error                  { return nil }
func (f *fakePort) SetRTS(bool) error                  { return nil }
func (f *fakePort) ResetInputBuffer() error            { return nil }
func (f *fakePort) ResetOutputBuffer() error           { return nil }
func (f *fakePort) Close() error                       { return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}

func (f *fakePort) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakePort) Drain() error                { return nil }

func (f *fakePort) Read(p []byte) (int, error) {
	if len(f.rx) == 0 {
		return 0, nil // mirrors a real port's read timeout
	}
	n := copy(p, f.rx)
	f.rx = f.rx[n:]
	return n, nil
}
