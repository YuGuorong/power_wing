# USB Alarm Light REST API

USB Alarm Light is a 7-color industrial alarm light controlled via Modbus RTU over USB serial (4800 baud, 8N1, station ID 0x01).

**Device type:** `usbalarm`  
**Interface:** `device.AlarmLight`  
**Default baud:** 4800  
**Protocol:** Modbus RTU (FC06 for direct control, FC10 for PWM) (see [usb_alarm_light_doc.md](../usb_alarm_light_doc.md))

---

## Device Config

Add an alarm light device via `POST /api/config/device`:

```json
{
  "id": "alarm1",
  "name": "Alarm Light",
  "type": "usbalarm",
  "port": "COM6",
  "baud": 4800,
  "enabled": true
}
```

| Field     | Type   | Description                                    |
|-----------|--------|------------------------------------------------|
| `id`      | string | Unique device identifier                       |
| `name`    | string | Display name                                   |
| `type`    | string | Must be `"usbalarm"`                           |
| `port`    | string | Serial port (e.g. `"COM6"`, `/dev/ttyUSB2`)     |
| `baud`    | int    | Baud rate; `0` or `4800` = default             |
| `enabled` | bool   | Whether device is active                       |

### Example

```bash
curl -X POST http://localhost:8765/api/config/device \
  -H "Content-Type: application/json" \
  -d '{"id":"alarm1","name":"Alarm Light","type":"usbalarm","port":"COM6","baud":4800,"enabled":true}'
```

**Response:**

```json
{"status": "ok"}
```

---

## Remove Device Config

```bash
curl -X DELETE http://localhost:8765/api/config/device/alarm1
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
    "id": "alarm1",
    "type": "usbalarm",
    "name": "Alarm Light",
    "connected": true,
    "port": "COM6",
    "baud": 4800,
    "state": { ... }
  }
]
```

---

## Get Device State

```bash
curl http://localhost:8765/api/device/alarm1/state
```

### AlarmState Schema

```json
{
  "connected": true,
  "color": "red",
  "mode": "on",
  "buzzer_on": false,
  "pwm_r": 0,
  "pwm_g": 0,
  "pwm_b": 0,
  "flash_interval": 0
}
```

| Field            | Type   | Description                                        |
|------------------|--------|----------------------------------------------------|
| `connected`      | bool   | Whether device is connected                        |
| `color`          | string | Current color: `"red"`, `"yellow"`, `"green"`, `"white"`, `"cyan"`, `"purple"`, `"blue"`, `"off"`, `"pwm"` |
| `mode`           | string | Current mode: `"on"`, `"blink_slow"`, `"blink_fast"`, `"off"`, `"pwm"` |
| `buzzer_on`      | bool   | Whether buzzer is active                           |
| `pwm_r`          | int    | PWM red value (0–1999)                             |
| `pwm_g`          | int    | PWM green value (0–1999)                           |
| `pwm_b`          | int    | PWM blue value (0–1999)                            |
| `flash_interval` | int    | Flash period in 100ms units (0 = always on, max 100) |

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

### alarm_set_color — Set Light Color / Animation

Light up one of 7 colors in steady, slow-blink, fast-blink, or animation mode.

| Param      | Type   | Required | Description                                           |
|------------|--------|----------|-------------------------------------------------------|
| `color`    | string | yes      | `"red"`, `"yellow"`, `"green"`, `"white"`, `"cyan"`, `"purple"`, `"blue"` |
| `mode`     | string | no       | `"on"` (default), `"blink_slow"`, `"blink_fast"`, `"animation"` |
| `color2`   | string | animation| Target color for fade animation (required when mode=animation) |
| `duration` | number | no       | Animation duration in seconds (default 2.0)           |

```bash
# Red steady on
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red","mode":"on"}}'

# Yellow slow blink
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"yellow","mode":"blink_slow"}}'

# Blue fast blink
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"blue","mode":"blink_fast"}}'

# Animation: fade from red to yellow over 3 seconds
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red","color2":"yellow","mode":"animation","duration":3}}'
```

Underlying device command: Modbus FC06 (steady/blink) or FC10 PWM fade (animation).

---

### alarm_buzzer — Turn Buzzer ON/OFF

| Param | Type | Required | Description                 |
|-------|------|----------|------------------------------|
| `on`  | bool | yes      | `true` = buzzer on, `false` = buzzer off |

```bash
# Buzzer ON
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_buzzer","params":{"on":true}}'

# Buzzer OFF
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_buzzer","params":{"on":false}}'
```

Underlying device command: Modbus FC06 write to register `0x00C2` with value `0x40` (on) or `0x41` (off).

---

### alarm_off — Turn Everything Off

Turn off all lights and buzzer.

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| —     | —    | —        | No params needed |

```bash
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_off","params":{}}'
```

Underlying device command: Modbus FC06 write to register `0x00C2` with value `0x60`.

