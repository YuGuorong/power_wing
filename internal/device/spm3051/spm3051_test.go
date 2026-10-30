package spm3051

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"go.bug.st/serial"
)

// fakePort implements serial.Port for driver-level tests.  Read serves rx once
// and then reports a timeout (0, nil), like go.bug.st/serial.
type fakePort struct {
	mu     sync.Mutex
	rx     []byte
	rxPos  int
	writes []string
	drains int
	closes int
}

func (f *fakePort) SetMode(*serial.Mode) error { return nil }

func (f *fakePort) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rxPos >= len(f.rx) {
		return 0, nil
	}
	n := copy(p, f.rx[f.rxPos:])
	f.rxPos += n
	return n, nil
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, string(p))
	return len(p), nil
}

func (f *fakePort) Drain() error             { f.drains++; return nil }
func (f *fakePort) ResetInputBuffer() error  { return nil }
func (f *fakePort) ResetOutputBuffer() error { return nil }
func (f *fakePort) SetDTR(bool) error        { return nil }
func (f *fakePort) SetRTS(bool) error        { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) Close() error                       { f.closes++; return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }

func (f *fakePort) written() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

func TestParseMeasureCapture(t *testing.T) {
	m, err := parseMeasure("5.010,0.000,0.000,OFF,OFF,OFF,1")
	if err != nil {
		t.Fatalf("parseMeasure: %v", err)
	}
	if m.volt != 5.010 || m.curr != 0 || m.power != 0 {
		t.Fatalf("measurements = %+v", m)
	}
	if m.ovp || m.ocp || m.otp {
		t.Fatalf("unexpected protection flags: %+v", m)
	}
	if !m.hasMode || m.mode != modeCV {
		t.Fatalf("mode = %d (has=%v), want CV", m.mode, m.hasMode)
	}
}

func TestParseMeasureFlagsAndCC(t *testing.T) {
	m, err := parseMeasure("12.000,4.900,58.800,ON,OFF,ON,2")
	if err != nil {
		t.Fatalf("parseMeasure: %v", err)
	}
	if !m.ovp || m.ocp || !m.otp {
		t.Fatalf("flags = ovp:%v ocp:%v otp:%v, want true/false/true", m.ovp, m.ocp, m.otp)
	}
	if !m.hasMode || m.mode != modeCC {
		t.Fatalf("mode = %d, want CC", m.mode)
	}
}

func TestParseMeasureSpaceSeparated(t *testing.T) {
	// The manual's generic example uses spaces; the parser should accept it.
	m, err := parseMeasure("2.000 5.000 10.000 1 0 0 2")
	if err != nil {
		t.Fatalf("parseMeasure: %v", err)
	}
	if m.volt != 2 || m.curr != 5 || m.power != 10 {
		t.Fatalf("measurements = %+v", m)
	}
	if !m.ovp || m.ocp || m.otp {
		t.Fatalf("flags = %+v", m)
	}
	if m.mode != modeCC {
		t.Fatalf("mode = %d, want CC", m.mode)
	}
}

func TestParseMeasureWithoutMode(t *testing.T) {
	m, err := parseMeasure("1.000,2.000,2.000,OFF,OFF,OFF")
	if err != nil {
		t.Fatalf("parseMeasure: %v", err)
	}
	if m.hasMode {
		t.Fatalf("hasMode = true for a 6-field reply")
	}
}

func TestParseMeasureRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "ON", "5.0", "5.0,abc,1.0", "5.0,1.0,x"} {
		if _, err := parseMeasure(in); err == nil {
			t.Fatalf("parseMeasure(%q) did not fail", in)
		}
	}
}

func TestParseBool(t *testing.T) {
	for _, in := range []string{"ON", "on", "1", "TRUE"} {
		if v, ok := parseBool(in); !ok || !v {
			t.Errorf("parseBool(%q) = %v,%v; want true,true", in, v, ok)
		}
	}
	for _, in := range []string{"OFF", "off", "0", "FALSE"} {
		if v, ok := parseBool(in); !ok || v {
			t.Errorf("parseBool(%q) = %v,%v; want false,true", in, v, ok)
		}
	}
	if _, ok := parseBool("garbage"); ok {
		t.Error("parseBool(garbage) reported ok")
	}
}

