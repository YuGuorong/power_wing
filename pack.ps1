# pack.ps1 – package power_wing.exe for distribution
#
# Usage:
#   .\pack.ps1                  # version 1.0.0 (default)
#   .\pack.ps1 -Version 1.2.3  # custom version
#
# Produces: dist/PowerWing-1.0.0.zip
#
param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = "Stop"
$DistDir  = "dist"
$ZipName  = "PowerWing-$Version.zip"
$ZipPath  = Join-Path $DistDir $ZipName

# ── 1. Build Windows binary ──────────────────────────────────────────────────
Write-Host "==> Building power_wing.exe ($Version)"
& ".\build.ps1" -Version $Version
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ── 1b. Build Linux binary ───────────────────────────────────────────────────
Write-Host "==> Building power_wing_linux_amd64 ($Version)"
$env:GOOS   = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags="-s -w -X github.com/yuguorong/power_wing/internal/version.Version=$Version" -o power_wing_linux_amd64 .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$env:GOOS = $null; $env:GOARCH = $null; $env:CGO_ENABLED = $null

# ── 2. Prepare dist folder ──────────────────────────────────────────────────
if (Test-Path $DistDir) { Remove-Item $DistDir -Recurse -Force }
New-Item -ItemType Directory -Path $DistDir -Force | Out-Null

$Staging = Join-Path $DistDir "staging"
New-Item -ItemType Directory -Path $Staging -Force | Out-Null

# ── 3. Copy files to staging ────────────────────────────────────────────────
Copy-Item "power_wing.exe"           $Staging
Copy-Item "power_wing_linux_amd64"   $Staging
Copy-Item "power-wing.md"            $Staging
Copy-Item "docs"                     $Staging -Recurse
Copy-Item "app.ico"                  $Staging

# ── 4. Write a README for end users ─────────────────────────────────────────
$Readme = @"
PowerWing $Version - Cross-Platform Power Supply & USB Hub Controller
====================================================================

RUNS ON: Windows 10/11, Ubuntu Linux (all versions)

PACKAGE CONTENTS
----------------
- power_wing.exe           Windows executable
- power_wing_linux_amd64   Linux amd64 executable
- power-wing.md            Full documentation
- docs\                    REST/WebSocket/MCP API reference and per-device notes
- app.ico                  Application icon
- README.txt               This file

QUICK START (Windows)
---------------------
1. Double-click power_wing.exe (or run it from cmd/PowerShell)
   - No console window: it runs in the system tray with the Web UI
   - Web UI opens automatically at http://127.0.0.1:8765
   - Config saved to: %AppData%\PowerWing\config.json
   - Logs: %AppData%\PowerWing\power_wing.log
   - Quit: right-click the tray icon -> Quit
   - MCP is opt-in: double-click starts only the Web UI + tray. To run MCP
     alongside them: --mcp=true --mcp-transport tcp --mcp-host 0.0.0.0
     --mcp-port 9876 (do not use --mcp-only). No authentication; use
     --mcp-host 127.0.0.1 for local-only access.
   - Serial ports are NOT opened at startup. Click Connect on a device page
     (or Connect All in Settings) when you need a device. Use --auto-connect
     to open every enabled device at startup.
   - Settings > Serial Port Debug shows every port open/close and its owner.

2. Command-line control:
   power_wing.exe --list-ports
   power_wing.exe --list-devices
   power_wing.exe --device <id> --volt 5.0
   power_wing.exe --device <id> --outp on

3. MCP server for AI (local):
   power_wing.exe --mcp-only

4. MCP server for AI (network):
   power_wing.exe --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876

QUICK START (Linux / Ubuntu)
-----------------------------
1. Make executable and run:
   chmod +x power_wing_linux_amd64
   ./power_wing_linux_amd64 --no-browser &
   (serial ports are opened only when you click Connect; add --auto-connect
    to open all enabled devices at startup)

2. Access Web UI:
   http://localhost:8765
   Config saved to: ~/.config/PowerWing/config.json

3. Command-line control:
   ./power_wing_linux_amd64 --list-ports
   ./power_wing_linux_amd64 --list-devices
   ./power_wing_linux_amd64 --device <id> --volt 5.0

