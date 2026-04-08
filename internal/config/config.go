package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DeviceConfig holds configuration for a single device.
type DeviceConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"` // "spm3051", "pd_pocket", "usbslim"
	Port    string `json:"port"` // e.g. "COM3" or "/dev/ttyUSB0"
	Baud    int    `json:"baud"` // 0 = use driver default
	Enabled bool   `json:"enabled"`
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
