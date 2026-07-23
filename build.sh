#!/usr/bin/env bash
# build.sh – build power_wing.exe for Windows (cross-compile or native)
#
# Usage:
#   ./build.sh [version]      e.g.  ./build.sh 1.2.0
#
# Requires:
#   - Go toolchain
#   - goversioninfo  (go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest)
#
set -euo pipefail

VERSION="${1:-1.0.0}"
OUTPUT="power_wing.exe"
GOOS=windows
GOARCH=amd64

echo "==> Patching version in versioninfo.json ($VERSION)"
IFS='.' read -r MAJOR MINOR PATCH <<< "${VERSION}"
BUILD=0

# Use sed to update the four version fields – works on Linux/macOS/Git-Bash.
sed -i.bak \
    -e "/\"FileVersion\"/,/\"Build\"/{
        s/\"Major\": [0-9]*/\"Major\": ${MAJOR}/
        s/\"Minor\": [0-9]*/\"Minor\": ${MINOR}/
        s/\"Patch\": [0-9]*/\"Patch\": ${PATCH}/
        s/\"Build\": [0-9]*/\"Build\": ${BUILD}/
    }" \
    -e "/\"ProductVersion\"/,/\"Build\"/{
        s/\"Major\": [0-9]*/\"Major\": ${MAJOR}/
        s/\"Minor\": [0-9]*/\"Minor\": ${MINOR}/
        s/\"Patch\": [0-9]*/\"Patch\": ${PATCH}/
        s/\"Build\": [0-9]*/\"Build\": ${BUILD}/
    }" \
    versioninfo.json
rm -f versioninfo.json.bak

echo "==> Generating rsrc_windows.syso (icon + version info)"
goversioninfo -o rsrc_windows.syso

echo "==> Building ${OUTPUT} (GOOS=${GOOS} GOARCH=${GOARCH})"
GOOS=${GOOS} GOARCH=${GOARCH} go build -trimpath -ldflags="-s -w" -o "${OUTPUT}" .

echo "==> Done: ${OUTPUT}"
