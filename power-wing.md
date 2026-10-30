# PowerWing - Cross-Platform Power Supply & USB Hub Controller

**Runs on:** Windows 10/11 and Ubuntu Linux (all versions)

## Features

1. **Web UI** — Easy-to-use responsive interface accessible via browser
2. **Cross-Platform** — Single codebase runs natively on Windows and Linux
3. **Extensible** — Plug-in architecture supports variant equipment via USB/HID/UART
4. **Voice Commands** — PC microphone/speaker voice control (Windows only)
5. **Command Line** — Full CLI control for scripting and automation
6. **MCP Server** — AI agent integration via Model Context Protocol (stdio + TCP + Streamable HTTP)
7. **REST API** — Full device/state/command API at `/api/...` (see [docs/api_reference.md](docs/api_reference.md))
8. **Per-Device UI** — Individual pages for each power supply or hub
9. **System Tray** — Quick access from system tray (Windows only)
10. **Auto-Config** — Configuration auto-saved to platform-specific user config dir
11. **Serial Port Debug** — Settings panel showing every serial open/close with owner, baud and caller
12. **Windowless Background Mode** — Double-click `power_wing.exe` to run in the system tray without a console window
13. **All Ports ON/OFF** — One-click master switches in every hub panel and `all-on` / `all-off` REST endpoints

## Supported Hardware

- **SPM3051** — UART lab power supply with built-in 4½-digit multimeter (9600 baud, SCPI protocol) — see [docs/api_spm3051.md](docs/api_spm3051.md) and [SPM3051-CMD.txt](SPM3051-CMD.txt)
- **PD_Pocket** — UART programmable PSU (115200 baud) — see [PD_Pocket API.txt](PD_Pocket%20API.txt)
- **USB-Slim** — UART USB hub 4-port controller (9600 baud) — see [Usb-slim-api.txt](Usb-slim-api.txt)
- **SanGenXian** — UART USB hub 12-port controller (4800 baud) — see [docs/api_sangenxian.md](docs/api_sangenxian.md)
- **USB Alarm Light** — Modbus RTU 7-color alarm light (4800 baud) — see [alarm_light_api.md](alarm_light_api.md)
- **F-11** — AFU IoT current/voltage monitor (38400 baud) — see [docs/api_f11.md](docs/api_f11.md)
- **POWER-Z** — ChargerLAB KM002C/KM003C USB power meter (CDC port, baud ignored) — see [docs/api_powerz.md](docs/api_powerz.md)

## Serial Port Safety

PowerWing **never opens a serial port at startup by default**. Configured devices are registered but stay disconnected until you:

- click **Connect** on the device's port panel, or
- click **Connect All** in Settings, or
- send a command to that device (MCP / REST / CLI).

Automatic reconnect only applies to devices that were successfully opened at least once in the current process. A stale `COMx` assignment is never touched by the background loop. Pass `--auto-connect` to opt back into opening every enabled device at startup.

Every open attempt, failure and close is recorded (owner, port, baud, source file/line) in **Settings → Serial Port Debug** and in the process log.

## Power Supply Features

- Get/set output voltage, current
- Get/set output enable/disable
- Get/set OCP/OVP protection limits
- Mouse-free number entry for voltage/current
- Real-time state monitoring
- Built-in multimeter (SPM3051): live reading, function/range selection, HOLD and NULL — see [docs/api_spm3051.md](docs/api_spm3051.md)
- Front-panel release: the panel is only locked while a setting is being applied (SPM3051)

## Building

### Windows

```powershell
.\build.ps1                # build power_wing.exe (version 1.0.0)
.\build.ps1 -Version 1.2.0 # build with custom version
```

### Linux / Ubuntu

```bash
./build.sh                 # build power_wing (native)
./build.sh 1.2.0          # build with custom version
./build.sh 1.2.0 windows   # cross-compile for Windows (requires Go on Linux)
```

## Installation & Running

### Windows

```powershell
# GUI mode (Web UI + system tray)
# Double-click power_wing.exe in Explorer: no console window is shown.
.\power_wing.exe

# CLI mode (execute command and exit; run from a terminal)
.\power_wing.exe --device psu1 --volt 5.0
.\power_wing.exe --list-devices
.\power_wing.exe --list-ports
```

