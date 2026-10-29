package manager

import (
	"context"
	"sync"
	"testing"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/device"
)

type fakeHub struct {
	mu        sync.Mutex
	connected bool
	allOn     bool
	allCalls  int
	ports     []bool
}

func (f *fakeHub) ID() string   { return "fakehub" }
func (f *fakeHub) Name() string { return "Fake Hub" }
func (f *fakeHub) Type() string { return "fake_hub" }
func (f *fakeHub) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = true
	return nil
}
func (f *fakeHub) Disconnect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	return nil
}
func (f *fakeHub) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}
func (f *fakeHub) SetPort(_ context.Context, port int, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ports[port-1] = on
	return nil
}
func (f *fakeHub) SetAllPorts(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allCalls++
	f.allOn = on
	for i := range f.ports {
		f.ports[i] = on
	}
	return nil
}
func (f *fakeHub) GetState(context.Context) (*device.HubState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &device.HubState{
		Connected: f.connected,
		Ports:     append([]bool(nil), f.ports...),
		PortCount: len(f.ports),
	}, nil
}
func (f *fakeHub) stats() (int, bool, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allCalls, f.allOn, append([]bool(nil), f.ports...)
}
func (f *fakeHub) SetLock(context.Context, bool) error     { return nil }
func (f *fakeHub) SetHWKeys(context.Context, bool) error   { return nil }
func (f *fakeHub) SetAutoSave(context.Context, bool) error { return nil }
func (f *fakeHub) SetDefaultOn(context.Context, byte) error {
	return nil
}

var _ device.USBHub = (*fakeHub)(nil)

func TestSetAllUSBPortsCommand(t *testing.T) {
	m := New(config.Default())
	f := &fakeHub{connected: true, ports: make([]bool, 12)}
	m.devices["fakehub"] = f

	for i, on := range []bool{true, false} {
		err := m.execCmd(CmdRequest{
			DeviceID: "fakehub",
			Command:  "set_all_usb_ports",
			Params:   map[string]interface{}{"on": on},
		})
		if err != nil {
			t.Fatalf("call %d (on=%v): %v", i, on, err)
		}
		calls, allOn, ports := f.stats()
		if calls != i+1 {
			t.Errorf("call %d: SetAllPorts called %d times", i, calls)
		}
		if allOn != on {
			t.Errorf("call %d: allOn=%v, want %v", i, allOn, on)
		}
		for p, state := range ports {
			if state != on {
				t.Errorf("call %d: port %d = %v, want %v", i, p+1, state, on)
			}
		}
	}
}
