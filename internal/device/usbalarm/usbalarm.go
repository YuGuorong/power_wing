// Package usbalarm implements device.AlarmLight for the USB 7-Color Alarm Light.
//
// Protocol: Modbus RTU over USB serial (4800-8-N-1, default station 0x01).
//
// Direct control (FC06, register 0x00C2):
//
//	Red/Yellow/Green/White/Cyan/Purple/Blue: steady 0x11-0x18, slow-blink 0x21-0x28, fast-blink 0x31-0x38
//	Buzzer: on=0x40, off=0x41
//	All off: 0x60
//
// PWM control (FC10, 7 registers from 0x0002):
//
//	[0]=blue PWM, [1]=green PWM, [2]=light on/off, [3]=flash interval (100ms), [4]=buzzer, [5]=reserved, [6]=red PWM
package usbalarm

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/serialport"
)

const (
	defaultBaud    = 4800
	readTimeout    = 200 * time.Millisecond
	deviceTypeName = "usbalarm"

	regDirect  = 0x00C2 // FC06 direct control register
	regPWMBase = 0x0002 // FC10 PWM start address
	stationID  = 0x01   // default Modbus station

	// Direct control values
	valRedOn      uint16 = 0x11
	valRedSlow    uint16 = 0x21
	valRedFast    uint16 = 0x31
	valYellowOn   uint16 = 0x12
	valYellowSlow uint16 = 0x22
	valYellowFast uint16 = 0x32
	valGreenOn    uint16 = 0x13
	valGreenSlow  uint16 = 0x23
	valGreenFast  uint16 = 0x33
	valWhiteOn    uint16 = 0x15
	valWhiteSlow  uint16 = 0x25
	valWhiteFast  uint16 = 0x35
	valCyanOn     uint16 = 0x16
	valCyanSlow   uint16 = 0x26
	valCyanFast   uint16 = 0x36
	valPurpleOn   uint16 = 0x17
	valPurpleSlow uint16 = 0x27
	valPurpleFast uint16 = 0x37
	valBlueOn     uint16 = 0x18
	valBlueSlow   uint16 = 0x28
	valBlueFast   uint16 = 0x38
	valBuzzerOn   uint16 = 0x40
	valBuzzerOff  uint16 = 0x41
	valAllOff     uint16 = 0x60
)

// colorSteady maps color name → steady-on command value.
var colorSteady = map[string]uint16{
	"red":    valRedOn,
	"yellow": valYellowOn,
	"green":  valGreenOn,
	"white":  valWhiteOn,
	"cyan":   valCyanOn,
	"purple": valPurpleOn,
	"blue":   valBlueOn,
}

// colorSlow maps color name → slow-blink command value.
var colorSlow = map[string]uint16{
	"red":    valRedSlow,
	"yellow": valYellowSlow,
	"green":  valGreenSlow,
	"white":  valWhiteSlow,
	"cyan":   valCyanSlow,
	"purple": valPurpleSlow,
	"blue":   valBlueSlow,
}

// colorFast maps color name → fast-blink command value.
var colorFast = map[string]uint16{
	"red":    valRedFast,
	"yellow": valYellowFast,
	"green":  valGreenFast,
	"white":  valWhiteFast,
	"cyan":   valCyanFast,
	"purple": valPurpleFast,
	"blue":   valBlueFast,
}

// validColors is the set of recognized color names.
var validColors = map[string]bool{
	"red": true, "yellow": true, "green": true, "white": true,
	"cyan": true, "purple": true, "blue": true,
}

// colorOrder for rainbow demo.
var colorOrder = []string{"red", "yellow", "green", "cyan", "blue", "purple", "white"}

// allColors for state lookup.
var allColors = []string{"red", "yellow", "green", "white", "cyan", "purple", "blue"}

// colorRGB maps color name → approximate R/G/B PWM values (0–1999).
var colorRGB = map[string][3]int{
	"red":    {1999, 0, 0},
	"yellow": {1999, 1000, 0},
	"green":  {0, 1999, 0},
	"white":  {1999, 1999, 1999},
	"cyan":   {0, 1999, 1999},
	"purple": {1999, 0, 1999},
	"blue":   {0, 0, 1999},
}

// USBLight drives a USB 7-color alarm light over Modbus RTU.
type USBAlarmLight struct {
	id   string
	name string
	port string
	baud int

	mu        sync.Mutex
	conn      serialport.Port
	connected bool

	// cancelFade cancels an in-progress Fade animation.
	cancelFade context.CancelFunc

	// locally tracked state (device doesn't report full state)
	color            string
	mode             string // "on", "blink_slow", "blink_fast", "off"
	buzzerOn         bool
	pwmR, pwmG, pwmB int
	flashInterval    int
}

