// Package manager owns the lifecycle of all configured devices and acts as the
// single source of truth for device state.  It exposes a subscription channel
// so callers (e.g., the HTTP server) can fan out state updates to WebSocket
// clients.
package manager

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/device"
	"github.com/yuguorong/power_wing/internal/device/pdpocket"
	"github.com/yuguorong/power_wing/internal/device/spm3051"
	"github.com/yuguorong/power_wing/internal/device/usbslim"
)

const (
	pollInterval      = 1 * time.Second
	reconnectInterval = 5 * time.Second
	readDeviceTimeout = 500 * time.Millisecond
)

// ─── public types ─────────────────────────────────────────────────────────────

// DeviceInfo is the metadata the UI needs for each device.
type DeviceInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Connected bool   `json:"connected"`
	Port      string `json:"port"`
	Baud      int    `json:"baud"`
}

// StateUpdate is pushed to subscribers whenever a device's state changes.
type StateUpdate struct {
	DeviceID   string      `json:"device_id"`
	DeviceType string      `json:"device_type"`
	DeviceName string      `json:"device_name"`
	State      interface{} `json:"state"`
}

// CmdRequest is how callers ask the manager to execute a device command.
type CmdRequest struct {
	DeviceID string
	Command  string
	Params   map[string]interface{}
	Result   chan error
}

// ─── Manager ──────────────────────────────────────────────────────────────────

// Manager owns all device instances and drives periodic polling.
type Manager struct {
	cfg *config.Config

	mu      sync.RWMutex
	devices map[string]device.Device // id → device
	states  map[string]interface{}   // id → *PowerState | *HubState
	renames map[string]string        // id → user-set display name override

	// manualDisconnect holds IDs that the user explicitly disconnected.
	// The auto-reconnect loop skips these until the user reconnects.
	manualDisconnect map[string]bool

	subsMu sync.Mutex
	subs   []chan StateUpdate

	cmdCh  chan CmdRequest
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New creates a Manager from configuration.
func New(cfg *config.Config) *Manager {
	return &Manager{
		cfg:              cfg,
		devices:          make(map[string]device.Device),
		states:           make(map[string]interface{}),
		renames:          make(map[string]string),
		manualDisconnect: make(map[string]bool),
		cmdCh:            make(chan CmdRequest, 32),
		stopCh:           make(chan struct{}),
	}
}

// Start connects to all configured devices and begins polling.
func (m *Manager) Start() error {
	for _, dc := range m.cfg.Devices {
		if !dc.Enabled {
			continue
		}
		dev, err := makeDevice(dc)
		if err != nil {
			log.Printf("[manager] %s: cannot create device: %v", dc.ID, err)
			continue
		}
		m.devices[dc.ID] = dev
		if dc.Name != "" {
			m.renames[dc.ID] = dc.Name
		}
		if err := dev.Connect(); err != nil {
			log.Printf("[manager] %s: connect failed (will retry): %v", dc.ID, err)
		} else {
			log.Printf("[manager] %s: connected", dc.ID)
		}
	}

	m.wg.Add(2)
	go m.pollLoop()
	go m.cmdLoop()
	return nil
}

// Stop signals all background goroutines and waits for them to finish.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, dev := range m.devices {
		_ = dev.Disconnect()
	}
}

// Subscribe returns a channel that receives StateUpdates.  The caller must
// eventually call Unsubscribe.
func (m *Manager) Subscribe() chan StateUpdate {
	ch := make(chan StateUpdate, 32)
	m.subsMu.Lock()
	m.subs = append(m.subs, ch)
	m.subsMu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscription channel.
func (m *Manager) Unsubscribe(ch chan StateUpdate) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	out := m.subs[:0]
	for _, s := range m.subs {
		if s != ch {
			out = append(out, s)
		}
	}
	m.subs = out
	close(ch)
}