4. MCP server for AI (network):
   ./power_wing_linux_amd64 --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876

5. Optional - grant serial port access without sudo:
   sudo usermod -aG dialout $USER
   (log out and back in)

SERIAL PORTS
------------
Windows:  COM1, COM2, COM3, etc. (typically USB devices appear as COMn)
Linux:    /dev/ttyUSB0, /dev/ttyACM0, /dev/ttyAMA0, etc.

MCP SERVER MODES
----------------
STDIO (local AI agents):
  Windows: { "command": "power_wing.exe", "args": ["--mcp-only"] }
  Linux:   { "command": "./power_wing_linux_amd64", "args": ["--mcp-only"] }

TCP (network AI agents, LAN):
  ./power_wing_linux_amd64 --mcp-only --mcp-transport tcp --mcp-host 0.0.0.0 --mcp-port 9876
  Then connect the client to: tcp://<host>:9876

Streamable HTTP (available whenever the web server runs):
  POST http://<host>:8765/mcp

MCP TOOLS AVAILABLE
-------------------
- powerwing_list_devices       List all configured devices
- powerwing_get_device_state   Get current state of device
- powerwing_send_command       Execute a device command
- powerwing_list_serial_ports  Enumerate available serial ports
- powerwing_disconnect_device  Disconnect a device
- powerwing_reconnect_device   Reconnect or reconfigure device
- powerwing_rename_device      Rename device for UI display
- powerwing_test_connection    Probe configured devices of a type
- powerwing_get_config         Read the full configuration
- powerwing_set_config         Replace config or update one device

See docs\api_reference.md for every REST endpoint, WebSocket message,
device command and state field.

SUPPORTED HARDWARE
------------------
- SPM3051          UART lab power supply with built-in multimeter (9600 baud)
- PD_Pocket        UART-based programmable power supply (115200 baud)
- USB-Slim         UART-based 4-port USB hub (9600 baud)
- SanGenXian       UART-based 12-port USB hub (4800 baud)
- USB Alarm Light  7-color Modbus RTU alarm light (4800 baud)
- F-11             AFU IoT power monitor (38400 baud)
- POWER-Z          ChargerLAB KM002C/KM003C USB power meter (CDC port)

CONFIGURATION
-------------
Edit the config file manually or use the Web UI:
  Windows: %AppData%\PowerWing\config.json
  Linux:   ~/.config/PowerWing/config.json

Example config entry:
  {
    "id": "psu1",
    "name": "Lab PSU",
    "type": "spm3051",
    "port": "COM3",     (or "/dev/ttyUSB0" on Linux)
    "baud": 9600,
    "enabled": true
  }

TROUBLESHOOTING
---------------
Serial port not found?
  - Windows: Check Device Manager > Ports (COM & LPT)
  - Linux: ls /dev/tty* and check permissions with ls -l /dev/ttyUSB*

Permission denied on serial port (Linux)?
  - Run: sudo usermod -aG dialout $USER
  - Then log out and back in

Connection errors?
  - Check port name and baud rate in config
  - Try manually setting port in config JSON
  - Verify device is powered on and connected

Build from source:
  - Windows: .\build.ps1
  - Linux: ./build.sh
"@
[System.IO.File]::WriteAllText(
    (Join-Path $Staging "README.txt"),
    $Readme,
    [System.Text.UTF8Encoding]::new($false)
)

# ── 5. Create ZIP ────────────────────────────────────────────────────────────
Write-Host "==> Creating $ZipName"
Compress-Archive -Path (Join-Path $Staging "*") -DestinationPath $ZipPath -Force

# ── 6. Clean up staging ─────────────────────────────────────────────────────
Remove-Item $Staging -Recurse -Force

$Size = (Get-Item $ZipPath).Length / 1MB
Write-Host "==> Done: $ZipPath ($([math]::Round($Size, 2)) MB)"
Write-Host ""
Write-Host "To install on another machine:"
Write-Host "  1. Copy $ZipName to target"
Write-Host "  2. Extract ZIP"
Write-Host "  3. Run power_wing.exe"