// New creates a new USBAlarmLight driver. baud=0 uses 4800.
func New(id, name, port string, baud int) *USBAlarmLight {
	if baud == 0 {
		baud = defaultBaud
	}
	return &USBAlarmLight{
		id:    id,
		name:  name,
		port:  port,
		baud:  baud,
		color: "off",
		mode:  "off",
	}
}

func (u *USBAlarmLight) ID() string   { return u.id }
func (u *USBAlarmLight) Name() string { return u.name }
func (u *USBAlarmLight) Type() string { return deviceTypeName }

func (u *USBAlarmLight) IsConnected() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.connected
}

func (u *USBAlarmLight) Connect() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.connected {
		return nil
	}
	if u.conn != nil {
		_ = u.conn.Close()
		u.conn = nil
	}
	mode := serialport.Mode(u.baud)
	p, err := serialport.Open(u.port, mode)
	if err != nil {
		return fmt.Errorf("usbalarm %s: connect: %w", u.id, err)
	}
	if err := p.SetReadTimeout(readTimeout); err != nil {
		p.Close()
		return fmt.Errorf("usbalarm %s: set timeout: %w", u.id, err)
	}
	u.conn = p
	u.connected = true
	_ = p.ResetInputBuffer()
	_ = p.ResetOutputBuffer()
	return nil
}

func (u *USBAlarmLight) Disconnect() error {
	u.mu.Lock()
	if u.cancelFade != nil {
		u.cancelFade()
		u.cancelFade = nil
	}
	if !u.connected && u.conn == nil {
		u.mu.Unlock()
		return nil
	}
	u.connected = false
	var err error
	if u.conn != nil {
		err = u.conn.Close()
		u.conn = nil
	}
	return err
}

func (u *USBAlarmLight) GetState(ctx context.Context) (*device.AlarmState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return &device.AlarmState{
		Connected:     u.connected,
		Color:         u.color,
		Mode:          u.mode,
		BuzzerOn:      u.buzzerOn,
		PWMR:          u.pwmR,
		PWMG:          u.pwmG,
		PWMB:          u.pwmB,
		FlashInterval: u.flashInterval,
	}, nil
}

// ─── Command helpers ────────────────────────────────────────────────────────

