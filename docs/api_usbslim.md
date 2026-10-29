# USB-Slim REST API

USB-Slim is a 4-port USB hub controller with per-port power switching, controlled via a binary UART protocol (9600 baud, 8N1).

**Device type:** `usbslim`  
**Interface:** `device.USBHub`  
**Default baud:** 9600  
**Protocol:** Binary frames `55 5A CMD PORT VAL CKSUM` (see [Usb-slim-api.txt](../Usb-slim-api.txt))

---

## Device Config

Add a USB-Slim device via `POST /api/config/device`:

```json
{
  "id": "hub1",
  "name": "USB Hub",
  "type": "usbslim",
  "port": "COM5",
  "baud": 9600,
  "enabled": true
}
```

| Field     | Type   | Description                                    |
|-----------|--------|------------------------------------------------|
| `id`      | string | Unique device identifier                       |
| `name`    | string | Display name                                   |
| `type`    | string | Must be `"usbslim"`                            |
| `port`    | string | Serial port (e.g. `"COM5"`, `/dev/ttyUSB1`)     |
| `baud`    | int    | Baud rate; `0` or `9600` = default             |
| `enabled` | bool   | Whether device is active                       |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"hub1","name":"USB Hub","type":"usbslim","port":"COM5","baud":9600,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## Remove Device Config

```bash
curl -X DELETE http://localhost:8765/api/config/device/hub1
```

---

## List All Devices

```bash
curl http://localhost:8765/api/devices
```

**Response:**

```json
[
  {
    "id": "hub1",
    "type": "usbslim",
    "name": "USB Hub",
    "connected": true,
    "port": "COM5",
    "baud": 9600,
    "state": { ... }
  }
]
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
  "ports": [true, false, true, false],
  "port_count": 4,
  "locked": false,
  "hw_keys_enabled": true,
  "auto_save_enabled": false,
  "supports_lock": true,
  "supports_hw_keys": true,
  "supports_auto_save": true
}
```

| Field              | Type      | Description                                    |
|--------------------|-----------|------------------------------------------------|
| `connected`        | bool      | Whether device is connected                    |
| `ports`            | bool[4]   | Per-port ON/OFF state; index 0 = port 1        |
| `port_count`       | int       | Number of switchable ports (4)                 |
| `locked`           | bool      | UI lock state (`true` = locked)                |
| `hw_keys_enabled`  | bool      | Physical button control (`true` = enabled)     |
| `auto_save_enabled`| bool      | Auto-save state on power-off (`true` = enabled)|
| `supports_lock`    | bool      | Driver supports the Lock control               |
| `supports_hw_keys` | bool      | Driver supports hardware-key control           |
| `supports_auto_save`| bool     | Driver supports auto-save control              |

> `ports` is a 4-element array where `ports[0]` corresponds to physical port 1, `ports[1]` to port 2, etc.

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
| `port` | int  | yes      | Port number (1–4)              |
| `on`   | bool | yes      | `true` = ON, `false` = OFF     |

```bash
# Turn port 1 ON
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":1,"on":true}}'

# Turn port 3 OFF
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":3,"on":false}}'
```

Underlying device command: binary frame `55 5A 01 <portMask> <0|1> <cksum>`.

---

### all-on / all-off — Switch Every Port

```bash
# All 4 ports ON
curl -X POST http://localhost:8765/api/device/hub1/all-on

# All 4 ports OFF
curl -X POST http://localhost:8765/api/device/hub1/all-off
```

Generic command form:

```json
{"command":"set_all_usb_ports","params":{"on":true}}
```

Underlying device command: `55 5A 01 0F <0|1> <cksum>` (mask `0x0F`).

---

### set_lock — Lock / Unlock UI

Lock or unlock the hub's UI control.

| Param    | Type | Required | Description                 |
|----------|------|----------|------------------------------|
| `locked` | bool | yes      | `true` = lock, `false` = unlock |

```bash
# Lock the hub
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_lock","params":{"locked":true}}'

# Unlock the hub
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_lock","params":{"locked":false}}'
```

Underlying device command: binary frames `55 5A 06 00 <0|1> <cksum>` followed by confirmation `55 5A 07 00 00 <cksum>`.

---

### set_hwkeys — Enable / Disable Physical Buttons

Enable or disable the physical hardware buttons on the hub.

| Param     | Type | Required | Description                     |
|-----------|------|----------|---------------------------------|
| `enabled` | bool | yes      | `true` = enable, `false` = disable |

```bash
# Enable physical buttons
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_hwkeys","params":{"enabled":true}}'

# Disable physical buttons
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_hwkeys","params":{"enabled":false}}'
```

Underlying device command: binary frames `55 5A 09 00 <0|1> <cksum>` followed by confirmation `55 5A 0A 00 00 <cksum>`.

---

### set_autosave — Enable / Disable Auto-Save

Enable or disable the hub's auto-save feature (saves port states on power-off).

| Param     | Type | Required | Description                     |
|-----------|------|----------|---------------------------------|
| `enabled` | bool | yes      | `true` = enable, `false` = disable |

```bash
# Enable auto-save
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_autosave","params":{"enabled":true}}'

# Disable auto-save
curl -X POST http://localhost:8765/api/device/hub1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_autosave","params":{"enabled":false}}'
```

Underlying device command: binary frames `55 5A 0F 00 <0|1> <cksum>` followed by confirmation `55 5A 10 00 00 <cksum>`.

---

## Lifecycle Endpoints

### Reconnect Device

```bash
# Reconnect using current config
curl -X POST http://localhost:8765/api/device/hub1/connect \
  -H "Content-Type: application/json" \
  -d '{}'

# Reconnect with new serial settings
curl -X POST http://localhost:8765/api/device/hub1/connect \
  -H "Content-Type: application/json" \
  -d '{"port":"COM7","baud":9600}'
```

---

### Disconnect Device

```bash
curl -X POST http://localhost:8765/api/device/hub1/disconnect
```

---

### Rename Device

```bash
curl -X POST http://localhost:8765/api/device/hub1/rename \
  -H "Content-Type: application/json" \
  -d '{"name":"Desk USB Hub"}'
```

---

## Serial Ports

```bash
curl http://localhost:8765/api/serial-ports
```

---

## Full Config

### Get Config

```bash
curl http://localhost:8765/api/config
```

### Replace Config

```bash
curl -X POST http://localhost:8765/api/config \
  -H "Content-Type: application/json" \
  -d '{"devices":[...],"server_port":8765}'
```

---

## CLI Mode

```bash
powerwing --device hub1 --usb-port 1 --usb-state on
powerwing --device hub1 --usb-port 3 --usb-state off
```

---

## Notes

- On connect, DTR is asserted to enable the USB-CDC adapter's TX path (required for CH340/CP210x adapters).
- Port state is seeded from the device on connect by querying all 4 ports.
- The hub echoes each sent frame as an ACK. Some firmware versions do not echo — the driver tolerates this.
- Port numbers in the API are 1–4 (human-friendly), internally mapped to masks: port 1 = `0x01`, port 2 = `0x02`, port 3 = `0x04`, port 4 = `0x08`.
