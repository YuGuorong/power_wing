# PowerWing API Reference — REST, WebSocket, and MCP

**Applies to:** PowerWing 1.2.0 (2026-10-30)
**Default HTTP address:** `http://<host>:8765`
**Content type:** `application/json` (except the embedded web UI)

This document describes everything an external client can call:

- the **REST API** (`/api/...`),
- the **WebSocket** push channel (`/ws`),
- the **MCP server** (stdio, raw TCP, and Streamable HTTP),
- the **device command set** shared by `POST /api/device/{id}/cmd` and MCP
  `powerwing_send_command`,
- the **state objects** returned for each device type.

Per-device, protocol-level notes live in the sibling documents
([api_spm3051.md](api_spm3051.md), [api_powerz.md](api_powerz.md),
[api_f11.md](api_f11.md), [api_sangenxian.md](api_sangenxian.md),
[api_usbslim.md](api_usbslim.md), [api_usbalarm.md](api_usbalarm.md),
[api_ftdi_vbus.md](api_ftdi_vbus.md)).

---

## 1. Conventions

| Item | Value |
|------|-------|
| Bind address | `--host` (default `0.0.0.0`) and `--port` (default `8765`) |
| Auth | None. The server is intended for a trusted LAN. Bind `127.0.0.1` to restrict it. |
| Device ID | The configured `id` (e.g. `psu1`, `hub1`); case-sensitive |
| Errors | HTTP 4xx/5xx with body `{"error": "<message>"}` |
| Time format | Local time, `YYYY-MM-DD HH:MM:SS.mmm` |

Device types and their defaults:

| `type` | Class | Default baud | Notes |
|--------|-------|--------------|-------|
| `spm3051` | power supply + meter | 9600 | OWON PSU with built-in 4½-digit DMM |
| `pd_pocket` | power supply | 115200 | no OVP; `set_ocp` maps to current limit |
| `usbslim` | USB hub | 9600 | 4 ports |
| `sangenxian` | USB hub | 4800 | 12 ports; lock/HW-keys/auto-save unsupported |
| `usbalarm` | alarm light | 4800 | Modbus RTU RGB beacon |
| `f11` | power monitor | 38400 | AFU IoT current/voltage meter |
| `powerz` | power monitor | 115200 (ignored) | ChargerLAB KM002C/KM003C |
| `ftdi_vbus` | VBUS control | — | FTDI FT230XQ CBUS pin over D2XX; Windows only, no COM port |

`baud: 0` in a configuration means "use the driver default".

An `ftdi_vbus` entry uses `serial`, `pin` and `active_low` instead of
`port`/`baud` — see [api_ftdi_vbus.md](api_ftdi_vbus.md).

---

## 2. REST API

### 2.1 `GET /api/devices`

Returns every configured device with its latest polled state (when available).

```json
[
  {
    "id": "psu1",
    "type": "spm3051",
    "name": "Lab PSU",
    "connected": true,
    "port": "COM3",
    "baud": 9600,
    "state": { "...": "see §5" }
  }
]
```

`state` is omitted until the device has been polled at least once.

### 2.2 `GET /api/device/{id}/state`

Returns the most recent state object for a device (see §5).
Returns `404 {"error":"device not found"}` when no state exists yet.

```bash
curl http://localhost:8765/api/device/psu1/state
```

### 2.3 `POST /api/device/{id}/cmd`

Executes a device command (see §4). The response only confirms execution; the
new state is pushed over the WebSocket and is visible on the next poll.

Request:

```json
{ "command": "set_volt", "params": { "value": 5.0 } }
```

Response: `{"status":"ok"}` (HTTP 200), or HTTP 500 with `{"error":"..."}`.

```bash
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_volt","params":{"value":5.0}}'
```

A command on a device that is registered but disconnected **lazy-connects** it
first (unless it was explicitly disconnected by the user). For `ftdi_vbus`,
"connect" only checks that D2XX is usable — no board is opened.

#### 2.3.1 FTDI VBUS-only endpoints