func TestReadLineKeepsLeftoverBytes(t *testing.T) {
	f := &fakePort{rx: []byte("5.010,0.000,0.000,OFF,OFF,OFF,1\r\nON\r\n")}
	s := &SPM3051{conn: f}

	first, err := s.readLine()
	if err != nil {
		t.Fatalf("first readLine: %v", err)
	}
	if first != "5.010,0.000,0.000,OFF,OFF,OFF,1" {
		t.Fatalf("first line = %q", first)
	}
	second, err := s.readLine()
	if err != nil {
		t.Fatalf("second readLine: %v", err)
	}
	if second != "ON" {
		t.Fatalf("second line = %q", second)
	}
}

func TestReadLineReportsIncompleteReply(t *testing.T) {
	f := &fakePort{rx: []byte("5.010,0.")}
	s := &SPM3051{conn: f}
	if _, err := s.readLine(); err == nil {
		t.Fatal("readLine accepted a reply without a terminator")
	}
	if len(s.rxBuf) != 0 {
		t.Fatalf("rxBuf not cleared after timeout: %q", s.rxBuf)
	}
}

func newTestDevice(f *fakePort) *SPM3051 {
	s := New("psu1", "Lab PSU", "COM9", 0)
	s.conn = f
	s.connected = true
	return s
}

func TestInitReadsThenReleasesToLocal(t *testing.T) {
	// Replies in the order initLocked asks for them (see SPM3051-CMD.txt
	// BOOT snapshot).  A previous session pretends to have left the panel
	// locked, which init must clear.
	f := &fakePort{rx: []byte(
		"OWON,SPM3051,24460467,FV:V2.0.0\r\n" + // *IDN?
			"5.400\r\n" + // VOLT?
			"4.900\r\n" + // CURR?
			"9.000\r\n" + // VOLT:LIM?
			"5.000\r\n" + // CURR:LIM?
			"ON\r\n" + // OUTP?
			"VOLT:DC,+0.0006V,AUTO,2V\r\n" + // :CONFigure:ALL?
			"OFF\r\n" + // MULTimeter:HOLD?
			"OFF\r\n", // :VOLT:DC:NULL?
	)}
	s := newTestDevice(f)
	s.remoteMode = true

	if err := s.initLocked(); err != nil {
		t.Fatalf("initLocked: %v", err)
	}
	want := []string{
		"*IDN?\n", "VOLT?\n", "CURR?\n", "VOLT:LIM?\n", "CURR:LIM?\n", "OUTP?\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
		"SYSTem:LOCal\r\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	if s.remoteMode {
		t.Fatal("panel must be local after the initial reads")
	}
	if s.voltSet != 5.4 || s.currSet != 4.9 || s.ovpLim != 9 || s.ocpLim != 5 {
		t.Fatalf("cached set-points = %v/%v/%v/%v", s.voltSet, s.currSet, s.ovpLim, s.ocpLim)
	}
	if !s.outputOn {
		t.Fatal("outputOn = false, OUTP? replied ON")
	}
	if !s.hasMeter || s.meter == nil {
		t.Fatal("multimeter not detected")
	}
	if s.meter.Value != 0.0006 || s.meter.Unit != "V" || !s.meter.AutoRange || s.meter.Range != "2V" {
		t.Fatalf("meter = %+v", s.meter)
	}
}

