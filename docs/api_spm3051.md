# SPM3051 REST API

SPM3051 is a UART lab power supply controlled via SCPI-like protocol (9600 baud, 8N1).

**Device type:** `spm3051`  
**Interface:** `device.PowerSupply`  
**Default baud:** 9600  
**Protocol:** SCPI-like (see [SPM3051-CMD.txt](../SPM3051-CMD.txt))

---

## Device Config

Add an SPM3051 device via `POST /api/config/device`:

```json
{
  "id": "psu1",
  "name": "Lab PSU",
  "type": "spm3051",
  "port": "COM3",
  "baud": 9600,
  "enabled": true
}
```

| Field    | Type   | Description                          |
|----------|--------|--------------------------------------|
| `id`     | string | Unique device identifier             |
| `name`   | string | Display name                         |
| `type`   | string | Must be `"spm3051"`                  |
| `port`   | string | Serial port (e.g. `"COM3"`, `/dev/ttyUSB0`) |
| `baud`   | int    | Baud rate; `0` or `9600` = default   |
| `enabled`| bool   | Whether device is active             |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"psu1","name":"Lab PSU","type":"spm3051","port":"COM3","baud":9600,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## Remove Device Config

```bash
curl -X DELETE http://localhost:8765/api/config/device/psu1
```

**Response:**

```json
{"status": "ok"}
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
    "id": "psu1",
    "type": "spm3051",
    "name": "Lab PSU",
    "connected": true,
    "port": "COM3",
    "baud": 9600,
    "state": { ... }
  }
]
```

---

## Get Device State

```bash
curl http://localhost:8765/api/device/psu1/state
```

### PowerState Schema

```json
{
  "connected":      true,
  "volt_meas":      5.234,
  "curr_meas":      1.002,
  "power_meas":     5.24,
  "volt_set":       5.000,
  "curr_set":       1.000,
  "ovp_limit":      6.000,
  "ocp_limit":      1.500,
  "output_on":      true,
  "ovp_triggered":  false,
  "ocp_triggered":  false,
  "cv_mode":        true
}
```

| Field            | Type   | Description                                |
|------------------|--------|--------------------------------------------|
| `connected`      | bool   | Whether device is connected                |
| `volt_meas`      | float  | Measured output voltage (V)                |
| `curr_meas`      | float  | Measured output current (A)                |
| `power_meas`     | float  | Measured output power (W)                  |
| `volt_set`       | float  | Set voltage (V)                            |
| `curr_set`       | float  | Set current limit (A)                      |
| `ovp_limit`      | float  | Over-voltage protection limit (V)          |
| `ocp_limit`      | float  | Over-current protection limit (A)          |
| `output_on`      | bool   | Output enable state                        |
| `ovp_triggered`  | bool   | OVP has been triggered                     |
| `ocp_triggered`  | bool   | OCP has been triggered                     |
| `cv_mode`        | bool   | `true` = CV mode, `false` = CC mode        |

---

## Send Command

All commands are sent via `POST /api/device/{id}/cmd` with a JSON body:

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

### set_volt — Set Output Voltage

Set the output voltage set-point.

| Param   | Type  | Required | Description          |
|---------|-------|----------|----------------------|
| `value` | float | yes      | Voltage in V (0–30)  |

```bash
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_volt","params":{"value":5.0}}'
```

---

### set_curr — Set Output Current

Set the output current limit.

| Param   | Type  | Required | Description          |
|---------|-------|----------|----------------------|
| `value` | float | yes      | Current in A (0–5)   |

```bash
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_curr","params":{"value":1.0}}'
```

---

### set_outp — Enable / Disable Output

Turn the output on or off.

| Param | Type | Required | Description             |
|-------|------|----------|-------------------------|
| `on`  | bool | yes      | `true` = ON, `false` = OFF |

```bash
# Turn output ON
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_outp","params":{"on":true}}'

# Turn output OFF
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_outp","params":{"on":false}}'
```

---

### set_ovp — Set OVP Limit

Set the over-voltage protection trip voltage.

| Param   | Type  | Required | Description              |
|---------|-------|----------|--------------------------|
| `value` | float | yes      | OVP limit in V           |

```bash
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_ovp","params":{"value":6.0}}'
```

---

### set_ocp — Set OCP Limit

Set the over-current protection trip current.

| Param   | Type  | Required | Description              |
|---------|-------|----------|--------------------------|
| `value` | float | yes      | OCP limit in A           |

```bash
curl -X POST http://localhost:8765/api/device/psu1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_ocp","params":{"value":1.5}}'
```

---

## Lifecycle Endpoints

### Reconnect Device

Reconnect with optional new port/baud settings.

```bash
# Reconnect on same port
curl -X POST http://localhost:8765/api/device/psu1/connect \
  -H "Content-Type: application/json" \
  -d '{}'

# Reconnect on a different port
curl -X POST http://localhost:8765/api/device/psu1/connect \
  -H "Content-Type: application/json" \
  -d '{"port":"COM5","baud":9600}'
```

| Param  | Type   | Required | Description                          |
|--------|--------|----------|--------------------------------------|
| `port` | string | no       | New serial port (empty = keep current) |
| `baud` | int    | no       | New baud rate (0 = keep current)       |

---

### Disconnect Device

Disconnect without removing from config.

```bash
curl -X POST http://localhost:8765/api/device/psu1/disconnect
```

---

### Rename Device

```bash
curl -X POST http://localhost:8765/api/device/psu1/rename \
  -H "Content-Type: application/json" \
  -d '{"name":"My Lab PSU"}'
```

---

## Serial Ports

List available serial ports for configuration:

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

SPM3051 can also be controlled via command-line flags:

```bash
powerwing --device psu1 --volt 5.0
powerwing --device psu1 --curr 1.0
powerwing --device psu1 --outp on
powerwing --device psu1 --ovp 6.0
powerwing --device psu1 --ocp 1.5
```

---

## Notes

- SPM3051 enters **remote-control mode** (`SYSTem:REMote`) automatically on connect.
- On connect, all set-points (voltage, current, OVP, OCP, output state) are queried from the device and cached.
- State is polled every 1 second via `MEASure:ALL:INFO?` which returns: `V,A,W,OVP_trig,OCP_trig,x,mode`.
- After 3 consecutive communication errors, the device is auto-disconnected and will retry reconnect every 5 seconds.
