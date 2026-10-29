package serialport

import (
	"fmt"
	"log"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event records one serial-port lifecycle operation.  The UI's Settings →
// Serial Port Debug panel displays these so it is always clear which device
// opened which port, when, and from where.
type Event struct {
	Time   string `json:"time"`
	Action string `json:"action"` // open | open_failed | close
	Owner  string `json:"owner,omitempty"`
	Port   string `json:"port"`
	Baud   int    `json:"baud,omitempty"`
	Caller string `json:"caller,omitempty"`
	Error  string `json:"error,omitempty"`
}

const maxEvents = 1000

var (
	logMu    sync.Mutex
	eventLog []Event
)

// Log returns a copy of the recorded serial-port events, oldest first.
func Log() []Event {
	logMu.Lock()
	defer logMu.Unlock()
	out := make([]Event, len(eventLog))
	copy(out, eventLog)
	return out
}

// ClearLog drops all recorded events.
func ClearLog() {
	logMu.Lock()
	eventLog = nil
	logMu.Unlock()
}

func addEvent(ev Event) {
	ev.Time = time.Now().Format("2006-01-02 15:04:05.000")
	logMu.Lock()
	if len(eventLog) >= maxEvents {
		// Drop the oldest entry.  Shifting 1000 elements is cheap enough at
		// the rate serial ports are opened.
		eventLog = append(eventLog[:0], eventLog[1:]...)
	}
	eventLog = append(eventLog, ev)
	logMu.Unlock()

	if ev.Error != "" {
		log.Printf("[serial] %s port=%s owner=%s caller=%s error=%s",
			ev.Action, ev.Port, ev.Owner, ev.Caller, ev.Error)
	} else {
		log.Printf("[serial] %s port=%s owner=%s caller=%s",
			ev.Action, ev.Port, ev.Owner, ev.Caller)
	}
}

// callerAt returns a short "package/file.go:line" for the caller at the given
// stack depth, used to identify which code path opened a port.
func callerAt(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return ""
	}
	if i := strings.LastIndex(file, "/"); i >= 0 {
		return fmt.Sprintf("%s:%d", file[i+1:], line)
	}
	return file + ":" + strconv.Itoa(line)
}