// modbusCRC16 computes the Modbus CRC-16 checksum.
func modbusCRC16(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// buildFC06 builds a Modbus FC06 frame: [station 06 regH regL valH valL CRCL CRCH]
func buildFC06(station byte, reg, val uint16) []byte {
	frame := make([]byte, 6)
	frame[0] = station
	frame[1] = 0x06
	binary.BigEndian.PutUint16(frame[2:4], reg)
	binary.BigEndian.PutUint16(frame[4:6], val)
	crc := modbusCRC16(frame)
	frame = append(frame, byte(crc), byte(crc>>8))
	return frame
}

// buildFC10 builds a Modbus FC10 (write multiple registers) frame.
func buildFC10(station byte, startAddr uint16, values []uint16) []byte {
	count := len(values)
	byteCount := count * 2
	frame := make([]byte, 7+byteCount)
	frame[0] = station
	frame[1] = 0x10
	binary.BigEndian.PutUint16(frame[2:4], startAddr)
	binary.BigEndian.PutUint16(frame[4:6], uint16(count))
	frame[6] = byte(byteCount)
	for i, v := range values {
		binary.BigEndian.PutUint16(frame[7+i*2:], v)
	}
	crc := modbusCRC16(frame)
	frame = append(frame, byte(crc), byte(crc>>8))
	return frame
}

// sendFrame writes a Modbus frame and drains the response.
func (u *USBAlarmLight) sendFrame(frame []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.connected || u.conn == nil {
		return fmt.Errorf("not connected")
	}
	// 3.5-char silent interval at 4800 baud (~7.3ms)
	time.Sleep(8 * time.Millisecond)
	u.conn.ResetInputBuffer()
	if _, err := u.conn.Write(frame); err != nil {
		u.connected = false
		return fmt.Errorf("write: %w", err)
	}
	u.conn.SetReadTimeout(readTimeout)
	// drain response (ignore actual bytes for now, just check for timeout)
	buf := make([]byte, 32)
	_, _ = u.conn.Read(buf)
	return nil
}

// ─── Public API ─────────────────────────────────────────────────────────────

// stopFade cancels any in-progress Fade animation.
func (u *USBAlarmLight) stopFade() {
	u.mu.Lock()
	if u.cancelFade != nil {
		u.cancelFade()
		u.cancelFade = nil
	}
	u.mu.Unlock()
}

// SetColor lights up one of 7 colors in the given mode.
// mode must be "on", "blink_slow", or "blink_fast".
func (u *USBAlarmLight) SetColor(ctx context.Context, color string, mode string) error {
	u.stopFade()
	if !validColors[color] {
		return fmt.Errorf("unknown color %q (valid: red,yellow,green,white,cyan,purple,blue)", color)
	}
	var val uint16
	switch mode {
	case "on":
		val = colorSteady[color]
	case "blink_slow":
		val = colorSlow[color]
	case "blink_fast":
		val = colorFast[color]
	default:
		return fmt.Errorf("unknown mode %q (valid: on, blink_slow, blink_fast)", mode)
	}
	frame := buildFC06(stationID, regDirect, val)
	if err := u.sendFrame(frame); err != nil {
		return err
	}
	u.mu.Lock()
	u.color = color
	u.mode = mode
	u.buzzerOn = false // switching color turns off buzzer
	u.mu.Unlock()
	return nil
}

// Buzzer turns the buzzer on or off.
func (u *USBAlarmLight) Buzzer(ctx context.Context, on bool) error {
	u.stopFade()
	val := valBuzzerOff
	if on {
		val = valBuzzerOn
	}
	frame := buildFC06(stationID, regDirect, val)
	if err := u.sendFrame(frame); err != nil {
		return err
	}
	u.mu.Lock()
	u.buzzerOn = on
	u.mu.Unlock()
	return nil
}

// Off turns off all lights and buzzer.
func (u *USBAlarmLight) Off(ctx context.Context) error {
	u.stopFade()
	frame := buildFC06(stationID, regDirect, valAllOff)
	if err := u.sendFrame(frame); err != nil {
		return err
	}
	u.mu.Lock()
	u.color = "off"
	u.mode = "off"
	u.buzzerOn = false
	u.mu.Unlock()
	return nil
}

// Beep turns on the light and buzzer for N milliseconds, then turns everything off.
func (u *USBAlarmLight) Beep(ctx context.Context, color string, ms int) error {
	u.stopFade()
	if !validColors[color] {
		return fmt.Errorf("unknown color %q", color)
	}
	if ms <= 0 {
		ms = 400
	}
	// Light on
	frameOn := buildFC06(stationID, regDirect, colorSteady[color])
	if err := u.sendFrame(frameOn); err != nil {
		return err
	}
	// Buzzer on
	frameBuzz := buildFC06(stationID, regDirect, valBuzzerOn)
	if err := u.sendFrame(frameBuzz); err != nil {
		return err
	}
	// Wait
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(ms) * time.Millisecond):
	}
	// All off
	return u.Off(ctx)
}

// PWM sets RGB values via Modbus FC10.
// red/green/blue: 0-1999
// flashInterval: flash period in 100ms units (0 = always on)
// buzzer: whether to enable buzzer
func (u *USBAlarmLight) PWM(ctx context.Context, red, green, blue int, flashInterval int, buzzer bool) error {
	u.stopFade()
	r := clamp(red, 0, 1999)
	g := clamp(green, 0, 1999)
	b := clamp(blue, 0, 1999)
	fi := clamp(flashInterval, 0, 100)
	buzz := 0
	if buzzer {
		buzz = 1
	}
	values := []uint16{uint16(b), uint16(g), 1, uint16(fi), uint16(buzz), 0, uint16(r)}
	frame := buildFC10(stationID, regPWMBase, values)
	if err := u.sendFrame(frame); err != nil {
		return err
	}
	u.mu.Lock()
	u.pwmR = r
	u.pwmG = g
	u.pwmB = b
	u.flashInterval = fi
	u.buzzerOn = buzzer
	u.color = "pwm"
	u.mode = "pwm"
	u.mu.Unlock()
	return nil
}

// Rainbow cycles through all 7 colors.
func (u *USBAlarmLight) Rainbow(ctx context.Context, durationMs int) error {
	u.stopFade()
	if durationMs <= 0 {
		durationMs = 2000
	}
	durSec := float64(durationMs) / 1000.0
	delay := time.Duration(float64(time.Second) * durSec / float64(len(colorOrder)))
	for _, c := range colorOrder {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		frame := buildFC06(stationID, regDirect, colorSteady[c])
		if err := u.sendFrame(frame); err != nil {
			return err
		}
		u.mu.Lock()
		u.color = c
		u.mode = "on"
		u.mu.Unlock()
		time.Sleep(delay)
	}
	// Brief pause then turn off
	time.Sleep(300 * time.Millisecond)
	return u.Off(ctx)
}