---

### alarm_beep — Flash + Beep for Duration

Light up a color and sound the buzzer for a specified duration, then turn everything off.

| Param   | Type   | Required | Description                        |
|---------|--------|----------|------------------------------------|
| `color` | string | yes      | Same colors as `alarm_set_color`   |
| `ms`    | int    | yes      | Duration in milliseconds (default: 400 if ≤ 0) |

```bash
# Red beep for 500ms
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_beep","params":{"color":"red","ms":500}}'
```

> **Note:** This command is blocking on the server side (sleeps for `ms` duration), but the API returns `{"status":"ok"}` immediately. The device state update arrives after the beep completes.

Underlying sequence: FC06 `colorSteady[color]` → FC06 `0x40` (buzzer on) → sleep `ms` → `alarm_off`.

---

### alarm_pwm — Custom PWM Color Control

Set custom RGB values with optional flash interval and buzzer via Modbus FC10 (write 7 registers).

| Param             | Type | Required | Description                                 |
|-------------------|------|----------|---------------------------------------------|
| `red`             | int  | no       | Red PWM (0–1999, default 0)                 |
| `green`           | int  | no       | Green PWM (0–1999, default 0)               |
| `blue`            | int  | no       | Blue PWM (0–1999, default 0)                |
| `flash_interval`  | int  | no       | Flash period in 100ms units (0–100, default 0 = always on) |
| `buzzer`          | bool | no       | Enable buzzer (default false)               |

```bash
# Bright red, always on, no buzzer
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_pwm","params":{"red":1999,"green":0,"blue":0,"flash_interval":0,"buzzer":false}}'

# Dim cyan, slow flash (1 second), buzzer on
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_pwm","params":{"red":0,"green":500,"blue":500,"flash_interval":10,"buzzer":true}}'
```

Underlying device command: Modbus FC10 write to registers starting at `0x0002`, 7 registers: `[blue, green, light_on/off, flash_interval, buzzer, reserved, red]`.

---

### alarm_rainbow — Rainbow Cycle Demo

Cycle through all 7 colors in sequence, then turn off.

| Param      | Type  | Required | Description                              |
|------------|-------|----------|------------------------------------------|
| `duration` | float | no       | Total cycle duration in seconds (default 2.0) |

```bash
# 3-second rainbow
curl -X POST http://localhost:8765/api/device/alarm1/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_rainbow","params":{"duration":3.0}}'
```

> **Note:** This command runs asynchronously. The API returns `{"status":"ok"}` immediately. The device cycles through: red → yellow → green → cyan → blue → purple → white, then turns off after a brief pause.

---

## Lifecycle Endpoints

### Reconnect Device

```bash
# Reconnect using current config
curl -X POST http://localhost:8765/api/device/alarm1/connect \
  -H "Content-Type: application/json" \
  -d '{}'

# Reconnect with new serial settings
curl -X POST http://localhost:8765/api/device/alarm1/connect \
  -H "Content-Type: application/json" \
  -d '{"port":"COM8","baud":4800}'
```

---

### Disconnect Device

```bash
curl -X POST http://localhost:8765/api/device/alarm1/disconnect
```

---

### Rename Device

```bash
curl -X POST http://localhost:8765/api/device/alarm1/rename \
  -H "Content-Type: application/json" \
  -d '{"name":"Factory Alarm"}'
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

## Color Reference

| Color    | Steady (mode `"on"`) | Slow blink (mode `"blink_slow"`) | Fast blink (mode `"blink_fast"`) |
|----------|----------------------|----------------------------------|----------------------------------|
| Red      | `0x11`               | `0x21`                           | `0x31`                           |
| Yellow   | `0x12`               | `0x22`                           | `0x32`                           |
| Green    | `0x13`               | `0x23`                           | `0x33`                           |
| White    | `0x15`               | `0x25`                           | `0x35`                           |
| Cyan     | `0x16`               | `0x26`                           | `0x36`                           |
| Purple   | `0x17`               | `0x27`                           | `0x37`                           |
| Blue     | `0x18`               | `0x28`                           | `0x38`                           |
| Buzzer ON| `0x40`               | —                                | —                                |
| Buzzer OFF| `0x41`              | —                                | —                                |
| All OFF  | `0x60`               | —                                | —                                |

---

## Notes

- The device does not report its full state over Modbus. The driver tracks state locally based on commands sent.
- Modbus RTU requires a 3.5-character silent interval (~7.3ms at 4800 baud) between frames; the driver enforces this with an 8ms sleep before each write.
- `alarm_beep` and `alarm_rainbow` are blocking operations that run in background goroutines. The API returns immediately while the operation completes asynchronously.
- Setting a color via `alarm_set_color` implicitly turns off the buzzer.
- PWM values are clamped to 0–1999; flash interval is clamped to 0–100.