`ftdi_vbus` controllers hold no connection. These two actions support the
board dropdown and the transient state read; both open the board only for the
duration of the request.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/device/{id}/boards` | List the FTDI boards D2XX can name (never opens one) |
| GET | `/api/device/{id}/vbus-state?serial=...` | One transient open/read/close; returns a `VBusState` (§5.5) |

```bash
curl http://localhost:8765/api/device/vbus1/boards
curl "http://localhost:8765/api/device/vbus1/vbus-state?serial=DKYGR02"
```

### 2.4 `POST /api/device/{id}/all-on` and `/all-off`

Hub shortcut, equivalent to `set_all_usb_ports` with `{"on":true|false}`.

```bash
curl -X POST http://localhost:8765/api/device/hub1/all-on
```

### 2.5 `POST /api/device/{id}/connect`

Reconnects the device, optionally changing port/baud (persisted to config).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `port` | string | no | New port; empty keeps the current one |
| `baud` | int | no | New baud; `0` keeps the current one |

```bash
curl -X POST http://localhost:8765/api/device/psu1/connect \
  -H "Content-Type: application/json" \
  -d '{"port":"COM5","baud":9600}'
```

### 2.6 `POST /api/device/{id}/disconnect`

Closes the port without removing the device. Auto-reconnect skips it until a
command, a reconnect, or a restart. For `spm3051` the driver also sends
`SYSTem:LOCal` (drained before `Close`) so the front panel is released.

### 2.7 `POST /api/device/{id}/rename`

```json
{ "name": "My Lab PSU" }
```

### 2.8 `GET /api/config` and `POST /api/config`

`GET` returns the stored configuration:

```json
{
  "devices": [
    { "id": "psu1", "name": "Lab PSU", "type": "spm3051",
      "port": "COM3", "baud": 9600, "enabled": true }
  ],
  "server_port": 8765,
  "active_device": "psu1",
  "voice_enabled": false
}
```

`POST` replaces the stored configuration with the posted object (same schema).

> **Note:** the REST replace updates the saved configuration only. Live driver
> instances are reconciled when devices are (re)loaded; use
> `POST /api/config/device` per device for immediate apply, or the MCP
> `powerwing_set_config` tool, which reconciles live devices.

### 2.9 `POST /api/config/device` and `DELETE /api/config/device/{id}`

`POST` adds or replaces one device, then persists the config. In lazy mode
(default) the port is **not** opened until the device is connected or used.

```json
{ "id": "psu1", "name": "Lab PSU", "type": "spm3051",
  "port": "COM3", "baud": 9600, "enabled": true }
```

`DELETE /api/config/device/{id}` disconnects the device and removes it.

### 2.10 `GET /api/serial-ports`

```json
["COM3", "COM4", "/dev/ttyUSB0"]
```

### 2.11 `GET /api/serial-log` and `DELETE /api/serial-log`

Every serial open/close is recorded in a ring buffer (max 1000 entries).

`GET`:

```json
{
  "events": [
    {
      "time": "2026-10-29 17:58:01.245",
      "action": "open",
      "owner": "psu1",
      "port": "COM3",
      "baud": 9600,
      "caller": "manager.go:1228"
    }
  ]
}
```

`action` is `open`, `open_failed`, or `close`; `error` appears on failures.
`DELETE` (or `POST`) clears the log.

### 2.12 `GET /api/version`

```json
{ "version": "1.2.0" }
```

### 2.13 `POST /mcp`

MCP Streamable HTTP endpoint (see §6.1). Stateless: each POST carries one
JSON-RPC message.

### 2.14 `GET /`

Serves the embedded web UI.

---

## 3. WebSocket API

Connect to `ws://<host>:8765/ws`. The server immediately sends an `init`
message with the full device list, then pushes `state` messages whenever a
device is polled (about once per second) or immediately after a command.

### Server → client

```json
{ "type": "init", "devices": [ { "id": "psu1", "...": "..." } ] }
```

```json
{
  "type": "state",
  "device_id": "psu1",
  "device_type": "spm3051",
  "device_name": "Lab PSU",
  "state": { "...": "see §5" }
}
```

