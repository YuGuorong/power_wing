package sangenxian

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestCRC8(t *testing.T) {
	cases := []struct {
		in   []byte
		want byte
	}{
		{[]byte{0x7E, 0x80}, 0xFD},
		{[]byte{0x7E, 0x81}, 0xFA},
		{[]byte{0x7E, 0x82}, 0xF3},
		{[]byte{0x7E, 0x84}, 0xE1},
		{[]byte{0x7E, 0x85}, 0xE6},
		{[]byte{0x7E, 0x00, 0xE7}, 0xF0},
	}
	for _, c := range cases {
		if got := crc8(c.in); got != c.want {
			t.Errorf("crc8(% X) = %02X, want %02X", c.in, got, c.want)
		}
	}
}

func TestFrames(t *testing.T) {
	if got := readFrame(0x00); !bytes.Equal(got, []byte{0x7E, 0x80, 0xFD}) {
		t.Errorf("readFrame(P0) = % X", got)
	}
	if got := readFrame(0x05); !bytes.Equal(got, []byte{0x7E, 0x85, 0xE6}) {
		t.Errorf("readFrame(ID) = % X", got)
	}
	if got := writeFrame(0x00, 0xE7); !bytes.Equal(got, []byte{0x7E, 0x00, 0xE7, 0xF0}) {
		t.Errorf("writeFrame(P0, 0xE7) = % X", got)
	}
}

func TestParseReply(t *testing.T) {
	cases := []struct {
		name   string
		reqCRC byte
		in     []byte
		want   []byte
	}{
		{"payload+crc echo", 0xFD, []byte{0xE7, 0xFD}, []byte{0xE7}},
		{"7e+reg prefix", 0xFD, []byte{0x7E, 0x00, 0xE7, 0xFD}, []byte{0xE7}},
		{"payload only", 0xFD, []byte{0xE7}, []byte{0xE7}},
		{"single byte equals crc", 0xE7, []byte{0xE7}, []byte{0xE7}},
	}
	for _, c := range cases {
		got, err := parseReply(c.reqCRC, c.in)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: got % X, want % X", c.name, got, c.want)
		}
	}
	if _, err := parseReply(0xFD, nil); err == nil {
		t.Error("parseReply(nil) should fail")
	}
}

func TestPortMap(t *testing.T) {
	want := [12]struct {
		reg int
		bit uint
	}{
		{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5}, {0, 6}, {0, 7},
		{1, 0}, {1, 1}, {1, 6}, {1, 7},
	}
	for i, w := range want {
		if portMap[i] != w {
			t.Errorf("port %d maps to %+v, want %+v", i+1, portMap[i], w)
		}
	}
}

func TestDecodePorts(t *testing.T) {
	s := &SangenXian{}
	// P0 = 0xE7 -> ports 4,5 ON; P1 = 0x7F -> port 12 ON.
	s.wire = [4]byte{0xE7, 0x7F, 0xFF, 0xFF}
	s.refreshPortsLocked()
	want := [12]bool{false, false, false, true, true, false, false, false, false, false, false, true}
	if s.ports != want {
		t.Errorf("ports = %v, want %v", s.ports, want)
	}
}

// TestHardwareReadOnly connects and dumps the decoded registers without
// writing anything.
func TestHardwareReadOnly(t *testing.T) {
	port := os.Getenv("SANGENXIAN_PORT")
	if port == "" {
		t.Skip("set SANGENXIAN_PORT to run against real hardware")
	}
	d := New("hub1", "SanGenXian", port, 0)
	if err := d.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer d.Disconnect()
	t.Logf("wire after connect: % X", d.wire)
	st, err := d.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	t.Logf("ports: %v (port_count=%d)", st.Ports, st.PortCount)
	t.Logf("wire after GetState: % X", d.wire)
}

// TestHardware is a manual integration test.  Set SANGENXIAN_PORT (e.g. COM56)
// to run it; it toggles port 2 and restores the original state.
func TestHardware(t *testing.T) {
	port := os.Getenv("SANGENXIAN_PORT")
	if port == "" {
		t.Skip("set SANGENXIAN_PORT to run against real hardware")
	}
	d := New("hub1", "SanGenXian", port, 0)
	if err := d.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer d.Disconnect()

	ctx := context.Background()
	st, err := d.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !st.Connected || st.PortCount != 12 || len(st.Ports) != 12 {
		t.Fatalf("unexpected state: %+v", st)
	}
	t.Logf("initial ports: %v", st.Ports)

	original := st.Ports[1]
	if err := d.SetPort(ctx, 2, !original); err != nil {
		t.Fatalf("SetPort(2, %v): %v", !original, err)
	}
	st, err = d.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState after set: %v", err)
	}
	if st.Ports[1] != !original {
		t.Errorf("port 2 = %v, want %v", st.Ports[1], !original)
	}
	t.Logf("after toggling port 2: %v", st.Ports)

	if err := d.SetPort(ctx, 2, original); err != nil {
		t.Fatalf("restore SetPort(2, %v): %v", original, err)
	}
	st, err = d.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState after restore: %v", err)
	}
	if st.Ports[1] != original {
		t.Errorf("port 2 not restored: %v, want %v", st.Ports[1], original)
	}
}

// TestHardwareToggleLoop stresses repeated port writes to catch missed frames.
func TestHardwareToggleLoop(t *testing.T) {
	port := os.Getenv("SANGENXIAN_PORT")
	if port == "" {
		t.Skip("set SANGENXIAN_PORT to run against real hardware")
	}
	d := New("hub1", "SanGenXian", port, 0)
	if err := d.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer d.Disconnect()

	ctx := context.Background()
	st, err := d.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	original := st.Ports[1]
	defer func() { _ = d.SetPort(ctx, 2, original) }()

	for i := 0; i < 10; i++ {
		want := original != (i%2 == 0)
		if err := d.SetPort(ctx, 2, want); err != nil {
			t.Fatalf("iteration %d: SetPort(2, %v): %v", i, want, err)
		}
		st, err := d.GetState(ctx)
		if err != nil {
			t.Fatalf("iteration %d: GetState: %v", i, err)
		}
		if st.Ports[1] != want {
			t.Fatalf("iteration %d: port 2 = %v, want %v", i, st.Ports[1], want)
		}
	}
}