func TestParseMeterReply(t *testing.T) {
	m, err := parseMeterReply("VOLT:DC,+0.0006V,AUTO,2V")
	if err != nil {
		t.Fatalf("parseMeterReply: %v", err)
	}
	if m.Func != "VOLT:DC" || m.Value != 0.0006 || m.Unit != "V" || !m.AutoRange || m.Range != "2V" {
		t.Fatalf("meter = %+v", m)
	}

	m, err = parseMeterReply("CURR:DC,-1.2345A,MAN,10A")
	if err != nil {
		t.Fatalf("parseMeterReply: %v", err)
	}
	if m.Func != "CURR:DC" || m.Value != -1.2345 || m.Unit != "A" || m.AutoRange || m.Range != "10A" {
		t.Fatalf("meter = %+v", m)
	}

	// Over-range readings still identify the meter.
	m, err = parseMeterReply("VOLT:DC,OL,AUTO,2V")
	if err != nil {
		t.Fatalf("parseMeterReply OL: %v", err)
	}
	if !m.Overload || m.Unit != "V" {
		t.Fatalf("meter = %+v", m)
	}

	for _, in := range []string{"", "OFF", "garbage,1,2"} {
		if _, err := parseMeterReply(in); err == nil {
			t.Fatalf("parseMeterReply(%q) did not fail", in)
		}
	}
}

func TestGetStateIncludesMeter(t *testing.T) {
	f := &fakePort{rx: []byte(
		"5.010,0.000,0.000,OFF,OFF,OFF,1\r\n" + // MEASure:ALL:INFO?
			"ON\r\n" + // OUTP?
			"VOLT:DC,+0.0006V,AUTO,2V\r\n" + // :CONFigure:ALL?
			"OFF\r\n" + // MULTimeter:HOLD?
			"OFF\r\n", // :VOLT:DC:NULL?
	)}
	s := newTestDevice(f)
	s.hasMeter = true

	st, err := s.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.Meter == nil {
		t.Fatal("state has no meter reading")
	}
	if st.Meter.Value != 0.0006 || st.Meter.Unit != "V" {
		t.Fatalf("meter = %+v", st.Meter)
	}
}

func newMeterDevice(f *fakePort) *SPM3051 {
	s := newTestDevice(f)
	s.hasMeter = true
	s.meter = &device.MeterState{Func: "VOLT:DC"}
	return s
}

func TestSetMeterHold(t *testing.T) {
	f := &fakePort{rx: []byte(
		"VOLT:DC,+0.0006V,AUTO,2V\r\n" + // :CONFigure:ALL?
			"ON\r\n" + // MULTimeter:HOLD?
			"OFF\r\n", // :VOLT:DC:NULL?
	)}
	s := newMeterDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetMeterHold(context.Background(), true); err != nil {
		t.Fatalf("SetMeterHold: %v", err)
	}
	want := []string{
		"SYSTem:REMote\r\n",
		"MULTimeter:HOLD ON\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	if s.meter == nil || !s.meter.Hold {
		t.Fatalf("meter = %+v, want hold", s.meter)
	}
}

func TestSetMeterFunctionFallback(t *testing.T) {
	f := &fakePort{rx: []byte(
		// First candidate (CONFigure:VOLT:AC) is ignored by the device.
		"VOLT:DC,+0.0006V,AUTO,2V\r\n" + "OFF\r\n" + "OFF\r\n" +
			// Fallback (FUNCtion "VOLT:AC") works.
			"VOLT:AC,+0.5000V,AUTO,2V\r\n" + "OFF\r\n" + "OFF\r\n",
	)}
	s := newMeterDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetMeterFunction(context.Background(), "VOLT:AC"); err != nil {
		t.Fatalf("SetMeterFunction: %v", err)
	}
	want := []string{
		"SYSTem:REMote\r\n",
		"CONFigure:VOLT:AC\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
		"FUNCtion \"VOLT:AC\"\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:AC:NULL?\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	if s.meter == nil || s.meter.Func != "VOLT:AC" {
		t.Fatalf("meter = %+v, want VOLT:AC", s.meter)
	}
}