```json
{ "type": "error", "message": "unknown command \"foo\"" }
```

### Client → server

```json
{ "type": "cmd", "device_id": "psu1", "command": "set_volt", "params": { "value": 5.0 } }
```

Commands have the same names and parameters as REST `POST /api/device/{id}/cmd`
and MCP `powerwing_send_command`.

---

## 4. Device commands

All commands below work through:

- REST: `POST /api/device/{id}/cmd` with `{"command": ..., "params": {...}}`
- WebSocket: `{"type":"cmd","device_id":...,"command":...,"params":{...}}`
- MCP: `powerwing_send_command`

Parameters are JSON objects; `{}` means "no parameters".

### 4.1 Power supply

| Command | Params | Applies to | Description |
|---------|--------|------------|-------------|
| `set_volt` | `value` number | spm3051, pd_pocket | Set output voltage (V) |
| `set_curr` | `value` number | spm3051, pd_pocket | Set current limit (A) |
| `set_outp` | `on` bool | spm3051, pd_pocket | Enable/disable the output |
| `set_ovp` | `value` number | spm3051 (pd_pocket no-op) | OVP trip voltage (V) |
| `set_ocp` | `value` number | spm3051, pd_pocket | OCP trip current (A) |
| `release_control` | — | spm3051 | Return front-panel control without disconnecting; monitoring continues |

The **SPM3051 remote-control policy**: the panel is in local mode by default.
The driver enters remote mode only around a set command and returns to local
2 s after the last one, on `release_control`, and on disconnect. The next set
command re-takes control automatically.

### 4.2 SPM3051 built-in multimeter

Requires a unit that reports a meter at connect time. The commands try the
likely OWON SCPI spellings and verify the result by reading the state back; if
no spelling is accepted the command returns an error.

| Command | Params | Description |
|---------|--------|-------------|
| `meter_set_function` | `value` string | `VOLT:DC`, `VOLT:AC`, `CURR:DC`, `CURR:AC`, `RES`, `FRES`, `CAP`, `CONT`, `DIOD`, `FREQ`, `PER` |
| `meter_set_auto` | `on` bool | Enable/disable auto-ranging |
| `meter_set_range` | `value` number | Manual range in base units (V, A, Ω, F, Hz); turns auto off |
| `meter_set_hold` | `on` bool | Freeze/unfreeze the reading |
| `meter_set_null` | `on` bool | Enable/disable relative (NULL) measurement |

### 4.3 USB hub

| Command | Params | Applies to | Description |
|---------|--------|------------|-------------|
| `set_usb_port` | `port` int, `on` bool | usbslim (1-4), sangenxian (1-12) | Switch one port |
| `set_all_usb_ports` | `on` bool | usbslim, sangenxian | Switch every port |
| `set_lock` | `locked` bool | usbslim | Panel/UI lock |
| `set_hwkeys` | `enabled` bool | usbslim | Physical buttons |
| `set_autosave` | `enabled` bool | usbslim | Auto-save state |

`sangenxian` returns an error for `set_lock`, `set_hwkeys`, `set_autosave`.

### 4.4 Alarm light (usbalarm)

| Command | Params | Description |
|---------|--------|-------------|
| `alarm_set_color` | `color` string, `mode`? string, `color2`? string, `duration`? int, `loop`? int | Solid/blink color. `mode` = `on` (default), `blink_slow`, `blink_fast`, `off`, or `animation` (requires `color2`; `duration` ms default 2000, `loop` default -1 = forever) |
| `alarm_buzzer` | `on` bool | Buzzer on/off |
| `alarm_off` | — | Light + buzzer off |
| `alarm_beep` | `color` string, `ms` int | Short beep (blocking; runs in background) |
| `alarm_pwm` | `red`?, `green`?, `blue`?, `flash_interval`?, `buzzer`? | Raw PWM (0-255 channels) |
| `alarm_rainbow` | `duration`? int | Rainbow effect, default 2000 ms |