The Windows binary is a GUI-subsystem application. Double-clicking it runs
silently in the background with the system-tray icon and the web UI. It
attaches to the existing console when started from cmd/PowerShell, so CLI
output still works.

- **Log file:** `%AppData%\PowerWing\power_wing.log`
- **Quit:** right-click the tray icon → Quit
- **MCP:** opt-in. Double-click starts only the Web UI + tray. To run MCP
  together with them, use
  `--mcp=true --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876`
  (do **not** add `--mcp-only`, which disables the Web UI and tray). The MCP
  TCP endpoint has no authentication; bind `127.0.0.1` instead of `0.0.0.0`
  if you do not want LAN access.

### Linux / Ubuntu

```bash
chmod +x power_wing_linux_amd64
./power_wing_linux_amd64 --no-browser &

# May need serial port access
sudo usermod -aG dialout $USER
# (log out and back in)
```

## Configuration

Config is auto-created and saved to:
- **Windows:** `%AppData%\PowerWing\config.json`
- **Linux:** `~/.config/PowerWing/config.json`

Example config entry:

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

Serial port names:
- **Windows:** `COM1`, `COM2`, `COM3`, etc. (check Device Manager)
- **Linux:** `/dev/ttyUSB0`, `/dev/ttyACM0`, `/dev/ttyAMA0`, etc. (check `ls /dev/tty*`)

## MCP Server Support (for AI Calling)

PowerWing includes a Model Context Protocol (MCP) server with **stdio** (local),
**TCP** (network) and **Streamable HTTP** (`POST /mcp` on the web server) transports.

A complete reference for REST, WebSocket, MCP tools, device commands and state
objects is in [docs/api_reference.md](docs/api_reference.md).

### MCP Transports

#### Stdio Transport (Local AI agents on same machine)

```bash
# Windows
power_wing.exe --mcp-only --mcp-transport stdio

# Linux
./power_wing_linux_amd64 --mcp-only --mcp-transport stdio
```

Client config (Hermes, Copilot CLI, OpenCode):
```json
{
  "command": "power_wing.exe",
  "args": ["--mcp-only", "--mcp-transport", "stdio"]
}
```

#### TCP Transport (Network AI agents on LAN)

```bash
# Windows
power_wing.exe --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876

# Linux
./power_wing_linux_amd64 --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876
```

AI clients connect to: `http://<host-ip>:9876`

### Run Modes

**MCP Only (no Web UI):**
```bash
power_wing --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876
```

**Web UI + Stdio MCP:**
```bash
power_wing --port 8765 --mcp --mcp-transport stdio
```

**Web UI + TCP MCP:**
```bash
power_wing --port 8765 --mcp --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876
```

### MCP Tools Exposed

All tools work on both Windows and Linux:

- **powerwing_list_devices** — List all configured devices with connection status
- **powerwing_get_device_state** — Get current measured values and settings
- **powerwing_send_command** — Execute device command (set_volt, set_outp, set_usb_port, release_control, meter_set_*, etc. — see [docs/api_reference.md §4](docs/api_reference.md#4-device-commands))
- **powerwing_list_serial_ports** — Enumerate available serial ports
- **powerwing_disconnect_device** — Disconnect device (manual mode)
- **powerwing_reconnect_device** — Reconnect device with optional new port/baud
- **powerwing_rename_device** — Change device display name
- **powerwing_test_connection** — Check whether configured devices of a type (spm3051, pd_pocket, usbslim, sangenxian, usbalarm, f11, powerz) respond on their configured ports (never opens non-configured ports)
- **powerwing_get_config** — Return the full current configuration (all devices, server port, feature flags)
- **powerwing_set_config** — Set configuration (counterpart of get_config): pass the full `devices` list, or `device_id` + optional name/port/baud/enabled to update one device; omitted fields keep current values. Use with `powerwing_list_serial_ports` to assign ports.

### Protocol Details

- **Version:** MCP 2024-11-05
- **Framing:** JSON-RPC 2.0 with Content-Length header (stdio and TCP)
- **Both stdio and TCP:** Support full bidirectional communication
- **Persistence:** All commands persist state back to config file
