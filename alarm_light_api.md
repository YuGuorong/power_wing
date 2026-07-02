# USB 7-Color Alarm Light — REST API Reference

Base URL: `http://<host>:8765`

---

## 1. Discover devices

**List all configured devices** (find the alarm light's device ID):

```
GET /api/devices
```

Response example:
```json
[
  {
    "id": "AlarmLight",
    "name": "AlarmLight",
    "type": "usbalarm",
    "port": "COM44",
    "baud": 4800,
    "connected": true,
    "state": null
  }
]
```

Make note of the `id` field — you'll need it in all command URLs.

---

## 2. Read device state

```
GET /api/device/{id}/state
```

Response:
```json
{
  "connected": true,
  "color": "red",
  "mode": "blink_slow",
  "buzzer_on": false,
  "pwm_r": 0,
  "pwm_g": 0,
  "pwm_b": 0,
  "flash_interval": 0
}
```

| Field | Description |
|---|---|
| `connected` | Whether the serial port is currently open |
| `color` | `red`, `yellow`, `green`, `white`, `cyan`, `purple`, `blue`, `pwm`, or `off` |
| `mode` | `on`, `blink_slow`, `blink_fast`, `animation`, or `off` |
| `buzzer_on` | `true` if the buzzer is sounding |
| `pwm_r/g/b` | PWM duty values 0–1999 (only meaningful when color is `pwm`) |
| `flash_interval` | Flash interval in 100ms units (PWM mode only) |

---

## 3. Send commands

All commands use the same endpoint:

```
POST /api/device/{id}/cmd
Content-Type: application/json
```

### 3.1 `alarm_set_color` — Turn on a color (or animate between two colors)

```json
{
  "command": "alarm_set_color",
  "params": {
    "color": "red",
    "mode": "blink_slow"
  }
}
```

| Param | Required | Type | Values |
|---|---|---|---|
| `color` | ✅ yes | string | `red`, `yellow`, `green`, `white`, `cyan`, `purple`, `blue` |
| `mode` | no (default `on`) | string | `on`, `blink_slow`, `blink_fast`, `animation` |
| `color2` | only when `mode:"animation"` | string | target color for the fade |
| `duration` | no (default 2000) | integer | fade duration in **milliseconds** |
| `loop` | no (default -1 = forever) | integer | number of back-and-forth cycles; -1 = loop until cancelled |

**Basic examples:**
```bash
# Red steady on
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red"}}'

# Green fast blink
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"green","mode":"blink_fast"}}'

# Blue slow blink
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"blue","mode":"blink_slow"}}'
```

**Animation mode** — smoothly fades between two colors via PWM interpolation at ~30 fps.
Any other LED command (set_color, buzzer, off, beep, pwm, rainbow) will cancel a running animation.

- `loop=-1` (default): loop forever until cancelled by another command or disconnect
- `loop=1`: single fade `color` → `color2`, then stop at `color2`
- `loop=2`: round trip `color` → `color2` → `color`, then stop at `color`
- `loop=N`: N back-and-forth alternations. Odd N ends on `color2`, even N ends on `color`.

```bash
# Red→yellow fade over 3 seconds
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red","color2":"yellow","mode":"animation","duration":3000}}'

# Red⇄blue pulse: 500ms each way, loop 3 times (red→blue→red→blue, ends on blue)
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red","color2":"blue","mode":"animation","duration":500,"loop":3}}'

# Red→yellow breathing fade, 1 second per cycle, loop forever (default: loop=-1)
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_set_color","params":{"color":"red","color2":"yellow","mode":"animation","duration":1000}}'
```

---

### 3.2 `alarm_buzzer` — Turn buzzer on/off

```json
{
  "command": "alarm_buzzer",
  "params": {
    "on": true
  }
}
```

| Param | Required | Type | Values |
|---|---|---|---|
| `on` | ✅ yes | boolean | `true` / `false` |

> **Note:** The buzzer operates independently of the light. Use `alarm_off` to turn everything off at once.

---

### 3.3 `alarm_off` — Turn off light and buzzer

```json
{
  "command": "alarm_off",
  "params": {}
}
```

No parameters required. Turns off both the light and the buzzer immediately.

---

### 3.4 `alarm_beep` — Light + buzzer for a duration (then auto-off)

```json
{
  "command": "alarm_beep",
  "params": {
    "color": "red",
    "ms": 800
  }
}
```

| Param | Required | Type | Range |
|---|---|---|---|
| `color` | ✅ yes | string | `red`, `yellow`, `green`, `white`, `cyan`, `purple`, `blue` |
| `ms` | ✅ yes | integer | Duration in milliseconds (e.g. 400, 800, 2000) |

The command returns immediately (runs in background). The light turns on in the given color + buzzer sounds, then both turn off automatically after `ms` milliseconds.

**Example:**
```bash
# Red alarm for 1 second
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_beep","params":{"color":"red","ms":1000}}'
```

---

### 3.5 `alarm_pwm` — PWM RGB control (custom color mixing)

```json
{
  "command": "alarm_pwm",
  "params": {
    "red": 1999,
    "green": 500,
    "blue": 0,
    "flash_interval": 0,
    "buzzer": false
  }
}
```

| Param | Required | Type | Range |
|---|---|---|---|
| `red` | no (default 0) | integer | 0–1999 |
| `green` | no (default 0) | integer | 0–1999 |
| `blue` | no (default 0) | integer | 0–1999 |
| `flash_interval` | no (default 0) | integer | 0–100 (unit: 100ms; 10 = 1 second) |
| `buzzer` | no (default false) | boolean | `true` / `false` |

**Examples:**
```bash
# Orange (R=100%, G=25%, B=0)
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_pwm","params":{"red":1999,"green":500,"blue":0}}'

# Purple with buzzer
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_pwm","params":{"red":1999,"green":0,"blue":1999,"buzzer":true}}'

# Flashing blue every 500ms
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_pwm","params":{"red":0,"green":0,"blue":1999,"flash_interval":5}}'
```

> **Note:** PWM mode uses a different Modbus function (FC10) and only writes values; it cannot read back current PWM state from the hardware. After sending a PWM command, the device state shows `"color":"pwm"` and the cached PWM values.

---

### 3.6 `alarm_rainbow` — Cycle through all 7 colors

```json
{
  "command": "alarm_rainbow",
  "params": {
    "duration": 3000
  }
}
```

| Param | Required | Type | Description |
|---|---|---|---|
| `duration` | no (default 2000) | integer | Total time in **milliseconds** to cycle all colors |

The command returns immediately (runs in background). Cycles through red → yellow → green → white → cyan → purple → blue, then turns off.

**Example:**
```bash
# 3.5 second rainbow
curl -X POST http://host:8765/api/device/AlarmLight/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"alarm_rainbow","params":{"duration":3500}}'
```

---

## 4. Quick reference card (for AI agents)

```
BASE=http://<ip>:8765/api/device/{id}

# Set color (steady / blink)
POST $BASE/cmd  {"command":"alarm_set_color",  "params":{"color":"red|yellow|green|white|cyan|purple|blue", "mode":"on|blink_slow|blink_fast"}}

# Fade animation (color→color2, ms duration, loop: -1=forever default, 1=single, N=cycles)
POST $BASE/cmd  {"command":"alarm_set_color",  "params":{"color":"red|...", "color2":"blue|...", "mode":"animation", "duration":3000}}

# Buzzer
POST $BASE/cmd  {"command":"alarm_buzzer",     "params":{"on":true|false}}

# All off
POST $BASE/cmd  {"command":"alarm_off",         "params":{}}

# Timed beep (light+buzzer then auto-off)
POST $BASE/cmd  {"command":"alarm_beep",        "params":{"color":"...", "ms":400}}

# PWM (custom color mixing)
POST $BASE/cmd  {"command":"alarm_pwm",         "params":{"red":0-1999,"green":0-1999,"blue":0-1999,"flash_interval":0-100,"buzzer":true|false}}

# Rainbow cycle (ms)
POST $BASE/cmd  {"command":"alarm_rainbow",     "params":{"duration":3000}}

# Read state
GET  $BASE/state
```

**Colors:** `red` `yellow` `green` `white` `cyan` `purple` `blue`
**Modes:** `on` (steady) `blink_slow` `blink_fast`

---

## 5. Error handling

All errors return HTTP 500 with:

```json
{"error": "description of what went wrong"}
```

Common errors:
- `"unknown device \"X\""` — device ID doesn't exist. Check `/api/devices` for the correct ID (case-sensitive).
- `"X is not an alarm light"` — the device ID exists but its type isn't `usbalarm`.
- `"invalid color"` — color name not in the allowed list.
- `"cannot connect"` — serial port could not be opened (wrong COM port, in use, etc.).