// Devices returns metadata for all registered devices.
func (m *Manager) Devices() []DeviceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []DeviceInfo
	// Build a port/baud lookup from config for the extra fields.
	connMap := make(map[string]struct {
		port string
		baud int
	}, len(m.cfg.Devices))
	for _, dc := range m.cfg.Devices {
		connMap[dc.ID] = struct {
			port string
			baud int
		}{dc.Port, dc.Baud}
	}
	for _, dev := range m.devices {
		cm := connMap[dev.ID()]
		name := m.renames[dev.ID()]
		if name == "" {
			name = dev.Name()
		}
		out = append(out, DeviceInfo{
			ID:        dev.ID(),
			Name:      name,
			Type:      dev.Type(),
			Connected: dev.IsConnected(),
			Port:      cm.port,
			Baud:      cm.baud,
		})
	}
	return out
}

// RenameDevice updates the display name for a device and persists it to config.
func (m *Manager) RenameDevice(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("device name cannot be empty")
	}
	m.mu.Lock()
	if _, ok := m.devices[id]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown device %q", id)
	}
	m.renames[id] = name
	m.mu.Unlock()
	m.cfg.UpdateDeviceName(id, name)
	return m.cfg.Save()
}

// LatestState returns the most recently polled state for a device.
func (m *Manager) LatestState(id string) (interface{}, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.states[id]
	return s, ok
}

// SendCmd queues a command for execution and waits for the result.
func (m *Manager) SendCmd(req CmdRequest) error {
	req.Result = make(chan error, 1)
	m.cmdCh <- req
	return <-req.Result
}

// AddDevice creates and connects a new device at runtime, and persists the
// config entry.
func (m *Manager) AddDevice(dc config.DeviceConfig) error {
	dev, err := makeDevice(dc)
	if err != nil {
		return err
	}
	if err := dev.Connect(); err != nil {
		log.Printf("[manager] %s: connect failed (will retry): %v", dc.ID, err)
	}
	m.mu.Lock()
	m.devices[dc.ID] = dev
	m.mu.Unlock()
	m.cfg.UpsertDevice(dc)
	return m.cfg.Save()
}

// Config returns the current configuration (read-only reference).
func (m *Manager) Config() *config.Config { return m.cfg }

// ReplaceConfig swaps configuration and saves it.  Does not reconnect devices.
func (m *Manager) ReplaceConfig(newCfg *config.Config) error {
	m.mu.Lock()
	m.cfg = newCfg
	m.mu.Unlock()
	return newCfg.Save()
}

// DisconnectDevice disconnects a device but keeps it registered so the UI
// can still show it and the user can reconnect later.
func (m *Manager) DisconnectDevice(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, ok := m.devices[id]
	if !ok {
		return fmt.Errorf("unknown device %q", id)
	}
	// Mark as intentionally disconnected so auto-reconnect skips it.
	m.manualDisconnect[id] = true
	return dev.Disconnect()
}

// ReconnectDevice optionally updates the serial port / baud rate, persists the
// config, then reconnects the device.  Pass empty string for port or 0 for baud
// to keep the current configuration values.
func (m *Manager) ReconnectDevice(id, port string, baud int) error {
	// Disconnect and remove the old instance.
	m.mu.Lock()
	dev, ok := m.devices[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown device %q", id)
	}
	_ = dev.Disconnect()
	delete(m.devices, id)
	m.mu.Unlock()

	// Update config with new connection params (if provided).
	m.cfg.UpdateDeviceConn(id, port, baud)
	if err := m.cfg.Save(); err != nil {
		log.Printf("[manager] %s: save config after reconnect: %v", id, err)
	}

	// Find the (now-updated) device config.
	var dc config.DeviceConfig
	found := false
	for _, d := range m.cfg.Devices {
		if d.ID == id {
			dc = d
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("device %q not in config", id)
	}

	// Create fresh driver and connect.
	newDev, err := makeDevice(dc)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.devices[id] = newDev         // register first so poll loop can see it
	delete(m.manualDisconnect, id) // user asked to reconnect — clear the block
	m.mu.Unlock()
	if err := newDev.Connect(); err != nil {
		return fmt.Errorf("connect %q: %w", id, err)
	}
	return nil
}

// RemoveDevice disconnects and removes a device.
func (m *Manager) RemoveDevice(id string) error {
	m.mu.Lock()
	dev, ok := m.devices[id]
	if ok {
		_ = dev.Disconnect()
		delete(m.devices, id)
		delete(m.states, id)
	}
	m.mu.Unlock()
	m.cfg.RemoveDevice(id)
	return m.cfg.Save()
}

// ─── background loops ─────────────────────────────────────────────────────────

func (m *Manager) pollLoop() {
	defer m.wg.Done()
	poll := time.NewTicker(pollInterval)
	reconnect := time.NewTicker(reconnectInterval)
	defer poll.Stop()
	defer reconnect.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-poll.C:
			m.pollAll()
		case <-reconnect.C:
			m.reconnectAll()
		}
	}
}