Color names: `red`, `yellow`, `green`, `white`, `cyan`, `purple`, `blue`,
`off`.

### 4.5 Power monitor (f11, powerz)

| Command | Params | Description |
|---------|--------|-------------|
| `monitor_set_interval` | `interval_ms` int | Sampling/reporting interval |
| `monitor_set_print` | `mode` int | Meter print mode (f11; powerz accepts it as a no-op) |
| `monitor_start` | — | Start average/peak accumulation |
| `monitor_stop` | — | Stop accumulation |

### 4.6 VBUS control (ftdi_vbus)

| Command | Params | Description |
|---------|--------|-------------|
| `set_vbus` | `on` bool, `serial`? string | One-shot open → drive the FT230XQ CBUS pin → read-back verify → close. The level is latched by the board. |
| `vbus_read_state` | `serial`? string | One transient open/read/close of the latched level; the fresh state is cached and broadcast. |

Neither command holds the port after it returns, and neither waits for or
retries a held port. `serial` selects the board; omit it to use the
configured default or the last selection.

Windows only; requires `ftd2xx.dll` and the CBUS pin programmed as GPIO in the
device EEPROM. See [api_ftdi_vbus.md](api_ftdi_vbus.md).

---

## 5. State objects

### 5.1 Power supply (`PowerState`)

```json
{
  "connected": true,
  "volt_meas": 5.010,
  "curr_meas": 0.000,
  "power_meas": 0.000,
  "volt_set": 5.400,
  "curr_set": 4.900,
  "ovp_limit": 9.000,
  "ocp_limit": 5.000,
  "output_on": true,
  "ovp_triggered": false,
  "ocp_triggered": false,
  "otp_triggered": false,
  "cv_mode": true,
  "remote_control": false,
  "meter": {
    "func": "VOLT:DC",
    "value": 0.0006,
    "unit": "V",
    "range": "2V",
    "auto_range": true,
    "hold": false,
    "null": false
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `connected` | bool | Port open and device responding |
| `volt_meas`, `curr_meas`, `power_meas` | float | Output measurements |
| `volt_set`, `curr_set` | float | Cached set-points (refreshed from the device) |
| `ovp_limit`, `ocp_limit` | float | Protection limits |
| `output_on` | bool | Output enabled |
| `ovp_triggered`, `ocp_triggered`, `otp_triggered` | bool | Protection flags |
| `cv_mode` | bool | `true` = CV, `false` = CC (SPM3051 uses the reported mode) |
| `remote_control` | bool | `true` while the PC holds front-panel control (spm3051) |
| `meter` | object? | Built-in multimeter; absent when unsupported |
| `temperature` | float? | Optional temperature (°C) |
| `input_volt` | float? | Optional input voltage (V) |

`meter` fields: `func`, `value` (in `unit`), `unit`, `range` (as reported,
e.g. `2V`), `auto_range`, `hold`, `null`, and `overload` (reading was `OL`).

### 5.2 USB hub (`HubState`)

```json
{
  "connected": true,
  "ports": [true, false, true, false],
  "port_count": 4,
  "locked": false,
  "hw_keys_enabled": true,
  "auto_save_enabled": true,
  "supports_lock": true,
  "supports_hw_keys": true,
  "supports_auto_save": true
}
```

### 5.3 Alarm light (`AlarmState`)

```json
{
  "connected": true,
  "color": "red",
  "mode": "on",
  "buzzer_on": false,
  "pwm_r": 255,
  "pwm_g": 0,
  "pwm_b": 0,
  "flash_interval": 0
}
```

### 5.4 Power monitor (`MonitorState`)

```json
{
  "connected": true,
  "current": 0.512,
  "voltage": 5.003,
  "monitoring": true,
  "avg_current": 0.508,
  "avg_voltage": 5.001,
  "peak_current": 0.620,
  "peak_voltage": 5.010,
  "sample_count": 1420,
  "interval_ms": 100
}
```

### 5.5 VBUS control (`VBusState`)

```json
{
  "connected": true,
  "vbus_on": true,
  "known": true,
  "serial": "DK0MAQPC",
  "pin": 0,
  "active_low": true
}
```

`vbus_on` is meaningful only while `known` is true (the latched level has been
read or commanded). `last_error` appears after a driver error.

---

## 6. MCP server

### 6.1 Transports

| Transport | How to start | Endpoint |
|-----------|--------------|----------|
| stdio | `power_wing --mcp-only --mcp-transport stdio` | stdin/stdout |
| raw TCP | `power_wing --mcp --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876` | `tcp://<host>:9876` |
| Streamable HTTP | always available with the web server | `POST http://<host>:8765/mcp` |

