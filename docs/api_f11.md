# F-11 Power Monitor REST API

F-11 (AFU IoT) is a high-precision current/voltage meter that streams continuous ASCII measurement lines over USB serial (38400 baud, 8N1).

**Device type:** `f11`  
**Interface:** `device.PowerMonitor`  
**Default baud:** 38400  
**Protocol:** ASCII commands (`sys ...`) + streamed `<current>,<voltage>` lines (see [f11_afu_iot_protocol_skill.md](../f11_afu_iot_protocol_skill.md))

---

## Device Config

Add an F-11 device via `POST /api/config/device`:

```json
{
  "id": "meter1",
  "name": "F-11 Meter",
  "type": "f11",
  "port": "COM7",
  "baud": 38400,
  "enabled": true
}
```

| Field     | Type   | Description                                |
|-----------|--------|--------------------------------------------|
| `id`      | string | Unique device identifier                   |
| `name`    | string | Display name                               |
| `type`    | string | Must be `"f11"`                            |
| `port`    | string | Serial port (e.g. `"COM7"`, `/dev/ttyUSB3`) |
| `baud`    | int    | Baud rate; `0` or `38400` = default        |
| `enabled` | bool   | Whether device is active                   |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"meter1","name":"F-11 Meter","type":"f11","port":"COM7","baud":38400,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## State (MonitorState)

`GET /api/device/meter1/state` or pushed live over the WebSocket:

```json
{
  "connected": true,
  "current": 0.5213,
  "voltage": 5.002,
  "monitoring": true,
  "avg_current": 0.5201,
  "avg_voltage": 5.001,
  "peak_current": 0.6120,
  "peak_voltage": 5.014,
  "sample_count": 1583,
  "interval_ms": 10
}
```

| Field          | Type   | Description                                        |
|----------------|--------|----------------------------------------------------|
| `connected`    | bool   | Serial connection state                            |
| `current`      | float  | Latest current sample (A)                          |
| `voltage`      | float  | Latest voltage sample (V)                          |
| `monitoring`   | bool   | Whether average/peak accumulation is running       |
| `avg_current`  | float  | Running average current since `monitor_start` (A)  |
| `avg_voltage`  | float  | Running average voltage since `monitor_start` (V)  |
| `peak_current` | float  | Peak current since `monitor_start` (A)             |
| `peak_voltage` | float  | Peak voltage since `monitor_start` (V)             |
| `sample_count` | int    | Samples accumulated in the current monitor period   |
| `interval_ms`  | int    | Device sample interval (10–10000 ms)               |

> Samples are pushed to WebSocket clients in real time via the driver's sample
> handler — no polling latency.

---

## Commands

All commands are `POST /api/device/{id}/cmd` with a JSON body, or via WebSocket
`{"type":"cmd", ...}`.

### `monitor_set_interval` — set sample interval

```json
{"command": "monitor_set_interval", "params": {"interval_ms": 10}}
```

Clamped to 10–10000 ms. Sends `sys interval <ms>` to the device.
The UI uses 500 ms for simple meter mode and 10 ms for measurement mode.

### `monitor_set_print` — set print mode

```json
{"command": "monitor_set_print", "params": {"mode": 2}}
```

`0` = off, `1` = current only, `2` = current + voltage. The driver sets `2` on connect.

### `monitor_start` — start averaging period

```json
{"command": "monitor_start", "params": {}}
```

Resets statistics and begins accumulating average/peak over every decoded
sample at the configured interval (10 ms in measurement mode).

### `monitor_stop` — stop averaging period

```json
{"command": "monitor_stop", "params": {}}
```

Freezes the average/peak values (they remain displayed until the next start).

---

## Web UI

- **Simple mode (folded):** live voltage/current/power cards refreshed at the
  500 ms sample interval.
- **Measurement mode (expanded):** the expand/fold button switches the device
  to a 10 ms sample interval and reveals:
  - Average / peak voltage & current cards
  - Start/Stop button controlling the averaging period
  - Real-time voltage & current curve chart (canvas) with independent
    show/hide checkboxes for each curve

---

## Serial protocol summary

```
sys interval <10-10000>   set sample interval (ms)
sys setprint <0|1|2>      0=off 1=current 2=current+voltage
sys baudrate <0|1|2>      0=9600 1=19200 2=38400
sys setcal <uA>           current calibration
sys setled <0|1>          LED mode
sys setprotect <1-200ms>  current protection time
sys reset / sys version / sys info / sys recovery
```

Streamed measurement lines: `<current>,<voltage>` (e.g. `0000521,00500`).
The driver scales current from µA and voltage from mV.

---

## MCP

- `powerwing_test_connection` with `device_type: "f11"` checks configured F11 devices on their configured ports at 38400 (non-configured ports are never opened).
- `powerwing_send_command` accepts the four `monitor_*` commands above.
