// Package device defines the interfaces and shared state types for all
// supported hardware (power supplies and USB hubs).
package device

import "context"

// ─── Base interface ──────────────────────────────────────────────────────────

// Device is the lowest-common-denominator interface every piece of hardware
// must satisfy.
type Device interface {
	ID() string
	Name() string
	Type() string // "power_supply" | "usb_hub"
	Connect() error
	Disconnect() error
	IsConnected() bool
}

// ─── Power supply ─────────────────────────────────────────────────────────────

// PowerSupply extends Device with PSU-specific controls.
type PowerSupply interface {
	Device
	// Measurements + set-points in one call (used for periodic polling).
	GetState(ctx context.Context) (*PowerState, error)
	// Individual setters.
	SetVoltage(ctx context.Context, volts float64) error
	SetCurrent(ctx context.Context, amps float64) error
	SetOutput(ctx context.Context, on bool) error
	SetOVP(ctx context.Context, volts float64) error
	SetOCP(ctx context.Context, amps float64) error
}

// PowerState carries everything the UI needs to render a PSU page.
type PowerState struct {
	Connected bool `json:"connected"`
	// Measured values
	VoltMeas  float64 `json:"volt_meas"`
	CurrMeas  float64 `json:"curr_meas"`
	PowerMeas float64 `json:"power_meas"`
	// Set-points (cached locally after each Set* call or explicit query)
	VoltSet float64 `json:"volt_set"`
	CurrSet float64 `json:"curr_set"`
	// Protection limits
	OVPLimit float64 `json:"ovp_limit"`
	OCPLimit float64 `json:"ocp_limit"`
	// Status flags
	OutputOn     bool `json:"output_on"`
	OVPTriggered bool `json:"ovp_triggered"`
	OCPTriggered bool `json:"ocp_triggered"`
	OTPTriggered bool `json:"otp_triggered"` // over-temperature protection tripped
	CVMode       bool `json:"cv_mode"`       // true=CV, false=CC
	// RemoteControl is true while the PC holds front-panel control (the
	// device's keys/knob are locked).  Devices without local/remote switching
	// leave it false.
	RemoteControl bool `json:"remote_control"`
	// Optional extras
	Temperature float64 `json:"temperature,omitempty"`
	InputVolt   float64 `json:"input_volt,omitempty"`
}

// ControlReleaser is implemented by power supplies that can lock their local
// front panel for remote operation.  ReleaseControl hands the panel back to
// the operator while the device stays connected and polled; the next Set*
// call takes control again automatically.
type ControlReleaser interface {
	ReleaseControl(ctx context.Context) error
}

// ─── USB hub ──────────────────────────────────────────────────────────────────

// USBHub extends Device with per-port power switching.
type USBHub interface {
	Device
	GetState(ctx context.Context) (*HubState, error)
	SetPort(ctx context.Context, port int, on bool) error // port 1..PortCount
	// SetAllPorts switches every port ON or OFF in one operation.
	SetAllPorts(ctx context.Context, on bool) error
	SetLock(ctx context.Context, locked bool) error        // UI lock
	SetHWKeys(ctx context.Context, enabled bool) error     // physical buttons
	SetAutoSave(ctx context.Context, enabled bool) error   // auto-save state
	SetDefaultOn(ctx context.Context, portMask byte) error // default-ON mask
}

// HubState carries everything the UI needs to render a USB hub page.
type HubState struct {
	Connected bool   `json:"connected"`
	Ports     []bool `json:"ports"`      // true = ON, indexed by port-1
	PortCount int    `json:"port_count"` // number of switchable ports
	Locked    bool   `json:"locked"`
	// Optional controls – drivers that lack the feature leave these false so
	// the UI can hide the corresponding buttons.
	HWKeysEnabled    bool `json:"hw_keys_enabled"`
	AutoSaveEnabled  bool `json:"auto_save_enabled"`
	SupportsLock     bool `json:"supports_lock"`
	SupportsHWKeys   bool `json:"supports_hw_keys"`
	SupportsAutoSave bool `json:"supports_auto_save"`
}

// ─── USB alarm light ──────────────────────────────────────────────────────────

// AlarmLight extends Device with 7-color alarm light controls over Modbus RTU.
type AlarmLight interface {
	Device
	GetState(ctx context.Context) (*AlarmState, error)
	SetColor(ctx context.Context, color string, mode string) error
	Buzzer(ctx context.Context, on bool) error
	Off(ctx context.Context) error
	Beep(ctx context.Context, color string, ms int) error
	PWM(ctx context.Context, red, green, blue int, flashInterval int, buzzer bool) error
	Rainbow(ctx context.Context, durationMs int) error
	Fade(ctx context.Context, colorFrom, colorTo string, durationMs int, loop int) error
}

// AlarmState carries everything the UI needs to render an alarm light page.
type AlarmState struct {
	Connected     bool   `json:"connected"`
	Color         string `json:"color"` // red, yellow, green, white, cyan, purple, blue, off
	Mode          string `json:"mode"`  // on, blink_slow, blink_fast, off
	BuzzerOn      bool   `json:"buzzer_on"`
	PWMR          int    `json:"pwm_r"`
	PWMG          int    `json:"pwm_g"`
	PWMB          int    `json:"pwm_b"`
	FlashInterval int    `json:"flash_interval"`
}

// ─── Power monitor meter ──────────────────────────────────────────────────────

// PowerMonitor extends Device with continuous current/voltage streaming and
// average/peak measurement for the F-11 (AFU IoT) high-precision meter.
type PowerMonitor interface {
	Device
	GetState(ctx context.Context) (*MonitorState, error)
	SetInterval(ctx context.Context, ms int) error
	SetPrint(ctx context.Context, mode int) error
	StartMonitor(ctx context.Context) error
	StopMonitor(ctx context.Context) error
	// SetSampleHandler registers a callback invoked on every decoded sample so
	// the manager can push real-time updates to WebSocket clients.
	SetSampleHandler(func(*MonitorState))
}

// MonitorState carries everything the UI needs to render a power monitor page.
type MonitorState struct {
	Connected bool `json:"connected"`
	// Latest decoded sample
	Current float64 `json:"current"` // A
	Voltage float64 `json:"voltage"` // V
	// Monitor (average/peak) mode
	Monitoring  bool    `json:"monitoring"`
	AvgCurrent  float64 `json:"avg_current"`
	AvgVoltage  float64 `json:"avg_voltage"`
	PeakCurrent float64 `json:"peak_current"`
	PeakVoltage float64 `json:"peak_voltage"`
	SampleCount int     `json:"sample_count"`
	// Current sample interval in ms
	IntervalMs int `json:"interval_ms"`
}