- The stdio/TCP server accepts **Content-Length framed** messages
  (`Content-Length: N`, blank line, JSON body) and **newline-delimited JSON**.
  Responses use `Content-Length: N\n\n<json>` (LF only, for WSL interop).
- The HTTP transport is stateless: one JSON-RPC message per POST; notifications
  (no `id`) receive HTTP 202 with no body. CORS is open (`*`).
- Protocol version: **MCP 2024-11-05**, JSON-RPC 2.0.

### 6.2 JSON-RPC methods

| Method | Purpose |
|--------|---------|
| `initialize` | Handshake; returns `protocolVersion`, `capabilities.tools`, `serverInfo` (`name: "power-wing"`) |
| `notifications/initialized` | Notification, no response |
| `ping` | Returns `{}` |
| `tools/list` | Returns the tool definitions below |
| `tools/call` | Invoke a tool: `{"name": "...", "arguments": {...}}` |

A `tools/call` result is:

```json
{
  "content": [ { "type": "text", "text": "<pretty-printed JSON result>" } ]
}
```

Tool failures add `"isError": true` and put the error message in
`content[0].text`.

### 6.3 Tools

#### `powerwing_list_devices`

No arguments. Returns:

```json
{ "devices": [
  { "id": "psu1", "name": "Lab PSU", "type": "spm3051",
    "connected": true, "port": "COM3", "baud": 9600, "state": { "...": "..." } }
] }
```

#### `powerwing_get_device_state`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `device_id` | string | yes | Device ID |

Returns `{"device_id": "...", "state": {...}}`. Errors if no state exists yet.

#### `powerwing_send_command`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `device_id` | string | yes | Device ID |
| `command` | string | yes | Command from §4 |
| `params` | object | no | Command parameters |

Returns `{"status":"ok","device_id":"...","command":"..."}`.

#### `powerwing_list_serial_ports`

No arguments. Returns `{"ports": ["COM3", ...]}`.

#### `powerwing_list_ftdi_boards`

No arguments. Lists the FTDI boards D2XX can currently name; never opens one.

```json
{ "boards": [
  { "serial": "DKYGR02", "description": "FT230X Basic UART", "type": 9 }
] }
```

#### `powerwing_read_vbus_state`

One transient open/read/close of an `ftdi_vbus` controller's selected board.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `device_id` | string | yes | VBUS controller device ID |
| `serial` | string | no | FTDI board serial; omit for the default/last selection |

Returns `{"device_id":"...","state":{...}}` (see §5.5).

#### `powerwing_disconnect_device`

| Argument | Type | Required |
|----------|------|----------|
| `device_id` | string | yes |

Returns `{"status":"ok","device_id":"..."}`.

#### `powerwing_reconnect_device`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `device_id` | string | yes | Device ID |
| `port` | string | no | New port; omit to keep |
| `baud` | int | no | New baud; omit/0 to keep |

Returns `{"status":"ok","device_id":"...","port":"...","baud":9600}`.

#### `powerwing_rename_device`

| Argument | Type | Required |
|----------|------|----------|
| `device_id` | string | yes |
| `name` | string | yes |

Returns `{"status":"ok","device_id":"...","name":"..."}`.

#### `powerwing_test_connection`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `device_type` | string | yes | `spm3051`, `pd_pocket`, `usbslim`, `sangenxian`, `usbalarm`, `f11`, `powerz`, `ftdi_vbus` |

