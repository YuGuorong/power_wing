#!/usr/bin/env bash
# build.sh – build power_wing for Windows and/or Linux
#
# Usage:
#   ./build.sh                  # build for current OS
#   ./build.sh [version]        # build for current OS with version
#   ./build.sh [version] [os]   # build for specific OS (windows or linux)
#
# Examples:
#   ./build.sh 1.2.0           # native build
#   ./build.sh 1.2.0 windows    # cross-compile for Windows
#   ./build.sh 1.2.0 linux      # cross-compile for Linux
#
# Requires:
#   - Go toolchain
#   - goversioninfo (only for Windows builds)
#     go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
#
set -euo pipefail

VERSION="${1:-1.0.0}"
TARGET_OS="${2:-$(go env GOOS)}"
BUILD=0

if [ "$TARGET_OS" = "windows" ]; then
    OUTPUT="power_wing.exe"
else
    OUTPUT="power_wing"
fi

echo "==> Building ${OUTPUT} (GOOS=${TARGET_OS} GOARCH=amd64, version ${VERSION})"

# ── Patch versioninfo.json (needed for Windows .syso resource) ─────────────
if [ "$TARGET_OS" = "windows" ]; then
    echo "==> Patching versioninfo.json (${VERSION})"
    IFS='.' read -r MAJOR MINOR PATCH <<< "${VERSION}"

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
fi

echo "==> Compiling"
CGO_ENABLED=0 GOOS=${TARGET_OS} GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "${OUTPUT}" .

echo "==> Done: ${OUTPUT}"
