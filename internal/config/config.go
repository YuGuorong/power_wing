package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// CycleConfig holds the cycle-switch settings for a power supply.
type CycleConfig struct {
	Enabled bool    `json:"enabled"`
	OnMin   float64 `json:"on_min"`
	OnMax   float64 `json:"on_max"`
	OffMin  float64 `json:"off_min"`
	OffMax  float64 `json:"off_max"`
}

// DeviceConfig holds configuration for a single device.
type DeviceConfig struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Type    string        `json:"type"` // "spm3051", "pd_pocket", "usbslim"
	Port    string        `json:"port"` // e.g. "COM3" or "/dev/ttyUSB0"
	Baud    int           `json:"baud"` // 0 = use driver default
	Enabled bool          `json:"enabled"`
	Cycles  []CycleConfig `json:"cycles,omitempty"`
}

// NumCyclePorts returns how many cycle ports this device has.
// 1 for power supply, 4 for USB hub.
func (d *DeviceConfig) NumCyclePorts() int {
	switch d.Type {
	case "usbslim":
		return 4
	default:
		return 1
	}
}

// GetCycle returns the cycle config for a specific port (0 for PSU, 1-4 for hub).
// Returns DefaultCycle() if the port's config is not yet initialized.
func (d *DeviceConfig) GetCycle(port int) CycleConfig {
	if port < 0 || len(d.Cycles) == 0 || port >= len(d.Cycles) {
		return DefaultCycle()
	}
	cc := d.Cycles[port]
	if cc.OnMin == 0 && cc.OnMax == 0 && cc.OffMin == 0 && cc.OffMax == 0 {
		def := DefaultCycle()
		cc.Enabled = def.Enabled
		cc.OnMin = def.OnMin
		cc.OnMax = def.OnMax
		cc.OffMin = def.OffMin
		cc.OffMax = def.OffMax
	}
	return cc
}

// SetCycle sets the cycle config for a specific port.
func (d *DeviceConfig) SetCycle(port int, cc CycleConfig) {
	d.EnsureCycles()
	if port >= 0 && port < len(d.Cycles) {
		d.Cycles[port] = cc
	}
}

// EnsureCycles initializes the Cycles slice with defaults if nil or too short.
func (d *DeviceConfig) EnsureCycles() {
	n := d.NumCyclePorts()
	if len(d.Cycles) < n {
		old := d.Cycles
		d.Cycles = make([]CycleConfig, n)
		for i := range d.Cycles {
			if i < len(old) {
				d.Cycles[i] = old[i]
			} else {
				d.Cycles[i] = DefaultCycle()
			}
		}
	}
}

// Config is the application-level configuration.
type Config struct {
	Devices      []DeviceConfig `json:"devices"`
	ServerPort   int            `json:"server_port"`
	ActiveDevice string         `json:"active_device"`
	VoiceEnabled bool           `json:"voice_enabled"`
}

// Default returns safe default configuration.
func Default() *Config {
	return &Config{
		Devices:      []DeviceConfig{},
		ServerPort:   8765,
		VoiceEnabled: false,
	}
}

// DefaultCycle returns default cycle-switch settings.
func DefaultCycle() CycleConfig {
	return CycleConfig{
		Enabled: false,
		OnMin:   1.0,
		OnMax:   3.0,
		OffMin:  1.0,
		OffMax:  3.0,
	}
}

// configFilePath returns the OS-appropriate config file path.
func configFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "PowerWing", "config.json"), nil
}

// Load reads configuration from disk. Returns Default() if the file does not
// exist yet.
func Load() (*Config, error) {
	path, err := configFilePath()
	if err != nil {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes configuration to disk.
func (c *Config) Save() error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// UpsertDevice adds or replaces a device entry by ID.
func (c *Config) UpsertDevice(dev DeviceConfig) {
	for i, d := range c.Devices {
		if d.ID == dev.ID {
			c.Devices[i] = dev
			return
		}
	}
	c.Devices = append(c.Devices, dev)
}

// UpdateDeviceName changes the display name for an existing device entry.
func (c *Config) UpdateDeviceName(id, name string) {
	for i, d := range c.Devices {
		if d.ID == id {
			c.Devices[i].Name = name
			return
		}
	}
}

// UpdateDeviceConn updates the serial port and baud for an existing device entry.
// Pass empty string for port or 0 for baud to keep the current value.
func (c *Config) UpdateDeviceConn(id, port string, baud int) {
	for i, d := range c.Devices {
		if d.ID == id {
			if port != "" {
				c.Devices[i].Port = port
			}
			if baud > 0 {
				c.Devices[i].Baud = baud
			}
			return
		}
	}
}

// RemoveDevice deletes a device entry by ID.
func (c *Config) RemoveDevice(id string) {
	out := c.Devices[:0]
	for _, d := range c.Devices {
		if d.ID != id {
			out = append(out, d)
		}
	}
	c.Devices = out
}