Checks **configured** devices of that type only; non-configured ports are never
opened. Returns:

```json
{
  "device_type": "spm3051",
  "results": [
    { "device_id": "psu1", "name": "Lab PSU", "port": "COM3", "baud": 9600,
      "matched": true, "reason": "device is connected via manager" }
  ]
}
```

Reasons: `device is connected via manager`, `port is in use by another
connected device`, `device has no port configured`, `configured port is not
present on the host`; otherwise `matched` is the probe result.

#### `powerwing_get_config`

No arguments. Returns:

```json
{
  "devices": [ { "id": "psu1", "name": "Lab PSU", "type": "spm3051",
                 "port": "COM3", "baud": 9600, "enabled": true } ],
  "server_port": 8765,
  "active_device": "psu1",
  "voice_enabled": false
}
```

#### `powerwing_set_config`

Two mutually exclusive forms:

1. **Replace all devices**

```json
{ "devices": [
  { "id": "psu1", "name": "Lab PSU", "type": "spm3051",
    "port": "COM3", "baud": 9600, "enabled": true }
] }
```

Entries require `id` and `type`; `name`, `port`, `baud`, `enabled` are optional.
For `type: "ftdi_vbus"`, pass `serial` (string), `pin` (0–3) and `active_low`
(bool) instead of `port`/`baud`.
The manager validates IDs (unique, non-empty), ports (unique), and types, then
reconciles live drivers and persists the result.

2. **Update one device**

```json
{ "device_id": "psu1", "name": "Bench A", "port": "COM5", "baud": 9600, "enabled": true }
```

Omitted fields keep their current values; a port/baud change reconnects the
device.

Returns `{"status":"ok","updated":"all","device_count":N}` or
`{"status":"ok","updated":"<id>","port":"...","baud":N}`.

### 6.4 Example session (stdio / TCP, newline-delimited)

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"powerwing_list_devices","arguments":{}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"powerwing_send_command","arguments":{"device_id":"psu1","command":"set_volt","params":{"value":5.0}}}}
```

Example over HTTP:

```bash
curl -s http://localhost:8765/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

---

## 7. CLI flags related to the API

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | `0.0.0.0` | HTTP bind host |
| `--port` | `8765` | HTTP port (REST + WebSocket + `/mcp`) |
| `--mcp` | `false` | Enable MCP alongside the web UI/tray |
| `--mcp-only` | `false` | Run only the MCP server (no web UI/tray) |
| `--mcp-transport` | `stdio` | `stdio` or `tcp` |
| `--mcp-host` | `0.0.0.0` | TCP MCP bind host |
| `--mcp-port` | `9876` | TCP MCP port |
| `--auto-connect` | `false` | Open all configured ports at startup (otherwise lazy) |
| `--no-browser` | `false` | Do not open the web UI on start |

---

## 8. Behaviour notes

1. **Lazy connect.** Ports are not opened at startup by default. A device is
   opened by clicking Connect, by `POST /api/device/{id}/connect`, or
   automatically by the first command sent to it.
2. **State freshness.** Devices are polled about once per second; a state
   update is also pushed immediately after each command. `GET .../state`
   always returns the latest polled snapshot.
3. **Reconnects.** The SPM3051 and PD Pocket drivers disconnect after 3
   consecutive communication errors; devices that connected successfully at
   least once are retried by the background reconnect loop (about every
   second).
4. **SPM3051 control release.** Read-only polling runs in local mode. Remote
   mode is entered only for set commands and released 2 s later, on
   `release_control`, and on disconnect — the front panel is never left locked.
5. **SPM3051 multimeter.** The DMM command tree is not publicly documented;
   the driver tries several OWON spellings per setting and verifies the
   read-back. A rejected setting returns an error (shown as a toast in the UI).
6. **REST config replace vs MCP.** `POST /api/config` updates the stored config
   only; MCP `powerwing_set_config` validates and reconciles live devices
   immediately.
7. **Serial log** is in memory (last 1000 open/close events) and is cleared on
   restart.
