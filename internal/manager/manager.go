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
	"github.com/yuguorong/power_wing/internal/device/f11"
	"github.com/yuguorong/power_wing/internal/device/pdpocket"
	"github.com/yuguorong/power_wing/internal/device/powerz"
	"github.com/yuguorong/power_wing/internal/device/sangenxian"
	"github.com/yuguorong/power_wing/internal/device/spm3051"
	"github.com/yuguorong/power_wing/internal/device/usbalarm"
	"github.com/yuguorong/power_wing/internal/device/usbslim"
)

const (
	pollInterval = 1 * time.Second
	// reconnectInterval controls how fast a dropped device is revived by the
	// auto-reconnect loop (e.g. monitor resuming sampling after a USB replug).
	// 1s keeps the gap between a port reappearing and the device reconnecting
	// within the 1-2s requirement.
	reconnectInterval = 1 * time.Second
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

	// everConnected records devices that have successfully opened their port
	// at least once in this process.  The reconnect loop only revives these
	// devices; configured-but-never-used devices are never opened
	// automatically.  This prevents stale COM assignments from grabbing a
	// port that now belongs to another device or application.
	everConnected map[string]bool

	// lazy disables automatic serial-port opening at startup and when devices
	// are added or reconfigured.  It is the default for both GUI and MCP
	// modes: ports are only opened by an explicit connect (UI button / HTTP)
	// or when a command targets a device (see execCmd).  Start(), used by the
	// --auto-connect opt-in, clears it.
	lazy bool

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
		everConnected:    make(map[string]bool),
		cmdCh:            make(chan CmdRequest, 32),
		stopCh:           make(chan struct{}),
	}
}

// Start connects to all configured devices and begins polling.  This opens
// every enabled device's serial port at startup and is only used when the
// user explicitly opts in with --auto-connect.
func (m *Manager) Start() error {
	return m.start(true)
}

// LazyStart registers all devices without opening any serial port.  This is
// the default for both the GUI and MCP modes: ports are opened only by an
// explicit connect (UI button / HTTP API) or when a command targets a device.
func (m *Manager) LazyStart() error {
	return m.start(false)
}

