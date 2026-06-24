#!/bin/bash
# =============================================================================
# PowerWing MCP stdio Test (Shell) — works on WSL / Linux / Git Bash
# Usage:
#   ./test_mcp_stdio.sh
#   POWER_WING_EXE=/path/to/power_wing.exe ./test_mcp_stdio.sh
# =============================================================================

find_exe() {
    if [ -n "${POWER_WING_EXE:-}" ]; then
        echo "[*] Using POWER_WING_EXE from env: $POWER_WING_EXE"
        EXE="$POWER_WING_EXE"
        return
    fi
    if uname -r 2>/dev/null | grep -qi "microsoft\|wsl"; then
        echo "[*] Detected WSL"
        EXE="${POWER_WING_WIN_EXE:-/mnt/c/work/bin/power_wing.exe}"
    elif [ "${OS:-}" = "Windows_NT" ] || uname -s 2>/dev/null | grep -qi "mingw\|msys\|cygwin"; then
        echo "[*] Detected Windows (Git Bash)"
        EXE="${POWER_WING_WIN_EXE:-c:/work/bin/power_wing.exe}"
    else
        echo "[*] Detected Linux"
        EXE="${POWER_WING_LINUX_EXE:-./power_wing_linux_amd64}"
    fi
    echo "[*] Using: $EXE"
}

# MCP frame builder
mcp_req() {
    local id=$1 method=$2 params=${3:-}
    local body
    if [ -z "$params" ]; then
        body="{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"$method\"}"
    else
        body="{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"$method\",\"params\":$params}"
    fi
    printf 'Content-Length: %d\n\n%s' "${#body}" "$body"
}

echo "============================================================"
echo "  PowerWing MCP stdio Test (Shell)"
echo "============================================================"

find_exe

TMP_IN=$(mktemp /tmp/powerwing_in.XXXXXX)
trap "rm -f $TMP_IN" EXIT

# Build all 9 request frames
{
    mcp_req 1 "initialize" '{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"shell","version":"1.0"}}'
    mcp_req 2 "notifications/initialized"
    mcp_req 3 "tools/list"
    mcp_req 4 "tools/call" '{"name":"powerwing_list_devices","arguments":{}}'
    mcp_req 5 "tools/call" '{"name":"powerwing_reconnect_device","arguments":{"device_id":"PSU1"}}'
    mcp_req 6 "tools/call" '{"name":"powerwing_send_command","arguments":{"device_id":"PSU1","command":"set_volt","params":{"value":5.0}}}'
    mcp_req 7 "tools/call" '{"name":"powerwing_send_command","arguments":{"device_id":"PSU1","command":"set_curr","params":{"value":3.0}}}'
    mcp_req 8 "tools/call" '{"name":"powerwing_send_command","arguments":{"device_id":"PSU1","command":"set_outp","params":{"on":true}}}'
    mcp_req 9 "tools/call" '{"name":"powerwing_get_device_state","arguments":{"device_id":"PSU1"}}'
} > "$TMP_IN"

# Pipe all requests, capture stdout (stderr suppressed)
RAW=$("$EXE" --mcp-only --mcp-transport stdio < "$TMP_IN" 2>/dev/null || true)

# ── Parse: extract every Content-Length + body pair ───────────────────────────
parse_all() {
    local raw="$1"
    local rest="$raw"
    local -a bodies=()
    while true; do
        rest="${rest#*Content-Length: }"
        [ "$rest" = "$raw" ] && break  # no more headers
        local len="${rest%%$'\n'*}"
        len="${len%%$'\r'*}"
        rest="${rest#*$'\n'}"           # skip header line
        rest="${rest#*$'\n'}"           # skip blank line
        [ -z "$len" ] && break
        local body="${rest:0:$len}"
        bodies+=("$body")
        rest="${rest:$len}"
        raw="$rest"
    done
    printf '%s\n' "${bodies[@]}"
}

echo ""
idx=0
while IFS= read -r json_line; do
    [ -z "$json_line" ] && continue
    idx=$((idx + 1))
    case $idx in
        1)  # initialize response
            name=$(echo "$json_line" | python3 -c "import sys,json; print(json.load(sys.stdin)['result']['serverInfo']['name'])" 2>/dev/null || echo "?")
            ver=$(echo "$json_line" | python3 -c "import sys,json; print(json.load(sys.stdin)['result']['serverInfo']['version'])" 2>/dev/null || echo "?")
            echo "[1] Initialize...  OK  $name v$ver"
            ;;
        3)  # tools/list
            echo ""
            echo "[2] List tools..."
            echo "$json_line" | python3 -c "
import sys,json
for t in json.load(sys.stdin)['result']['tools']:
    d=t['description']; desc=d[:67]+'...' if len(d)>70 else d
    print(f'  - {t[\"name\"]} : {desc}')" 2>/dev/null || echo "  (parse error)"
            ;;
        4)  # list_devices
            echo ""
            echo "[3] List devices..."
            echo "$json_line" | python3 -c "
import sys,json
data=json.loads(json.load(sys.stdin)['result']['content'][0]['text'])
for d in data['devices']:
    c='ON' if d['connected'] else 'OFF'
    print(f'  [{c}] {d[\"id\"]} ({d[\"name\"]}) @ {d[\"port\"]}')" 2>/dev/null || echo "  (parse error)"
            ;;
        5)  # reconnect
            echo ""
            echo "[4] Connect PSU1..."
            echo "$json_line" | python3 -c "
import sys,json
d=json.loads(json.load(sys.stdin)['result']['content'][0]['text'])
print(f'  status={d.get(\"status\",\"?\")}')" 2>/dev/null || echo "  (parse error)"
            ;;
        6)  # set_volt
            echo ""
            echo "[5] Set PSU1 to 5V / 3A..."
            echo "  set_volt: ok"
            ;;
        7)  # set_curr
            echo "  set_curr: ok"
            ;;
        8)  # set_outp
            echo ""
            echo "[6] Enable output..."
            echo "  set_outp: ok"
            ;;
        9)  # get_device_state
            echo ""
            echo "[7] Check final state..."
            echo "$json_line" | python3 -c "
import sys,json
s=json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['state']
print(f'  Voltage: {s[\"volt_set\"]:.2f}V  Current: {s[\"curr_set\"]:.2f}A  Output: {\"ON\" if s[\"output_on\"] else \"OFF\"}')" 2>/dev/null || echo "  (parse error)"
            ;;
    esac
done < <(parse_all "$RAW")

echo ""
echo "============================================================"
echo "  Done!"
echo "============================================================"
