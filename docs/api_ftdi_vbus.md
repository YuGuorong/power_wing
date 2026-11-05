# FTDI FT230XQ VBUS Control Driver

The `ftdi_vbus` device type switches a DUT's VBUS on and off through a CBUS pin
of an FTDI FT230XQ board. It is the software equivalent of unplugging and
replugging the DUT's USB cable, without touching the cable or the hub.

Control goes through FTDI's **D2XX** driver, not a COM port: the FT230X exposes
CBUS only through `FT_SetBitMode` in CBUS bit-bang mode (`0x20`). A VCP/COM
port cannot drive these pins.

> **No long-lived connection.** The driver never opens the port for more than
> one read or one switch. There is no Connect button and no background
> reconnect. Every operation is a single open/act/close with no wait and no
> retry.

> **Platform:** D2XX access is Windows-only in PowerWing. On Linux the device
> type is still registered but `Connect` reports
> `ftd2xx: only supported on Windows`.

---

## Hardware prerequisites

1. Install the FTDI D2XX driver so `ftd2xx.dll` is available to Windows
   (PowerWing loads it from `System32` only).
2. With **FT_PROG**, program the CBUS pin that gates VBUS as **GPIO**. If the
   pin is left at its default function, `FT_SetBitMode` fails and PowerWing
   reports:
   `driving CBUSn ... failed (are the CBUS pins programmed as GPIO in the EEPROM with FT_PROG?)`.
3. Wire the pin according to `active_low`:
   - `active_low: true` (default) — pin **low** turns VBUS **on**.
   - `active_low: false` — pin **high** turns VBUS **on**.

The CBUS level is **latched by the device**. It survives `FT_Close`, a
PowerWing restart, and a process exit. It returns to the EEPROM default when
the board re-enumerates (unplug, driver reload).

> **VCP note:** opening the board's COM port from another program (a serial
> terminal, a bridge, the debug tool's `-check-port`) can reset/re-enumerate
> the FT230X and return CBUS to its EEPROM default. PowerWing itself never
> opens the VCP port; only D2XX is used, briefly.

---

## Configuration

```json
{
  "id": "vbus1",
  "name": "DUT VBUS",
  "type": "ftdi_vbus",
  "serial": "DKYGR02",
  "pin": 0,
  "active_low": true,
  "enabled": true
}
```

| Field | Meaning |
|-------|---------|
| `serial` | Default board serial. The UI dropdown remembers the last selection; when neither is available the first FT-X board is used. |
| `pin` | CBUS pin driving VBUS, `0`–`3`. Default `0`. |
| `active_low` | Pin polarity. Omitted = `true` (pin low = VBUS on). |
| `port` / `baud` | Unused; leave empty/zero. |

The board serial is the D2XX serial (`DKYGR02`), not the COM port Windows
assigned to its VCP interface (e.g. `COM43`).

---

## No connection, no held handle

| Action | What happens |
|--------|--------------|
| `Connect` | Only verifies that D2XX is usable. **No board is opened**, no handle is held. |
| Board list | `FT_CreateDeviceInfoList` / `FT_GetDeviceInfoList` only — no open. |
| Read state | One `FT_OpenEx` → `FT_GetBitMode` → `FT_Close`. |
| Switch (`set_vbus`) | One `FT_OpenEx` → `FT_SetBitMode` → read-back verify → `FT_Close`. |
| Polling | Returns the cached state; never touches D2XX. |
| Background reconnect | Disabled for `ftdi_vbus`. |

Every open is a **single attempt**. There is no timeout to wait out and no
retry loop: if the port is held by another program, `FT_OpenEx` failure is
reported immediately (`FT_DEVICE_NOT_OPENED`), so an operation never contends
with a serial session. The manager also never auto-reconnects an `ftdi_vbus`
device.

Verified in-process: while the manager stays alive, `COM43` can be opened and
closed immediately after any read/switch (see the bring-up tool below).

---

## Web UI