// Fade smoothly transitions between two colors by interpolating PWM.
// durationMs is milliseconds; default 2000 if <=0. Steps at ~30 fps.
// loop: -1 = forever (until ctx cancelled), 0 or 1 = single fade, N = N back-and-forth cycles.
func (u *USBAlarmLight) Fade(ctx context.Context, colorFrom, colorTo string, durationMs int, loop int) error {
	if !validColors[colorFrom] {
		return fmt.Errorf("unknown color %q", colorFrom)
	}
	if !validColors[colorTo] {
		return fmt.Errorf("unknown color %q", colorTo)
	}
	if durationMs <= 0 {
		durationMs = 2000
	}
	durSec := float64(durationMs) / 1000.0
	if loop == 0 {
		loop = 1
	}
	infinite := loop < 0

	// Create a cancelable context so other commands can stop this animation.
	ctx, cancel := context.WithCancel(ctx)
	u.mu.Lock()
	if u.cancelFade != nil {
		u.cancelFade() // cancel any previous fade
	}
	u.cancelFade = cancel
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.cancelFade = nil
		u.mu.Unlock()
		cancel()
	}()

	const fps = 30
	tickerInterval := time.Second / fps
	loopLabel := fmt.Sprintf("%d", loop)
	if infinite {
		loopLabel = "∞"
	}
	log.Printf("[usbalarm] Fade %s→%s: durationMs=%d (%.3fs) loop=%s fps=%d interval=%v",
		colorFrom, colorTo, durationMs, durSec, loopLabel, fps, tickerInterval)

	overallStart := time.Now()
	for l := 0; infinite || l < loop; l++ {
		// Alternate direction: even iterations from→to, odd iterations to→from
		c1, c2 := colorFrom, colorTo
		if l%2 == 1 {
			c1, c2 = colorTo, colorFrom
		}
		rgb1 := colorRGB[c1]
		rgb2 := colorRGB[c2]

		startTime := time.Now()
		deadline := startTime.Add(time.Duration(durSec * float64(time.Second)))
		ticker := time.NewTicker(tickerInterval)

	innerLoop:
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			case now := <-ticker.C:
				elapsed := now.Sub(startTime)
				t := elapsed.Seconds() / durSec
				if t > 1.0 {
					t = 1.0
				}
				r := clamp(int(float64(rgb1[0])+(float64(rgb2[0])-float64(rgb1[0]))*t+0.5), 0, 1999)
				g := clamp(int(float64(rgb1[1])+(float64(rgb2[1])-float64(rgb1[1]))*t+0.5), 0, 1999)
				b := clamp(int(float64(rgb1[2])+(float64(rgb2[2])-float64(rgb1[2]))*t+0.5), 0, 1999)

				frame := buildFC10(stationID, regPWMBase, []uint16{uint16(b), uint16(g), 1, 0, 0, 0, uint16(r)})
				if err := u.sendFrame(frame); err != nil {
					ticker.Stop()
					return err
				}
				u.mu.Lock()
				u.pwmR, u.pwmG, u.pwmB = r, g, b
				u.color = "pwm"
				u.mode = "animation"
				u.mu.Unlock()

				if now.After(deadline) || now.Equal(deadline) {
					ticker.Stop()
					break innerLoop
				}
			}
		}
	}
	// End on the target color in steady mode (finite loops only; infinite is cancelled)
	targetColor := colorTo
	if !infinite && loop%2 == 0 && loop > 0 {
		// After even loops we end back at colorFrom
		targetColor = colorFrom
	}
	frameEnd := buildFC06(stationID, regDirect, colorSteady[targetColor])
	if err := u.sendFrame(frameEnd); err != nil {
		return err
	}
	u.mu.Lock()
	u.color = targetColor
	u.mode = "on"
	u.mu.Unlock()
	if infinite {
		log.Printf("[usbalarm] Fade %s⇄%s: infinite loop stopped after %v",
			colorFrom, colorTo, time.Since(overallStart))
	} else {
		log.Printf("[usbalarm] Fade %s→%s: done in %v (expected ~%.3fs × %d)",
			colorFrom, colorTo, time.Since(overallStart), durSec, loop)
	}
	return nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