func (m *Manager) cmdLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stopCh:
			return
		case req := <-m.cmdCh:
			req.Result <- m.execCmd(req)
		}
	}
}

// pollDevice immediately fetches and broadcasts the state of a single device.
// It is called in a goroutine after each command so the UI reflects the
// hardware response without waiting for the next periodic poll tick.
func (m *Manager) pollDevice(id string) {
	m.mu.RLock()
	dev, ok := m.devices[id]
	m.mu.RUnlock()
	if !ok || !dev.IsConnected() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), readDeviceTimeout)
	defer cancel()
	var st interface{}
	var err error
	switch d := dev.(type) {
	case device.PowerSupply:
		st, err = d.GetState(ctx)
	case device.USBHub:
		st, err = d.GetState(ctx)
	}
	if err != nil {
		log.Printf("[manager] pollDevice %s: %v", id, err)
		return
	}
	m.mu.Lock()
	m.states[id] = st
	m.mu.Unlock()
	m.broadcast(StateUpdate{
		DeviceID:   dev.ID(),
		DeviceType: dev.Type(),
		DeviceName: dev.Name(),
		State:      st,
	})
}

func (m *Manager) pollAll() {
	ctx, cancel := context.WithTimeout(context.Background(), pollInterval-50*time.Millisecond)
	defer cancel()

	m.mu.RLock()
	devs := make([]device.Device, 0, len(m.devices))
	for _, d := range m.devices {
		devs = append(devs, d)
	}
	m.mu.RUnlock()

	for _, dev := range devs {
		if !dev.IsConnected() {
			continue
		}
		var state interface{}
		var err error
		switch d := dev.(type) {
		case device.PowerSupply:
			state, err = d.GetState(ctx)
		case device.USBHub:
			state, err = d.GetState(ctx)
		}
		if err != nil {
			log.Printf("[manager] poll %s: %v", dev.ID(), err)
			continue
		}
		m.mu.Lock()
		m.states[dev.ID()] = state
		m.mu.Unlock()
		m.broadcast(StateUpdate{
			DeviceID:   dev.ID(),
			DeviceType: dev.Type(),
			DeviceName: dev.Name(),
			State:      state,
		})
	}
}

func (m *Manager) reconnectAll() {
	m.mu.RLock()
	devs := make([]device.Device, 0, len(m.devices))
	for _, d := range m.devices {
		devs = append(devs, d)
	}
	m.mu.RUnlock()

	for _, dev := range devs {
		if !dev.IsConnected() {
			// Skip devices the user manually disconnected.
			m.mu.RLock()
			skip := m.manualDisconnect[dev.ID()]
			m.mu.RUnlock()
			if skip {
				continue
			}
			if err := dev.Connect(); err == nil {
				log.Printf("[manager] %s: reconnected", dev.ID())
			}
		}
	}
}

func (m *Manager) broadcast(u StateUpdate) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- u:
		default:
		}
	}
}

// ─── command execution ────────────────────────────────────────────────────────