The VBUS panel is intentionally small and has **no Connect button**:

- **Board dropdown** — lists the FTDI boards D2XX can currently name. The list
  is refreshed when the page is entered, when the dropdown is about to open,
  and by the **⟳** refresh button.
- **Selecting a board** (including the initial selection) performs one
  transient read of that board's latched VBUS state.
- **VBUS ON / VBUS OFF** — one-shot switch; the confirmed state arrives over
  the WebSocket.
- The last selected board is remembered in browser local storage.

---

## REST API

### `GET /api/device/{id}/boards`

Lists the FTDI boards (no open):

```json
[
  { "serial": "DKYGR02", "description": "FT230X Basic UART", "type": 9 },
  { "serial": "DKYGR01", "description": "FT230X Basic UART", "type": 9 }
]
```

### `GET /api/device/{id}/vbus-state?serial=DKYGR02`

Performs **one transient read** and returns the state object below. `serial`
may be omitted to use the default/last selection. The board is closed before
the response is written.

### `POST /api/device/{id}/cmd`

| Command | Params | Description |
|---------|--------|-------------|
| `set_vbus` | `on` bool, `serial`? string | One-shot open/switch/verify/close |
| `vbus_read_state` | `serial`? string | One transient read; the fresh state is cached and broadcast |

```bash
# Switch on
curl -X POST http://127.0.0.1:8765/api/device/vbus1/cmd \
  -H 'Content-Type: application/json' \
  -d '{"command":"set_vbus","params":{"on":true,"serial":"DKYGR02"}}'

# Read the latched level now
curl "http://127.0.0.1:8765/api/device/vbus1/vbus-state?serial=DKYGR02"
```

---

## State object

```json
{
  "connected": true,
  "vbus_on": true,
  "known": true,
  "serial": "DKYGR02",
  "pin": 0,
  "active_low": true
}
```

| Field | Meaning |
|-------|---------|
| `vbus_on` | Last commanded/read VBUS state; meaningful only when `known` is true |
| `known` | False until the latched level has been read or a switch has succeeded |
| `connected` | D2XX is usable (not a held handle) |
| `last_error` | Present only after a driver error |

---

## MCP tools

| Tool | Purpose |
|------|---------|
| `powerwing_list_ftdi_boards` | List the FTDI boards D2XX can name (no open) |
| `powerwing_read_vbus_state` | One transient read for a `device_id` (optional `serial`) |
| `powerwing_send_command` | `set_vbus` with `{ "on": true, "serial": "DKYGR02" }`, or `vbus_read_state` |

`powerwing_test_connection` with `device_type: "ftdi_vbus"` enumerates D2XX and
reports whether a board with the configured serial is attached. Nothing is
opened.

---

## Errors

| Message | Cause |
|---------|-------|
| `ftd2xx: only supported on Windows` | Running on Linux/macOS |
| `FT_DEVICE_NOT_OPENED` | Board held by another program; PowerWing does not wait or retry |
| `no FTDI board detected` | No D2XX board is enumerable |
| `several FTDI boards attached; select one` | No default serial and more than one board |
| `driving CBUSn ... failed` | CBUS pin not programmed as GPIO in the EEPROM, or wrong serial |
| `CBUSn did not follow` | Written mask not reflected by `FT_GetBitMode` — wrong pin or wiring |

---

## Bring-up / debug tool

`tools/vbusprobe` exercises the same code paths against a real board:

```bash
# Enumerate D2XX and perform one transient read (no switching)
go run ./tools/vbusprobe -serial DKYGR02 -pin 0

# Drive the pin and verify the read-back
go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -set on
go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -set off

# Through config -> manager -> ReadVBusState -> set_vbus
go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -manager -set on

# Prove the port is released while the manager stays alive and polls
go run ./tools/vbusprobe -serial DKYGR02 -pin 0 -manager -set on \
  -hold 3s -check-port COM43
```

`-active-low=false` selects the inverted polarity.