func TestSetMeterAutoRange(t *testing.T) {
	f := &fakePort{rx: []byte(
		"VOLT:DC,+0.0006V,AUTO,2V\r\n" + "OFF\r\n" + "OFF\r\n",
	)}
	s := newMeterDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetMeterAutoRange(context.Background(), true); err != nil {
		t.Fatalf("SetMeterAutoRange: %v", err)
	}
	want := []string{
		"SYSTem:REMote\r\n",
		"VOLT:DC:RANGE:AUTO ON\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func TestSetMeterRange(t *testing.T) {
	f := &fakePort{rx: []byte(
		"VOLT:DC,+2.0000V,MAN,2V\r\n" + "OFF\r\n" + "OFF\r\n",
	)}
	s := newMeterDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetMeterRange(context.Background(), 2); err != nil {
		t.Fatalf("SetMeterRange: %v", err)
	}
	want := []string{
		"SYSTem:REMote\r\n",
		"VOLT:DC:RANGE 2\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func TestSetMeterNull(t *testing.T) {
	f := &fakePort{rx: []byte(
		"VOLT:DC,+0.0006V,AUTO,2V\r\n" + "OFF\r\n" + "ON\r\n",
	)}
	s := newMeterDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetMeterNull(context.Background(), true); err != nil {
		t.Fatalf("SetMeterNull: %v", err)
	}
	want := []string{
		"SYSTem:REMote\r\n",
		":VOLT:DC:NULL ON\r\n",
		":CONFigure:ALL?\n", "MULTimeter:HOLD?\n", ":VOLT:DC:NULL?\n",
	}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func TestSetMeterFunctionRejectsUnknown(t *testing.T) {
	f := &fakePort{}
	s := newMeterDevice(f)
	if err := s.SetMeterFunction(context.Background(), "BOGUS"); err == nil {
		t.Fatal("SetMeterFunction accepted an unknown function")
	}
	if got := f.written(); len(got) != 0 {
		t.Fatalf("unexpected writes %q", got)
	}
}

// stopReleaseTimer makes sure a pending post-operation timer does not fire
// after a test has finished.
func stopReleaseTimer(s *SPM3051) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releaseTimer != nil {
		s.releaseTimer.Stop()
		s.releaseTimer = nil
	}
}

func TestReleaseAfterIdleTimer(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	s.remoteMode = true

	// A recent control command keeps the panel locked.
	s.lastControl = time.Now()
	s.releaseAfterIdle()
	if !s.remoteMode || len(f.written()) != 0 {
		t.Fatalf("released inside the window: remote=%v writes=%q", s.remoteMode, f.written())
	}

	// Once the post-operation window has elapsed the panel is released.
	s.lastControl = time.Now().Add(-remoteIdleRelease - time.Millisecond)
	s.releaseAfterIdle()
	if !slices.Contains(f.written(), "SYSTem:LOCal\r\n") {
		t.Fatalf("writes = %q; want SYSTem:LOCal", f.written())
	}
	if s.remoteMode {
		t.Fatal("remoteMode still set after the release timer fired")
	}
}

func TestSetVoltageTakesRemoteControl(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.SetVoltage(context.Background(), 5); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	want := []string{"SYSTem:REMote\r\n", "VOLT 5.000\r\n"}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	if !s.remoteMode {
		t.Fatal("remoteMode not set after a set command")
	}
	if !timerArmed(s) {
		t.Fatal("post-operation release timer not armed")
	}

	// A second set must not resend SYSTem:REMote.
	if err := s.SetCurrent(context.Background(), 0.46); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	want = append(want, "CURR 0.460\r\n")
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func timerArmed(s *SPM3051) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseTimer != nil
}

func TestIdleReleaseReturnsPanelControl(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	s.remoteMode = true
	s.lastControl = time.Now().Add(-remoteIdleRelease - time.Second)

	s.maybeReleaseLocked()
	if !slices.Contains(f.written(), "SYSTem:LOCal\r\n") {
		t.Fatalf("writes = %q; want SYSTem:LOCal", f.written())
	}
	if s.remoteMode {
		t.Fatal("remoteMode still set after idle release")
	}

	// Inside the idle window nothing is sent.
	f2 := &fakePort{}
	s2 := newTestDevice(f2)
	s2.remoteMode = true
	s2.lastControl = time.Now()
	s2.maybeReleaseLocked()
	if len(f2.written()) != 0 {
		t.Fatalf("idle window: unexpected writes %q", f2.written())
	}
}

func TestReleaseControlKeepsConnection(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	s.remoteMode = true
	t.Cleanup(func() { stopReleaseTimer(s) })

	if err := s.ReleaseControl(context.Background()); err != nil {
		t.Fatalf("ReleaseControl: %v", err)
	}
	if timerArmed(s) {
		t.Fatal("release timer still armed after ReleaseControl")
	}
	if !s.IsConnected() {
		t.Fatal("ReleaseControl must not disconnect the device")
	}
	if !slices.Contains(f.written(), "SYSTem:LOCal\r\n") {
		t.Fatalf("writes = %q; want SYSTem:LOCal", f.written())
	}
	if s.remoteMode {
		t.Fatal("remoteMode still set after ReleaseControl")
	}

	// The next set command takes control again.
	if err := s.SetVoltage(context.Background(), 3.3); err != nil {
		t.Fatalf("SetVoltage after release: %v", err)
	}
	got := f.written()
	if len(got) < 3 || got[len(got)-2] != "SYSTem:REMote\r\n" || got[len(got)-1] != "VOLT 3.300\r\n" {
		t.Fatalf("writes after release = %q", got)
	}
	if !timerArmed(s) {
		t.Fatal("post-operation release timer not armed after a set command")
	}
}

func TestGetStateParsesReply(t *testing.T) {
	f := &fakePort{rx: []byte("5.010,0.000,0.000,OFF,OFF,OFF,1\r\nON\r\n")}
	s := newTestDevice(f)
	s.currSet = 1

	st, err := s.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.VoltMeas != 5.010 || st.CurrMeas != 0 || st.PowerMeas != 0 {
		t.Fatalf("measurements = %+v", st)
	}
	if !st.OutputOn {
		t.Fatal("output_on = false, OUTP? replied ON")
	}
	if !st.CVMode {
		t.Fatal("cv_mode = false, mode field is 1 (CV)")
	}
	if st.RemoteControl {
		t.Fatal("remote_control = true for a local-mode device")
	}
	if s.commErrCnt != 0 {
		t.Fatalf("commErrCnt = %d after a good poll", s.commErrCnt)
	}
}

func TestGetStateReportsRemoteControl(t *testing.T) {
	f := &fakePort{rx: []byte("5.010,0.000,0.000,OFF,OFF,OFF,1\r\nON\r\n")}
	s := newTestDevice(f)
	s.remoteMode = true
	s.lastControl = time.Now()

	st, err := s.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !st.RemoteControl {
		t.Fatal("remote_control = false while remote mode is active")
	}
}

func TestDisconnectReleasesPanelControl(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	s.remoteMode = true
	s.releaseTimer = time.AfterFunc(time.Hour, func() {})

	if err := s.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if !slices.Contains(f.written(), "SYSTem:LOCal\r\n") {
		t.Fatalf("writes = %q; Disconnect must send SYSTem:LOCal", f.written())
	}
	if f.drains == 0 {
		t.Fatal("Disconnect did not Drain before closing the port")
	}
	if f.closes != 1 {
		t.Fatalf("closes = %d, want 1", f.closes)
	}
	if s.IsConnected() {
		t.Fatal("still connected after Disconnect")
	}
	if s.remoteMode {
		t.Fatal("remoteMode still set after Disconnect")
	}
	if timerArmed(s) {
		t.Fatal("release timer still armed after Disconnect")
	}
}

func TestDisconnectNotConnectedIsNoop(t *testing.T) {
	f := &fakePort{}
	s := &SPM3051{conn: nil}
	if err := s.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if len(f.written()) != 0 {
		t.Fatal("unexpected writes")
	}
}

func TestApplySetRejectsInvalidValues(t *testing.T) {
	f := &fakePort{}
	s := newTestDevice(f)
	for _, v := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := s.SetVoltage(context.Background(), v); err == nil {
			t.Fatalf("SetVoltage(%v) succeeded", v)
		}
	}
	if len(f.written()) != 0 {
		t.Fatalf("invalid set-points wrote to the port: %q", f.written())
	}
}
