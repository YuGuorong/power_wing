# SanGenXian 12-Port USB Hub REST API

SanGenXian (三根线) is a 12-port USB hub controller with per-port power switching, controlled over a binary UART protocol at 4800 baud, 8N1. The driver is a native Go reimplementation of the vendor `usbHubCtrl.dll` protocol; no DLL is required and it works on Windows and Linux.

**Device type:** `sangenxian`  
**Interface:** `device.USBHub`  
**Default baud:** 4800  
**Protocol:** `7E REG DATA CRC8` frames (CRC-8 poly `0x07`, init `0`)

---

## Device Config

Add a SanGenXian device via `POST /api/config/device`:

```json
{
  "id": "hub1",
  "name": "12-Port USB Hub",
  "type": "sangenxian",
  "port": "COM56",
  "baud": 4800,
  "enabled": true
}
```

| Field     | Type   | Description                                     |
|-----------|--------|-------------------------------------------------|
| `id`      | string | Unique device identifier                        |
| `name`    | string | Display name                                    |
| `type`    | string | Must be `"sangenxian"`                          |
| `port`    | string | Serial port (e.g. `"COM56"`, `/dev/ttyUSB1`)    |
| `baud`    | int    | Baud rate; `0` or `4800` = default              |
| `enabled` | bool   | Whether device is active                        |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"hub1","name":"12-Port USB Hub","type":"sangenxian","port":"COM56","baud":4800,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## Get Device State

```bash
curl http://localhost:8765/api/device/hub1/state
```

### HubState Schema

```json
{
  "connected": true,
  "ports": [true, true, false, false, false, false, false, false, false, false, false, true],
  "port_count": 12,
  "locked": false,
  "hw_keys_enabled": false,
  "auto_save_enabled": false,
  "supports_lock": false,
  "supports_hw_keys": false,
  "supports_auto_save": false
}
```

| Field               | Type    | Description                                          |
|---------------------|---------|------------------------------------------------------|
| `connected`         | bool    | Whether device is connected                          |
| `ports`             | bool[12]| Per-port ON/OFF state; index 0 = port 1              |
| `port_count`        | int     | Number of switchable ports (12)                      |
| `locked`            | bool    | Always `false` (feature not supported)               |
| `hw_keys_enabled`   | bool    | Always `false` (feature not supported)               |
| `auto_save_enabled` | bool    | Always `false` (feature not supported)               |
| `supports_lock`     | bool    | `false` – UI hides the Lock button                   |
| `supports_hw_keys`  | bool    | `false` – UI hides the HW Keys button                |
| `supports_auto_save`| bool    | `false` – UI hides the Auto-Save button              |

---

## Send Command

All commands are sent via `POST /api/device/{id}/cmd`:

```json
{
  "command": "<command_name>",
  "params": { ... }
}
```

**Response:**

```json
{"status": "ok"}
```

---

### set_usb_port — Set Port ON/OFF

Turn an individual USB port on or off.

| Param  | Type | Required | Description                    |
|--------|------|----------|--------------------------------|
| `port` | int  | yes      | Port number (1–12)             |
| `on`   | bool | yes      | `true` = ON, `false` = OFF     |

```bash
# Turn port 1 ON
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":1,"on":true}}'

# Turn port 12 OFF
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":12,"on":false}}'
```

Underlying device command: the driver updates the cached P0/P1 register bits and writes all four registers `P0/P1/P2/P4` (20 ms apart), exactly like the vendor DLL.

---

### all-on / all-off — Switch All 12 Ports

```bash
# All 12 ports ON
curl -X POST http://localhost:8765/api/device/hub1/all-on

# All 12 ports OFF
curl -X POST http://localhost:8765/api/device/hub1/all-off
```

Generic command form:

```json
{"command":"set_all_usb_ports","params":{"on":false}}
```

The driver updates every port bit in P0/P1 and writes P0/P1/P2/P4 once, preserving P2/P4.

`set_lock`, `set_hwkeys`, and `set_autosave` return an error for this device type because the controller does not implement them.

---

## Protocol Details

### Frame format

| Operation | Frame | Notes |
|-----------|-------|-------|
| Query | `7E (0x80 \| REG) CRC8` | 3 bytes |
| Reply | `<payload> CRC8(request)` | Status payload is one inverted byte |
| Write | `7E REG ~value CRC8` | 4 bytes |

- CRC-8: polynomial `0x07`, initial value `0`, covers all preceding bytes.
- Registers: `P0=0x00`, `P1=0x01`, `P2=0x02`, `P4=0x04`.
- Register bits are active-low on the wire: a `0` bit means the port is ON.
- The device echoes every write frame; the driver drains the echoes.

### Port map

| Physical port | Register | Bit |
|---------------|----------|-----|
| 1–8 | P0 | 0–7 |
| 9 | P1 | 0 |
| 10 | P1 | 1 |
| 11 | P1 | 6 |
| 12 | P1 | 7 |

P2/P4 belong to ports 13–30 on larger boards. They are read once at connect and preserved on every write, so 12-port hardware is not disturbed.

---

## CLI Mode

```bash
powerwing --device hub1 --usb-port 1 --usb-state on
powerwing --device hub1 --usb-port 12 --usb-state off
```

---

## Notes

- The driver probes the device on connect by reading all four registers; a device that does not answer causes `Connect` to fail.
- `GetState` refreshes P0/P1 from the hardware on every poll, so state changes are reflected in the UI within one poll interval (~1 s).
- DTR and RTS are asserted on connect, matching the vendor SDK.
- No vendor DLL is used. The protocol was verified against hardware on COM56: port 1 and ports 9–12 toggling, plus read-back, match the vendor DLL (`usbHubCtrl-x64.dll`) byte for byte.