func (m *Manager) execCmd(req CmdRequest) error {
	m.mu.RLock()
	dev, ok := m.devices[req.DeviceID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown device %q", req.DeviceID)
	}
	ctx := context.Background()

	switch req.Command {
	// ── power supply ──────────────────────────────────────────────────────────
	case "set_volt":
		psu, ok := dev.(device.PowerSupply)
		if !ok {
			return fmt.Errorf("%s is not a power supply", req.DeviceID)
		}
		v, err := floatParam(req.Params, "value")
		if err != nil {
			return err
		}
		if err := psu.SetVoltage(ctx, v); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "set_curr":
		psu, ok := dev.(device.PowerSupply)
		if !ok {
			return fmt.Errorf("%s is not a power supply", req.DeviceID)
		}
		v, err := floatParam(req.Params, "value")
		if err != nil {
			return err
		}
		if err := psu.SetCurrent(ctx, v); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "set_outp":
		psu, ok := dev.(device.PowerSupply)
		if !ok {
			return fmt.Errorf("%s is not a power supply", req.DeviceID)
		}
		on, err := boolParam(req.Params, "on")
		if err != nil {
			return err
		}
		if err := psu.SetOutput(ctx, on); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "set_ovp":
		psu, ok := dev.(device.PowerSupply)
		if !ok {
			return fmt.Errorf("%s is not a power supply", req.DeviceID)
		}
		v, err := floatParam(req.Params, "value")
		if err != nil {
			return err
		}
		if err := psu.SetOVP(ctx, v); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "set_ocp":
		psu, ok := dev.(device.PowerSupply)
		if !ok {
			return fmt.Errorf("%s is not a power supply", req.DeviceID)
		}
		v, err := floatParam(req.Params, "value")
		if err != nil {
			return err
		}
		if err := psu.SetOCP(ctx, v); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	// ── USB hub ───────────────────────────────────────────────────────────────
	case "set_usb_port":
		hub, ok := dev.(device.USBHub)
		if !ok {
			return fmt.Errorf("%s is not a USB hub", req.DeviceID)
		}
		port, err := intParam(req.Params, "port")
		if err != nil {
			return err
		}
		on, err := boolParam(req.Params, "on")
		if err != nil {
			return err
		}
		return hub.SetPort(ctx, port, on)

	case "set_lock":
		hub, ok := dev.(device.USBHub)
		if !ok {
			return fmt.Errorf("%s is not a USB hub", req.DeviceID)
		}
		locked, err := boolParam(req.Params, "locked")
		if err != nil {
			return err
		}
		return hub.SetLock(ctx, locked)

	case "set_hwkeys":
		hub, ok := dev.(device.USBHub)
		if !ok {
			return fmt.Errorf("%s is not a USB hub", req.DeviceID)
		}
		enabled, err := boolParam(req.Params, "enabled")
		if err != nil {
			return err
		}
		return hub.SetHWKeys(ctx, enabled)

	case "set_autosave":
		hub, ok := dev.(device.USBHub)
		if !ok {
			return fmt.Errorf("%s is not a USB hub", req.DeviceID)
		}
		enabled, err := boolParam(req.Params, "enabled")
		if err != nil {
			return err
		}
		return hub.SetAutoSave(ctx, enabled)

	default:
		return fmt.Errorf("unknown command %q", req.Command)
	}
}

// ─── device factory ───────────────────────────────────────────────────────────

func makeDevice(dc config.DeviceConfig) (device.Device, error) {
	switch dc.Type {
	case "spm3051":
		return spm3051.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	case "pd_pocket":
		return pdpocket.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	case "usbslim":
		return usbslim.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	default:
		return nil, fmt.Errorf("unknown device type %q", dc.Type)
	}
}

// ─── param helpers ────────────────────────────────────────────────────────────

func floatParam(p map[string]interface{}, key string) (float64, error) {
	v, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("missing param %q", key)
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	}
	return 0, fmt.Errorf("param %q is not a number", key)
}

func boolParam(p map[string]interface{}, key string) (bool, error) {
	v, ok := p[key]
	if !ok {
		return false, fmt.Errorf("missing param %q", key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("param %q is not a bool", key)
	}
	return b, nil
}

func intParam(p map[string]interface{}, key string) (int, error) {
	v, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("missing param %q", key)
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	}
	return 0, fmt.Errorf("param %q is not an integer", key)
}
