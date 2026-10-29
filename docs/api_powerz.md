# POWER-Z Power Monitor REST API

POWER-Z (ChargerLAB KM002C / KM003C) is a USB-C power meter that reports bus
voltage, current, temperature and CC/D+/D- levels over a vendor binary
protocol. This driver talks to the device through its CDC-ACM virtual serial
port, which Windows 10/11 enumerate as a COM port without any driver install.

**Device type:** `powerz`  
**Interface:** `device.PowerMonitor`  
**Default baud:** 115200 (the device ignores the value)  
**Protocol:** binary command frames over USB CDC (see [protocol summary](#serial-protocol-summary))

---

## Device Config

Add a POWER-Z device via `POST /api/config/device`:

```json
{
  "id": "POWERZ",
  "name": "POWER-Z",
  "type": "powerz",
  "port": "COM65",
  "baud": 115200,
  "enabled": true
}
```

| Field     | Type   | Description                                  |
|-----------|--------|----------------------------------------------|
| `id`      | string | Unique device identifier                     |
| `name`    | string | Display name                                 |
| `type`    | string | Must be `"powerz"`                           |
| `port`    | string | CDC port (e.g. `"COM65"`, `/dev/ttyACM0`)     |
| `baud`    | int    | Baud rate; ignored by the device, `0` = 115200 |
| `enabled` | bool   | Whether device is active                     |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"POWERZ","name":"POWER-Z","type":"powerz","port":"COM65","baud":115200,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## State (MonitorState)

`GET /api/device/POWERZ/state` or pushed live over the WebSocket:

```json
{
  "connected": true,
  "current": 0.000098,
  "voltage": 4.257111,
  "monitoring": false,
  "avg_current": 0,
  "avg_voltage": 0,
  "peak_current": 0,
  "peak_voltage": 0,
  "sample_count": 0,
  "interval_ms": 500
}
```

| Field          | Type   | Description                                        |
|----------------|--------|----------------------------------------------------|
| `connected`    | bool   | Serial connection state                            |
| `current`      | float  | Latest bus current (A, signed by direction)        |
| `voltage`      | float  | Latest bus voltage (V)                             |
| `monitoring`   | bool   | Whether average/peak accumulation is running       |
| `avg_current`  | float  | Running average current since `monitor_start` (A)  |
| `avg_voltage`  | float  | Running average voltage since `monitor_start` (V)  |
| `peak_current` | float  | Peak current since `monitor_start` (A)             |
| `peak_voltage` | float  | Peak voltage since `monitor_start` (V)             |
| `sample_count` | int    | Samples accumulated in the current monitor period  |
| `interval_ms`  | int    | Host sampling interval (10–10000 ms)               |

> Samples are pushed to WebSocket clients in real time via the driver's sample
> handler — the UI updates at the sampling interval, not the 1 s manager poll.

---

## Commands

All commands are `POST /api/device/{id}/cmd` with a JSON body, or via WebSocket
`{"type":"cmd", ...}`.

### `monitor_set_interval` — set sampling interval

```json
{"command": "monitor_set_interval", "params": {"interval_ms": 10}}
```

Clamped to 10–10000 ms. The POWER-Z has no continuous-stream mode in this
driver: the interval controls how often the host sends `GetData`, so the
achievable rate is limited by the transaction time (≈8 ms per sample over CDC
in measurement mode).

### `monitor_start` — start averaging period

```json
{"command": "monitor_start", "params": {}}
```

Resets statistics and begins accumulating average/peak over every decoded
sample.

### `monitor_stop` — stop averaging period

```json
{"command": "monitor_stop", "params": {}}
```

Freezes the average/peak values (they remain displayed until the next start).

### `monitor_set_print` — no-op

Accepted for UI compatibility; the POWER-Z protocol has no print-mode setting.

---

## Web UI

The POWER-Z uses the shared power-monitor page:

- **Simple mode (folded):** live voltage/current/power cards at the 500 ms
  sampling interval.
- **Measurement mode (expanded):** switches to 10 ms sampling and reveals
  average/peak cards, a Start/Stop averaging button, and a real-time
  voltage/current curve chart.

---

## Serial protocol summary

Command header (4 bytes, little-endian bit fields):

```text
byte 0: type:7 | reserved:1        Connect 0x02, Disconnect 0x03, GetData 0x0C
byte 1: transaction id (rolling 0-255)
byte 2-3: attribute:15 << 1 | unused:1
```

Get ADC (attribute `0x0001` → wire value `0x0002`):

```text
host → device:  0C <tid> 02 00
device → host:  41 <tid> <count:2>            PutData
                01/00 00/0B  ...               ext header: attr=ADC, next=0, size=44
                <44-byte ADC payload>
```

The extended header packs `attr:15 | next:1 | chunk:6 | size:10`; `size` gives
the payload length, so chained logical packets are delimited without relying on
the top-level object count.

ADC payload (44 bytes):

| Offset | Type | Field        | Unit                        |
|--------|------|--------------|-----------------------------|
| 0      | i32  | Vbus         | µV                          |
| 4      | i32  | Ibus         | µA (signed, positive = female→male) |
| 8      | i32  | Vbus_avg     | µV (filtered)               |
| 12     | i32  | Ibus_avg     | µA (filtered)               |
| 16     | i32  | Vbus_ori_avg | µV (uncalibrated)           |
| 20     | i32  | Ibus_ori_avg | µA (uncalibrated)           |
| 24     | i16  | Temp         | 1/128 °C                    |
| 26     | u16  | Vcc1         | 0.1 mV                      |
| 28     | u16  | Vcc2         | 0.1 mV                      |
| 30     | u16  | Vdp (D+)     | 0.1 mV                      |
| 32     | u16  | Vdm (D−)     | 0.1 mV                      |
| 34     | u16  | Vdd          | 0.1 mV                      |
| 36     | u8   | sample rate index | 0–4                    |
| 37     | u8   | flags        | status flags                |
| 38     | u16  | CC2_avg      | mV                          |
| 40     | u16  | D+_avg       | mV                          |
| 42     | u16  | D−_avg       | mV                          |

Other commands/attributes documented by the vendor (not yet used by this
driver): `0x0E StartGraph` / `0x0F StopGraph` for AdcQueue streaming,
`0x10/0x11` PD monitor enable/disable, `ATT_SETTINGS 0x008`,
`ATT_PD_PACKET 0x010`, `ATT_PD_STATUS 0x020`.

### Interfaces

A KM002C/KM003C exposes four USB interfaces with the same command set:

| Interface | Class | Endpoints | Notes |
|-----------|-------|-----------|-------|
| IF0 | vendor bulk | `0x01` OUT / `0x81` IN | Full feature set (AdcQueue streaming, auth) |
| IF1+2 | CDC ACM | `0x02`/`0x82`, `0x83` | Used by this driver; no driver install needed |
| IF3 | HID | `0x05`/`0x85` | Driverless alternative, 64-byte transfers |

**USB IDs:** `0x5FC9` ChargerLAB; PID `0x0061` = KM002C, `0x0063` = KM003C.

---

## MCP

- `powerwing_test_connection` with `device_type: "powerz"` checks configured
  POWER-Z devices on their configured ports (Connect + GetData probe; only
  configured ports are opened).
- `powerwing_send_command` accepts the three `monitor_*` commands above.

---

## Verified

Tested against a POWER-Z KM002C (PID `0x0061`) on Windows via its CDC port:
Connect + `GetData` returned 4.257 V / 98 µA, and 10 ms sampling produced
≈120 samples/s with average/peak accumulation working.
