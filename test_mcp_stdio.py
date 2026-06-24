
# -*- coding: utf-8 -*-
"""
PowerWing MCP stdio test - spawns power_wing.exe and tests via stdin/stdout.
Usage: python test_mcp_stdio.py
"""
from __future__ import print_function
import json
import os
import platform
import subprocess
import sys

PY2 = sys.version_info[0] == 2


def get_mcp_command():
    """Detect the platform and return the correct MCP server command.

    Returns:
        list: Command list, e.g. ["power_wing.exe", "--mcp-only", ...]
    """
    # Check environment variable first (allows manual override)
    env_cmd = os.environ.get("POWER_WING_EXE", "")
    if env_cmd:
        cmd = [env_cmd, "--mcp-only", "--mcp-transport", "stdio"]
        print("  Using POWER_WING_EXE from env: {}".format(env_cmd))
        return cmd

    system = platform.system().lower()

    # Check if running inside WSL
    uname = platform.uname()
    if PY2:
        # Python 2: uname is a tuple (system, node, release, version, machine, processor)
        release = uname[2].lower()
    else:
        release = uname.release.lower()
    is_wsl = "microsoft" in release or "wsl" in release

    if is_wsl:
        # WSL: use the Windows exe via /mnt/... path
        exe = os.environ.get("POWER_WING_WIN_EXE", "/mnt/c/work/bin/power_wing.exe")
        print("  Detected WSL, using: {}".format(exe))
        return [exe, "--mcp-only", "--mcp-transport", "stdio"]

    if system == "windows":
        # Native Windows
        exe = os.environ.get("POWER_WING_WIN_EXE", "c:\\work\\bin\\power_wing.exe")
        print("  Detected Windows, using: {}".format(exe))
        return [exe, "--mcp-only", "--mcp-transport", "stdio"]

    # Linux / macOS (native)
    exe = os.environ.get("POWER_WING_LINUX_EXE", "./power_wing_linux_amd64")
    print("  Detected {}, using: {}".format(platform.system(), exe))
    return [exe, "--mcp-only", "--mcp-transport", "stdio"]


def send_frame(proc, body):
    """Send a JSON-RPC request frame to the process via stdin."""
    frame = "Content-Length: {}\n\n{}".format(len(body), body)
    if PY2:
        if isinstance(frame, unicode):  # noqa: F821
            frame = frame.encode("utf-8")
        proc.stdin.write(frame)
    else:
        proc.stdin.write(frame.encode("utf-8"))
    proc.stdin.flush()


def read_response(proc):
    """Read a single Content-Length + body frame from stdout. Blocking."""
    header = b""
    while b"\n\n" not in header:
        b = proc.stdout.read(1)
        if not b:
            return None
        header += b

    if PY2:
        header_line = header.split(b"\n")[0]
        content_len = int(header_line.split(b":")[1].strip())
    else:
        header_line = header.split(b"\n")[0].decode()
        content_len = int(header_line.split(":")[1].strip())

    parts = header.split(b"\n\n", 1)
    body_bytes = parts[1] if len(parts) > 1 else b""
    while len(body_bytes) < content_len:
        chunk = proc.stdout.read(content_len - len(body_bytes))
        if not chunk:
            break
        body_bytes += chunk

    if PY2:
        return json.loads(body_bytes)
    return json.loads(body_bytes.decode("utf-8"))


def main():
    print("=" * 60)
    print("  PowerWing MCP stdio Test")
    print("=" * 60)

    cmd = get_mcp_command()
    # WSL: stderr logs can mix into stdout pipe; send stderr to DEVNULL
    try:
        from subprocess import DEVNULL
    except ImportError:
        DEVNULL = open(os.devnull, "w")
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=DEVNULL)

    # 1. Initialize
    print("\n[1] Initialize...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                   "clientInfo": {"name": "test", "version": "1.0"}},
    }))
    resp = read_response(proc)
    info = resp["result"]["serverInfo"]
    print("  OK  {} v{}".format(info["name"], info["version"]))

    # 2. Initialized notification
    send_frame(proc, json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}))

    # 3. List tools (MCP built-in)
    print("\n[2] List tools...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 2, "method": "tools/list",
    }))
    resp = read_response(proc)
    for t in resp["result"]["tools"]:
        desc = t["description"]
        if len(desc) > 70:
            desc = desc[:67] + "..."
        print("  - {} : {}".format(t["name"], desc))

    # 4. List devices
    print("\n[3] List devices...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": {"name": "powerwing_list_devices", "arguments": {}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    for dev in data["devices"]:
        conn = "ON" if dev["connected"] else "OFF"
        print("  [{}] {} ({}) @ {}".format(conn, dev["id"], dev["name"], dev["port"]))

    # 5. Connect PSU1
    print("\n[4] Connect PSU1...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 4, "method": "tools/call",
        "params": {"name": "powerwing_reconnect_device",
                   "arguments": {"device_id": "PSU1"}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    print("  {}".format(json.dumps(data)))

    # 6. Set 5V / 3A
    print("\n[5] Set PSU1 to 5V / 3A...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 5, "method": "tools/call",
        "params": {"name": "powerwing_send_command",
                   "arguments": {"device_id": "PSU1", "command": "set_volt",
                                 "params": {"value": 5.0}}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    print("  set_volt: {}".format(data))

    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 6, "method": "tools/call",
        "params": {"name": "powerwing_send_command",
                   "arguments": {"device_id": "PSU1", "command": "set_curr",
                                 "params": {"value": 3.0}}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    print("  set_curr: {}".format(data))

    # 7. Enable output
    print("\n[6] Enable output...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 7, "method": "tools/call",
        "params": {"name": "powerwing_send_command",
                   "arguments": {"device_id": "PSU1", "command": "set_outp",
                                 "params": {"on": True}}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    print("  set_outp: {}".format(data))

    # 8. Check final state
    print("\n[7] Check final state...")
    send_frame(proc, json.dumps({
        "jsonrpc": "2.0", "id": 8, "method": "tools/call",
        "params": {"name": "powerwing_get_device_state",
                   "arguments": {"device_id": "PSU1"}},
    }))
    resp = read_response(proc)
    data = json.loads(resp["result"]["content"][0]["text"])
    s = data["state"]
    print("  Voltage: {:.2f}V  Current: {:.2f}A  Output: {}".format(
        s["volt_set"], s["curr_set"], "ON" if s["output_on"] else "OFF"))

    proc.stdin.close()
    proc.wait()

    print("\n" + "=" * 60)
    print("  Done!")
    print("=" * 60)


if __name__ == "__main__":
    main()
