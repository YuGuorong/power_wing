// Package manager owns the lifecycle of all configured devices and acts as the
// single source of truth for device state.  It exposes a subscription channel
// so callers (e.g., the HTTP server) can fan out state updates to WebSocket
// clients.
package manager

import (
	"context"
	"fmt"
	"log"
	"math/rand"
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

// cycleState tracks the runtime state of a port's cycle loop.
type cycleState struct {
	cancel context.CancelFunc
	paused bool
	phase  string  // "on", "off", "paused", ""
	remain float64 // seconds remaining
	total  float64 // total seconds for current phase (random set value)
	mu     sync.Mutex
}

// cycleKey builds a unique key for a device + port.
func cycleKey(id string, port int) string {
	return fmt.Sprintf("%s:%d", id, port)
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

	cycleMu   sync.Mutex
	cycleState map[string]*cycleState // key: "id:port"

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
		cycleState:       make(map[string]*cycleState),
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
		// Start per-port cycle loops
		dc.EnsureCycles()
		for port := 0; port < dc.NumCyclePorts(); port++ {
			if dc.Cycles[port].Enabled {
				m.startCycleLocked(dc.ID, port)
			}
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
	m.stopAllCycle()
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
	// Ensure cycles are initialized.
	dc.EnsureCycles()
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
	m.stopAllDeviceCycle(id)
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
	m.stopAllDeviceCycle(id)
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

// ─── cycle control ─────────────────────────────────────────────────────────────

// GetCycleConfig returns the cycle config for a device+port.
// port=0 for PSU, 1-4 for hub.
func (m *Manager) GetCycleConfig(id string, port int) (*config.CycleConfig, error) {
	for _, d := range m.cfg.Devices {
		if d.ID == id {
			cc := d.GetCycle(port)
			return &cc, nil
		}
	}
	return nil, fmt.Errorf("unknown device %q", id)
}

// SetCycleConfig updates the cycle config for a device+port and persists it.
// port=0 for PSU, 1-4 for hub.
func (m *Manager) SetCycleConfig(id string, port int, cc config.CycleConfig) error {
	found := false
	for i, d := range m.cfg.Devices {
		if d.ID == id {
			m.cfg.Devices[i].SetCycle(port, cc)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown device %q", id)
	}
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if cc.Enabled {
		m.startCycle(id, port)
	} else {
		m.stopCycle(id, port)
	}
	go m.pollDevice(id)
	return nil
}

func (m *Manager) startCycle(id string, port int) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	m.startCycleLocked(id, port)
}

func (m *Manager) startCycleLocked(id string, port int) {
	key := cycleKey(id, port)
	if cs, ok := m.cycleState[key]; ok && cs.cancel != nil {
		cs.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cycleState[key] = &cycleState{cancel: cancel, paused: false, phase: "", remain: 0}
	m.wg.Add(1)
	go m.cycleLoop(ctx, id, port)
	log.Printf("[manager] %s port %d: cycle started", id, port)
}

func (m *Manager) stopCycle(id string, port int) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	key := cycleKey(id, port)
	if cs, ok := m.cycleState[key]; ok {
		cs.mu.Lock()
		if cs.cancel != nil {
			cs.cancel()
			cs.cancel = nil
		}
		cs.phase = ""
		cs.remain = 0
		cs.mu.Unlock()
		delete(m.cycleState, key)
		log.Printf("[manager] %s port %d: cycle stopped", id, port)
	}
}

// stopAllCycle cancels all running cycle loops.
func (m *Manager) stopAllCycle() {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	for key, cs := range m.cycleState {
		cs.mu.Lock()
		if cs.cancel != nil {
			cs.cancel()
		}
		cs.mu.Unlock()
		delete(m.cycleState, key)
	}
}

// stopAllDeviceCycle stops all cycle loops for a given device (all ports).
func (m *Manager) stopAllDeviceCycle(id string) {
	for port := 0; port < 4; port++ {
		m.stopCycle(id, port)
	}
}

// cycleLoop is the per-port goroutine that toggles output on/off at random
// intervals within the configured duration ranges.
// port=0 for PSU, 1-4 for hub port.
func (m *Manager) cycleLoop(ctx context.Context, id string, port int) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		key := cycleKey(id, port)
		m.cycleMu.Lock()
		cs, ok := m.cycleState[key]
		m.cycleMu.Unlock()
		if !ok {
			return
		}

		cs.mu.Lock()
		paused := cs.paused
		cs.mu.Unlock()
		if paused {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		cc, err := m.GetCycleConfig(id, port)
		if err != nil || !cc.Enabled {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// ── ON phase ────────────────────────────────────────────────────────
		onDur := randomDur(cc.OnMin, cc.OnMax)
		cs.mu.Lock()
		cs.phase = "on"
		cs.remain = onDur
		cs.total = onDur
		cs.mu.Unlock()

		m.mu.RLock()
		dev, devOk := m.devices[id]
		m.mu.RUnlock()
		if devOk {
			m.setCyclePort(ctx, dev, port, true)
			go m.pollDevice(id)
		}

		// Sleep with decrementing remain every 100ms
		if !sleepDecrement(ctx, cs, onDur) {
			return
		}

		// ── OFF phase ───────────────────────────────────────────────────────
		offDur := randomDur(cc.OffMin, cc.OffMax)
		cs.mu.Lock()
		cs.phase = "off"
		cs.remain = offDur
		cs.total = offDur
		cs.mu.Unlock()

		if devOk {
			m.setCyclePort(ctx, dev, port, false)
			go m.pollDevice(id)
		}

		if !sleepDecrement(ctx, cs, offDur) {
			return
		}
	}
}

// injectCycleState enriches a PowerState or HubState with cycle-switch status fields.
func (m *Manager) injectCycleState(id string, st interface{}) interface{} {
	switch v := st.(type) {
	case *device.PowerState:
		key := cycleKey(id, 0)
		m.cycleMu.Lock()
		cs, has := m.cycleState[key]
		m.cycleMu.Unlock()
		if has {
			cs.mu.Lock()
			v.CycleEnabled = true
			if cs.paused {
				v.CyclePhase = "paused"
				v.CycleRemain = 0
				v.CycleTotal = 0
			} else {
				v.CyclePhase = cs.phase
				v.CycleRemain = cs.remain
				v.CycleTotal = cs.total
			}
			cs.mu.Unlock()
		} else {
			v.CycleEnabled = false
			v.CyclePhase = ""
			v.CycleRemain = 0
			v.CycleTotal = 0
		}

	case *device.HubState:
		for port := 1; port <= 4; port++ {
			key := cycleKey(id, port)
			m.cycleMu.Lock()
			cs, has := m.cycleState[key]
			m.cycleMu.Unlock()
			idx := port - 1
			v.CyclePerPort[idx].Port = port
			if has {
				cs.mu.Lock()
				v.CyclePerPort[idx].Enabled = true
				if cs.paused {
					v.CyclePerPort[idx].Phase = "paused"
					v.CyclePerPort[idx].Remain = 0
					v.CyclePerPort[idx].Total = 0
				} else {
					v.CyclePerPort[idx].Phase = cs.phase
					v.CyclePerPort[idx].Remain = cs.remain
					v.CyclePerPort[idx].Total = cs.total
				}
				cs.mu.Unlock()
			} else {
				v.CyclePerPort[idx].Enabled = false
				v.CyclePerPort[idx].Phase = ""
				v.CyclePerPort[idx].Remain = 0
				v.CyclePerPort[idx].Total = 0
			}
		}
	}
	return st
}

// setCyclePort toggles a single port on/off. For PSU, port is ignored.
func (m *Manager) setCyclePort(ctx context.Context, dev device.Device, port int, on bool) {
	switch d := dev.(type) {
	case device.PowerSupply:
		_ = d.SetOutput(ctx, on)
	case device.USBHub:
		_ = d.SetPort(ctx, port, on)
	}
}

// randomDur returns a random float in [min, max]. If min >= max, returns min.
func randomDur(min, max float64) float64 {
	if min >= max {
		return min
	}
	return min + rand.Float64()*(max-min)
}

// waitCtx sleeps for d or returns false if ctx is cancelled.
func waitCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// sleepDecrement sleeps for dur seconds while decrementing cs.remain every
// tickInterval (100ms) so that polled state reflects the actual remaining time.
// Returns false if the context was cancelled.
func sleepDecrement(ctx context.Context, cs *cycleState, dur float64) bool {
	const tickInterval = 100 * time.Millisecond
	remaining := dur
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(tickInterval):
			remaining -= float64(tickInterval) / float64(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			cs.mu.Lock()
			cs.remain = remaining
			cs.mu.Unlock()
		}
	}
	return true
}

// pauseCycle marks a port's cycle loop as paused.
// For PSU, port is 0.
func (m *Manager) pauseCycle(id string, port int) {
	key := cycleKey(id, port)
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	if cs, ok := m.cycleState[key]; ok {
		cs.mu.Lock()
		cs.paused = true
		cs.phase = "paused"
		cs.remain = 0
		cs.mu.Unlock()
	}
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
	st = m.injectCycleState(id, st)
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
		state = m.injectCycleState(dev.ID(), state)
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
		m.pauseCycle(req.DeviceID, 0)
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
		m.pauseCycle(req.DeviceID, port)
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
