# PD Pocket REST API

PD Pocket is a UART programmable power supply controlled via a text command protocol (115200 baud, 8N1).

**Device type:** `pd_pocket`  
**Interface:** `device.PowerSupply`  
**Default baud:** 115200  
**Protocol:** Text commands terminated with `\r\n` (see [PD_Pocket API.txt](../PD_Pocket%20API.txt))

---

## Device Config

Add a PD Pocket device via `POST /api/config/device`:

```json
{
  "id": "pdp1",
  "name": "PD Pocket",
  "type": "pd_pocket",
  "port": "COM4",
  "baud": 115200,
  "enabled": true
}
```

| Field     | Type   | Description                                    |
|-----------|--------|------------------------------------------------|
| `id`      | string | Unique device identifier                       |
| `name`    | string | Display name                                   |
| `type`    | string | Must be `"pd_pocket"`                          |
| `port`    | string | Serial port (e.g. `"COM4"`, `/dev/ttyUSB0`)     |
| `baud`    | int    | Baud rate; `0` or `115200` = default           |
| `enabled` | bool   | Whether device is active                       |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"pdp1","name":"PD Pocket","type":"pd_pocket","port":"COM4","baud":115200,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## Remove Device Config

```bash
curl -X DELETE http://localhost:8765/api/config/device/pdp1
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
    "id": "pdp1",
    "type": "pd_pocket",
    "name": "PD Pocket",
    "connected": true,
    "port": "COM4",
    "baud": 115200,
    "state": { ... }
  }
]
```

---

## Get Device State

```bash
curl http://localhost:8765/api/device/pdp1/state
```

### PowerState Schema

```json
{
  "connected": true,
  "volt_meas": 5.2345,
  "curr_meas": 1.2345,
  "power_meas": 6.47,
  "volt_set": 5.0,
  "curr_set": 1.5,
  "ovp_limit": 0,
  "ocp_limit": 0,
  "output_on": true,
  "ovp_triggered": false,
  "ocp_triggered": false,
  "cv_mode": true,
  "temperature": 24.7
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
| `output_on`      | bool   | Output enable state                        |
| `cv_mode`        | bool   | `true` = CV mode, `false` = CC mode        |
| `temperature`    | float  | Device temperature, refreshed about every 10 seconds |

> `ovp_limit`, `ocp_limit`, `ovp_triggered`, and `ocp_triggered` are shared PowerState fields. PD Pocket does not expose all of these directly.

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

### set_volt — Set Output Voltage

| Param   | Type  | Required | Description          |
|---------|-------|----------|----------------------|
| `value` | float | yes      | Voltage in V (1–36)  |

```bash
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_volt","params":{"value":12.0}}'
```

Underlying device command: `VOLT <v>`.

---

### set_curr — Set Output Current

| Param   | Type  | Required | Description             |
|---------|-------|----------|-------------------------|
| `value` | float | yes      | Current in A (0.3–8.5)  |

```bash
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_curr","params":{"value":2.0}}'
```

Underlying device command: `CURR <a>`.

---

### set_outp — Enable / Disable Output

| Param | Type | Required | Description                 |
|-------|------|----------|-----------------------------|
| `on`  | bool | yes      | `true` = ON, `false` = OFF  |

```bash
# Turn output ON
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_outp","params":{"on":true}}'

# Turn output OFF
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_outp","params":{"on":false}}'
```

Underlying device command: `OUTP ON` / `OUTP OFF`.

---

### set_ovp — Set OVP Limit

```bash
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_ovp","params":{"value":15.0}}'
```

> **Note:** PD Pocket does not support direct OVP setting in the current driver. This command is accepted as a no-op and returns `{"status":"ok"}`.

---

### set_ocp — Set OCP Limit

| Param   | Type  | Required | Description          |
|---------|-------|----------|----------------------|
| `value` | float | yes      | Current in A         |

```bash
curl -X POST http://localhost:8765/api/device/pdp1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_ocp","params":{"value":2.5}}'
```

> **Note:** In the current driver, `set_ocp` maps to `set_curr`, so it sets the current limit.

---

## Lifecycle Endpoints

### Reconnect Device

```bash
# Reconnect using current config
curl -X POST http://localhost:8765/api/device/pdp1/connect \
  -H "Content-Type: application/json" \
  -d '{}'

# Reconnect with new serial settings
curl -X POST http://localhost:8765/api/device/pdp1/connect \
  -H "Content-Type: application/json" \
  -d '{"port":"COM6","baud":115200}'
```

---

### Disconnect Device

```bash
curl -X POST http://localhost:8765/api/device/pdp1/disconnect
```

---

### Rename Device

```bash
curl -X POST http://localhost:8765/api/device/pdp1/rename \
  -H "Content-Type: application/json" \
  -d '{"name":"Bench PD Pocket"}'
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
powerwing --device pdp1 --volt 12.0
powerwing --device pdp1 --curr 2.0
powerwing --device pdp1 --outp on
powerwing --device pdp1 --ocp 2.5
```

---

## Notes

- On first state poll after connect, PD Pocket set-points are queried using `get vset` and `get iset`.
- Measurements are queried with `MEAS:VOLT?`, `MEAS:CURR?`, and `MEAS:POW?`.
- Temperature is queried with `gettemp 1` roughly every 10 polling cycles.
- Output state is inferred from explicit `set_outp` commands or measured voltage/power when unknown.
- After 3 consecutive communication errors, the device is auto-disconnected and will retry reconnect every 5 seconds.
