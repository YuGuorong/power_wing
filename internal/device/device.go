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
	CVMode       bool `json:"cv_mode"` // true=CV, false=CC
	// Optional extras
	Temperature float64 `json:"temperature,omitempty"`
	InputVolt   float64 `json:"input_volt,omitempty"`
}

// ─── USB hub ──────────────────────────────────────────────────────────────────

// USBHub extends Device with per-port power switching.
type USBHub interface {
	Device
	GetState(ctx context.Context) (*HubState, error)
	SetPort(ctx context.Context, port int, on bool) error  // port 1-4
	SetLock(ctx context.Context, locked bool) error        // UI lock
	SetHWKeys(ctx context.Context, enabled bool) error     // physical buttons
	SetAutoSave(ctx context.Context, enabled bool) error   // auto-save state
	SetDefaultOn(ctx context.Context, portMask byte) error // default-ON mask
}

// HubState carries everything the UI needs to render a USB hub page.
type HubState struct {
	Connected       bool    `json:"connected"`
	Ports           [4]bool `json:"ports"` // true = ON
	Locked          bool    `json:"locked"`
	HWKeysEnabled   bool    `json:"hw_keys_enabled"`
	AutoSaveEnabled bool    `json:"auto_save_enabled"`
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
