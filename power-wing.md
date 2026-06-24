# PowerWing - Cross-Platform Power Supply & USB Hub Controller

**Runs on:** Windows 10/11 and Ubuntu Linux (all versions)

## Features

1. **Web UI** — Easy-to-use responsive interface accessible via browser
2. **Cross-Platform** — Single codebase runs natively on Windows and Linux
3. **Extensible** — Plug-in architecture supports variant equipment via USB/HID/UART
4. **Voice Commands** — PC microphone/speaker voice control (Windows only)
5. **Command Line** — Full CLI control for scripting and automation
6. **MCP Server** — AI agent integration via Model Context Protocol (stdio + TCP)
7. **Per-Device UI** — Individual pages for each power supply or hub
8. **System Tray** — Quick access from system tray (Windows only)
9. **Auto-Config** — Configuration auto-saved to platform-specific user config dir

## Supported Hardware

- **SPM3051** — UART lab power supply (9600 baud, SCPI protocol) — see [SPM3051-CMD.txt](SPM3051-CMD.txt)
- **PD_Pocket** — UART programmable PSU (115200 baud) — see [PD_Pocket API.txt](PD_Pocket%20API.txt)
- **USB-Slim** — UART USB hub 4-port controller (9600 baud) — see [Usb-slim-api.txt](Usb-slim-api.txt)

## Power Supply Features

- Get/set output voltage, current
- Get/set output enable/disable
- Get/set OCP/OVP protection limits
- Mouse-free number entry for voltage/current
- Real-time state monitoring

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
.\power_wing.exe

# CLI mode (execute command and exit)
.\power_wing.exe --device psu1 --volt 5.0
.\power_wing.exe --list-devices
.\power_wing.exe --list-ports
```

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

PowerWing includes a Model Context Protocol (MCP) server with both **stdio** (local) and **TCP** (network) transports.

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
- **powerwing_send_command** — Execute device command (set_volt, set_outp, set_usb_port, etc.)
- **powerwing_list_serial_ports** — Enumerate available serial ports
- **powerwing_disconnect_device** — Disconnect device (manual mode)
- **powerwing_reconnect_device** — Reconnect device with optional new port/baud
- **powerwing_rename_device** — Change device display name
- **powerwing_test_connection** — Probe all serial ports to discover where a device type is connected
- **powerwing_get_config** — Return the full current configuration (all devices, server port, feature flags)

### Protocol Details

- **Version:** MCP 2024-11-05
- **Framing:** JSON-RPC 2.0 with Content-Length header (stdio and TCP)
- **Both stdio and TCP:** Support full bidirectional communication
- **Persistence:** All commands persist state back to config file