func (m *Manager) start(autoConnect bool) error {
	m.lazy = !autoConnect
	for _, dc := range m.cfg.Devices {
		if !dc.Enabled {
			continue
		}
		dev, err := makeDevice(dc)
		if err != nil {
			log.Printf("[manager] %s: cannot create device: %v", dc.ID, err)
			continue
		}
		m.registerSampleHandler(dev)
		m.devices[dc.ID] = dev
		if dc.Name != "" {
			m.renames[dc.ID] = dc.Name
		}
		if autoConnect {
			if err := dev.Connect(); err != nil {
				log.Printf("[manager] %s: connect failed (will retry): %v", dc.ID, err)
			} else {
				m.everConnected[dc.ID] = true
				log.Printf("[manager] %s: connected", dc.ID)
			}
		} else {
			log.Printf("[manager] %s: registered (lazy; connect explicitly to open %s)", dc.ID, dc.Port)
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

// AddDevice creates a new device at runtime and persists the config entry.
// The serial port is only opened immediately when auto-connect mode is active;
// otherwise the user connects explicitly from the UI.
func (m *Manager) AddDevice(dc config.DeviceConfig) error {
	dev, err := makeDevice(dc)
	if err != nil {
		return err
	}

	// Replace an existing instance cleanly so its old port handle is released
	// before the new driver opens the same (or a different) port.
	m.mu.Lock()
	old := m.devices[dc.ID]
	m.mu.Unlock()
	if old != nil {
		_ = old.Disconnect()
		m.mu.Lock()
		delete(m.states, dc.ID)
		delete(m.everConnected, dc.ID)
		delete(m.manualDisconnect, dc.ID)
		m.mu.Unlock()
	}

	m.registerSampleHandler(dev)
	if !m.lazy {
		if err := dev.Connect(); err != nil {
			log.Printf("[manager] %s: connect failed: %v", dc.ID, err)
		} else {
			m.markEverConnected(dc.ID)
		}
	} else {
		log.Printf("[manager] %s: registered (lazy; connect explicitly to open %s)", dc.ID, dc.Port)
	}
	m.mu.Lock()
	m.devices[dc.ID] = dev
	m.mu.Unlock()
	m.cfg.UpsertDevice(dc)
	return m.cfg.Save()
}

// registerSampleHandler wires a PowerMonitor's sample callback so every decoded
// sample is pushed to WebSocket clients in real time.
func (m *Manager) registerSampleHandler(dev device.Device) {
	pm, ok := dev.(device.PowerMonitor)
	if !ok {
		return
	}
	pm.SetSampleHandler(func(st *device.MonitorState) {
		m.mu.Lock()
		m.states[dev.ID()] = st
		m.mu.Unlock()
		m.broadcast(StateUpdate{
			DeviceID:   dev.ID(),
			DeviceType: dev.Type(),
			DeviceName: dev.Name(),
			State:      st,
		})
	})
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

// SetConfig replaces the configured device list.  It validates every entry
// (non-empty unique IDs, unique ports, known device type), reconciles the live
// device instances (removing, adding, or recreating drivers as needed), and
// persists the result.  Serial ports are only ever opened for devices that are
// part of the new configuration.
func (m *Manager) SetConfig(devices []config.DeviceConfig) error {
	// Validate everything up front so a bad entry cannot half-apply.
	ids := make(map[string]bool, len(devices))
	ports := make(map[string]bool, len(devices))
	for _, dc := range devices {
		if strings.TrimSpace(dc.ID) == "" {
			return fmt.Errorf("device id cannot be empty")
		}
		if ids[dc.ID] {
			return fmt.Errorf("duplicate device id %q", dc.ID)
		}
		ids[dc.ID] = true
		if dc.Port != "" {
			if ports[dc.Port] {
				return fmt.Errorf("serial port %q is assigned to more than one device", dc.Port)
			}
			ports[dc.Port] = true
		}
		if _, err := makeDevice(dc); err != nil {
			return err
		}
	}

	// Snapshot the old configuration and live instances for comparison.
	m.mu.Lock()
	old := make(map[string]config.DeviceConfig, len(m.cfg.Devices))
	for _, d := range m.cfg.Devices {
		old[d.ID] = d
	}
	live := make(map[string]device.Device, len(m.devices))
	for id, d := range m.devices {
		live[id] = d
	}
	m.mu.Unlock()

	// Tear down live instances that are gone, disabled, or need a driver
	// rebuild because their port/baud changed.
	for _, nd := range devices {
		if dev, ok := live[nd.ID]; ok {
			od := old[nd.ID]
			if !nd.Enabled || nd.Port != od.Port || nd.Baud != od.Baud {
				_ = dev.Disconnect()
				m.mu.Lock()
				delete(m.devices, nd.ID)
				delete(m.states, nd.ID)
				delete(m.manualDisconnect, nd.ID)
				delete(m.everConnected, nd.ID)
				m.mu.Unlock()
				delete(live, nd.ID)
			}
		}
	}
	for id, dev := range live {
		if !ids[id] {
			_ = dev.Disconnect()
			m.mu.Lock()
			delete(m.devices, id)
			delete(m.states, id)
			delete(m.renames, id)
			delete(m.manualDisconnect, id)
			delete(m.everConnected, id)
			m.mu.Unlock()
		}
	}

	// Register devices that are new or were torn down above.
	for _, dc := range devices {
		if !dc.Enabled {
			continue
		}
		m.mu.RLock()
		_, already := m.devices[dc.ID]
		m.mu.RUnlock()
		if already {
			// Only metadata changed; update the display name override.
			m.mu.Lock()
			if dc.Name != "" {
				m.renames[dc.ID] = dc.Name
			} else {
				delete(m.renames, dc.ID)
			}
			m.mu.Unlock()
			continue
		}
		dev, err := makeDevice(dc)
		if err != nil {
			return err
		}
		m.registerSampleHandler(dev)
		m.mu.Lock()
		m.devices[dc.ID] = dev
		if dc.Name != "" {
			m.renames[dc.ID] = dc.Name
		}
		delete(m.manualDisconnect, dc.ID)
		delete(m.everConnected, dc.ID)
		m.mu.Unlock()
		if !m.lazy {
			if err := dev.Connect(); err != nil {
				log.Printf("[manager] %s: connect failed: %v", dc.ID, err)
			} else {
				m.markEverConnected(dc.ID)
				log.Printf("[manager] %s: connected", dc.ID)
			}
		} else {
			log.Printf("[manager] %s: registered (lazy; connect explicitly to open %s)", dc.ID, dc.Port)
		}
	}

	// Persist the new device list, keeping the other config fields.
	m.mu.Lock()
	m.cfg.Devices = devices
	m.mu.Unlock()
	return m.cfg.Save()
}

// SetDeviceEnabled flips the enabled flag of an existing device entry and
// persists it.  Disabling tears down the live instance (closing its serial
// port); enabling registers a driver that the caller may then configure or
// connect.
func (m *Manager) SetDeviceEnabled(id string, enabled bool) error {
	m.mu.Lock()
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
		m.mu.Unlock()
		return fmt.Errorf("unknown device %q", id)
	}
	if dc.Enabled == enabled {
		m.mu.Unlock()
		return nil
	}
	dc.Enabled = enabled
	m.cfg.UpsertDevice(dc)
	dev, hasLive := m.devices[id]
	m.mu.Unlock()

	if !enabled {
		if hasLive {
			_ = dev.Disconnect()
			m.mu.Lock()
			delete(m.devices, id)
			delete(m.states, id)
			delete(m.manualDisconnect, id)
			delete(m.everConnected, id)
			m.mu.Unlock()
		}
	} else {
		m.mu.RLock()
		_, already := m.devices[id]
		m.mu.RUnlock()
		if !already {
			dev, err := makeDevice(dc)
			if err != nil {
				return err
			}
			m.registerSampleHandler(dev)
			m.mu.Lock()
			m.devices[id] = dev
			if dc.Name != "" {
				m.renames[id] = dc.Name
			}
			delete(m.manualDisconnect, id)
			delete(m.everConnected, id)
			m.mu.Unlock()
			if !m.lazy {
				if err := dev.Connect(); err != nil {
					log.Printf("[manager] %s: connect failed: %v", id, err)
				} else {
					m.markEverConnected(id)
				}
			}
		}
	}
	return m.cfg.Save()
}

// SetDeviceConfig updates a single device entry by ID and persists it.  Pass
// an empty port or a zero baud to keep the current values.  If the port/baud
// actually changed, the live device instance is rebuilt and reconnected
// (lazily in MCP mode); a purely metadata change leaves the connection
// untouched.
func (m *Manager) SetDeviceConfig(id, name, port string, baud int) error {
	m.mu.Lock()
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
		m.mu.Unlock()
		return fmt.Errorf("unknown device %q", id)
	}
	newName := dc.Name
	if name != "" {
		newName = name
	}
	if port == "" {
		port = dc.Port
	}
	if baud <= 0 {
		baud = dc.Baud
	}
	connChanged := port != dc.Port || baud != dc.Baud
	dev, hasLive := m.devices[id]
	m.mu.Unlock()

	// Reject a port that another device already owns.
	for _, d := range m.cfg.Devices {
		if d.ID != id && d.Port != "" && d.Port == port {
			return fmt.Errorf("serial port %q is already assigned to device %q", port, d.ID)
		}
	}

	if connChanged {
		if hasLive {
			_ = dev.Disconnect()
			m.mu.Lock()
			delete(m.devices, id)
			delete(m.states, id)
			delete(m.manualDisconnect, id)
			delete(m.everConnected, id)
			m.mu.Unlock()
		}
		m.mu.Lock()
		dc.Port = port
		dc.Baud = baud
		dc.Name = newName
		m.cfg.UpsertDevice(dc)
		delete(m.everConnected, id)
		m.mu.Unlock()

		newDev, err := makeDevice(dc)
		if err != nil {
			return err
		}
		m.registerSampleHandler(newDev)
		m.mu.Lock()
		m.devices[id] = newDev
		if newName != "" {
			m.renames[id] = newName
		}
		m.mu.Unlock()
		if !m.lazy {
			if err := newDev.Connect(); err != nil {
				log.Printf("[manager] %s: connect failed: %v", id, err)
			} else {
				m.markEverConnected(id)
			}
		}
	} else {
		m.mu.Lock()
		dc.Name = newName
		m.cfg.UpsertDevice(dc)
		if newName != "" {
			m.renames[id] = newName
		}
		m.mu.Unlock()
	}
	return m.cfg.Save()
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
	m.registerSampleHandler(newDev)
	m.mu.Lock()
	m.devices[id] = newDev         // register first so poll loop can see it
	delete(m.manualDisconnect, id) // user asked to reconnect — clear the block
	m.mu.Unlock()
	if err := newDev.Connect(); err != nil {
		return fmt.Errorf("connect %q: %w", id, err)
	}
	m.markEverConnected(id)
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
		delete(m.renames, id)
		delete(m.manualDisconnect, id)
		delete(m.everConnected, id)
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
	m.markEverConnected(id)
	ctx, cancel := context.WithTimeout(context.Background(), readDeviceTimeout)
	defer cancel()
	var st interface{}
	var err error
	switch d := dev.(type) {
	case device.PowerSupply:
		st, err = d.GetState(ctx)
	case device.USBHub:
		st, err = d.GetState(ctx)
	case device.AlarmLight:
		st, err = d.GetState(ctx)
	case device.PowerMonitor:
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
		m.markEverConnected(dev.ID())
		var state interface{}
		var err error
		switch d := dev.(type) {
		case device.PowerSupply:
			state, err = d.GetState(ctx)
		case device.USBHub:
			state, err = d.GetState(ctx)
		case device.AlarmLight:
			state, err = d.GetState(ctx)
		case device.PowerMonitor:
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
		id := dev.ID()
		if dev.IsConnected() {
			m.markEverConnected(id)
			continue
		}
		m.mu.RLock()
		// In lazy mode (the default), never open a port for a device that has
		// not been explicitly used in this process.  This is the guard that
		// keeps configured-but-unused (possibly stale) COM ports free for
		// other applications.  Auto-connect mode (--auto-connect) keeps the
		// old retry-everything behaviour.
		ever := m.everConnected[id]
		skip := m.manualDisconnect[id]
		lazy := m.lazy
		m.mu.RUnlock()
		if skip || (!ever && lazy) {
			continue
		}
		if err := dev.Connect(); err == nil {
			m.markEverConnected(id)
			log.Printf("[manager] %s: reconnected", id)
		}
	}
}

// markEverConnected records a successful, user-initiated (or auto-connect
// opt-in) open so the reconnect loop may revive it later.
func (m *Manager) markEverConnected(id string) {
	m.mu.Lock()
	if m.everConnected == nil {
		m.everConnected = make(map[string]bool)
	}
	m.everConnected[id] = true
	m.mu.Unlock()
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

	// Lazy-connect: if the device is registered but not connected, auto-connect now.
	m.mu.RLock()
	manualDisc := m.manualDisconnect[req.DeviceID]
	m.mu.RUnlock()
	if !dev.IsConnected() && !manualDisc {
		log.Printf("[manager] %s: lazy-connecting before command %q", req.DeviceID, req.Command)
		if err := dev.Connect(); err != nil {
			return fmt.Errorf("lazy-connect %s: %w", req.DeviceID, err)
		}
		m.markEverConnected(req.DeviceID)
		log.Printf("[manager] %s: lazy-connected", req.DeviceID)
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

	case "release_control":
		// Hand the front panel back without disconnecting, so polling (and
		// therefore monitoring) continues.  The next set command re-takes
		// control on devices that support it.
		rr, ok := dev.(device.ControlReleaser)
		if !ok {
			return fmt.Errorf("%s does not support releasing front-panel control", req.DeviceID)
		}
		if err := rr.ReleaseControl(ctx); err != nil {
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

	case "set_all_usb_ports":
		hub, ok := dev.(device.USBHub)
		if !ok {
			return fmt.Errorf("%s is not a USB hub", req.DeviceID)
		}
		on, err := boolParam(req.Params, "on")
		if err != nil {
			return err
		}
		if err := hub.SetAllPorts(ctx, on); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

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

	// ── alarm light ───────────────────────────────────────────────────────────
	case "alarm_set_color":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		color, err := stringParam(req.Params, "color")
		if err != nil {
			return err
		}
		mode, _ := optionalStringParam(req.Params, "mode")
		if mode == "" {
			mode = "on"
		}
		if mode == "animation" {
			color2, err := stringParam(req.Params, "color2")
			if err != nil {
				return fmt.Errorf("animation requires color2: %w", err)
			}
			durationMs := 2000
			if v, ok := req.Params["duration"]; ok {
				switch n := v.(type) {
				case float64:
					durationMs = int(n)
				case int:
					durationMs = n
				}
			}
			loop := -1
			if v, ok := req.Params["loop"]; ok {
				switch n := v.(type) {
				case float64:
					loop = int(n)
				case int:
					loop = n
				}
			}
			go func() {
				_ = al.Fade(ctx, color, color2, durationMs, loop)
				m.pollDevice(req.DeviceID)
			}()
			return nil
		}
		if err := al.SetColor(ctx, color, mode); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "alarm_buzzer":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		on, err := boolParam(req.Params, "on")
		if err != nil {
			return err
		}
		if err := al.Buzzer(ctx, on); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "alarm_off":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		if err := al.Off(ctx); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "alarm_beep":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		color, err := stringParam(req.Params, "color")
		if err != nil {
			return err
		}
		ms, err := intParam(req.Params, "ms")
		if err != nil {
			return err
		}
		// Beep is blocking (sleeps ms), run in background
		go func() {
			_ = al.Beep(ctx, color, ms)
			m.pollDevice(req.DeviceID)
		}()
		return nil

	case "alarm_pwm":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		r, _ := intParamOptional(req.Params, "red")
		g, _ := intParamOptional(req.Params, "green")
		b, _ := intParamOptional(req.Params, "blue")
		flash, _ := intParamOptional(req.Params, "flash_interval")
		buzz, _ := boolParamOptional(req.Params, "buzzer")
		if err := al.PWM(ctx, r, g, b, flash, buzz); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "alarm_rainbow":
		al, ok := dev.(device.AlarmLight)
		if !ok {
			return fmt.Errorf("%s is not an alarm light", req.DeviceID)
		}
		durationMs := 2000
		if v, ok := req.Params["duration"]; ok {
			switch n := v.(type) {
			case float64:
				durationMs = int(n)
			case int:
				durationMs = n
			}
		}
		go func() {
			_ = al.Rainbow(ctx, durationMs)
			m.pollDevice(req.DeviceID)
		}()
		return nil

	// ── power monitor (F-11) ────────────────────────────────────────────────
	case "monitor_set_interval":
		pm, ok := dev.(device.PowerMonitor)
		if !ok {
			return fmt.Errorf("%s is not a power monitor", req.DeviceID)
		}
		ms, err := intParam(req.Params, "interval_ms")
		if err != nil {
			return err
		}
		if err := pm.SetInterval(ctx, ms); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "monitor_set_print":
		pm, ok := dev.(device.PowerMonitor)
		if !ok {
			return fmt.Errorf("%s is not a power monitor", req.DeviceID)
		}
		mode, err := intParam(req.Params, "mode")
		if err != nil {
			return err
		}
		if err := pm.SetPrint(ctx, mode); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "monitor_start":
		pm, ok := dev.(device.PowerMonitor)
		if !ok {
			return fmt.Errorf("%s is not a power monitor", req.DeviceID)
		}
		if err := pm.StartMonitor(ctx); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

	case "monitor_stop":
		pm, ok := dev.(device.PowerMonitor)
		if !ok {
			return fmt.Errorf("%s is not a power monitor", req.DeviceID)
		}
		if err := pm.StopMonitor(ctx); err != nil {
			return err
		}
		go m.pollDevice(req.DeviceID)
		return nil

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
	case "usbalarm":
		return usbalarm.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	case "sangenxian":
		return sangenxian.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	case "f11":
		return f11.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
	case "powerz":
		return powerz.New(dc.ID, dc.Name, dc.Port, dc.Baud), nil
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

func stringParam(p map[string]interface{}, key string) (string, error) {
	v, ok := p[key]
	if !ok {
		return "", fmt.Errorf("missing param %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("param %q must be a string", key)
	}
	return s, nil
}

func optionalStringParam(p map[string]interface{}, key string) (string, bool) {
	v, ok := p[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func intParamOptional(p map[string]interface{}, key string) (int, bool) {
	v, ok := p[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func boolParamOptional(p map[string]interface{}, key string) (bool, bool) {
	v, ok := p[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}
