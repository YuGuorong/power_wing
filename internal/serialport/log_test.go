package serialport

import "testing"

func TestOpenFailureIsLogged(t *testing.T) {
	ClearLog()
	if _, err := OpenFor("test-device", "__NO_SUCH_SERIAL_PORT__", Mode(9600)); err == nil {
		t.Fatal("expected an error opening a nonexistent port")
	}
	events := Log()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Action != "open_failed" {
		t.Errorf("action = %q, want open_failed", ev.Action)
	}
	if ev.Owner != "test-device" || ev.Port != "__NO_SUCH_SERIAL_PORT__" || ev.Baud != 9600 {
		t.Errorf("unexpected event: %+v", ev)
	}
	if ev.Caller == "" || ev.Error == "" {
		t.Errorf("expected caller and error, got %+v", ev)
	}
}

func TestEmptyPortIsRejected(t *testing.T) {
	ClearLog()
	if _, err := Open("", Mode(9600)); err == nil {
		t.Fatal("expected an error for an empty port name")
	}
	if events := Log(); len(events) != 0 {
		t.Errorf("empty port should not be logged as an open attempt: %+v", events)
	}
}

func TestClearLog(t *testing.T) {
	ClearLog()
	_, _ = OpenFor("x", "__NO_SUCH_SERIAL_PORT__", Mode(4800))
	if len(Log()) == 0 {
		t.Fatal("expected at least one event")
	}
	ClearLog()
	if len(Log()) != 0 {
		t.Fatal("ClearLog did not clear the event log")
	}
}
