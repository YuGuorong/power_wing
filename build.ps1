# build.ps1 – build power_wing.exe on Windows
#
# Usage:
#   .\build.ps1             # version 1.0.0 (default)
#   .\build.ps1 -Version 1.2.3
#
# Requires:
#   - Go toolchain
#   - goversioninfo  (go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest)
#
param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = "Stop"
$Output = "power_wing.exe"

# ── 1. Patch versioninfo.json ────────────────────────────────────────────────
Write-Host "==> Patching versioninfo.json ($Version)"
$parts = $Version -split '\.'
$major = if ($parts.Count -gt 0) { [int]$parts[0] } else { 1 }
$minor = if ($parts.Count -gt 1) { [int]$parts[1] } else { 0 }
$patch = if ($parts.Count -gt 2) { [int]$parts[2] } else { 0 }

$json = Get-Content versioninfo.json -Raw | ConvertFrom-Json

$json.FixedFileInfo.FileVersion.Major    = $major
$json.FixedFileInfo.FileVersion.Minor    = $minor
$json.FixedFileInfo.FileVersion.Patch    = $patch
$json.FixedFileInfo.FileVersion.Build    = 0
$json.FixedFileInfo.ProductVersion.Major = $major
$json.FixedFileInfo.ProductVersion.Minor = $minor
$json.FixedFileInfo.ProductVersion.Patch = $patch
$json.FixedFileInfo.ProductVersion.Build = 0
$json.StringFileInfo.FileVersion         = "$Version.0"
$json.StringFileInfo.ProductVersion      = "$Version.0"

# Write UTF-8 without BOM; goversioninfo fails on BOM-prefixed JSON.
$jsonText = $json | ConvertTo-Json -Depth 10
[System.IO.File]::WriteAllText((Join-Path $PWD "versioninfo.json"), $jsonText, [System.Text.UTF8Encoding]::new($false))

# ── 3. Generate rsrc_windows.syso ────────────────────────────────────────────
Write-Host "==> Generating rsrc_windows.syso (icon + version info)"
& "$env:GOPATH\bin\goversioninfo.exe" -o rsrc_windows.syso
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ── 4. Build executable ──────────────────────────────────────────────────────
Write-Host "==> Building $Output"
$env:GOOS   = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o $Output .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> Done: $Output"
