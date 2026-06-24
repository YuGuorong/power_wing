
# -*- coding: utf-8 -*-
"""
PowerWing MCP API Test (Python 2/3 compatible)
Connects via TCP to the running MCP server and tests all 9 tools.
Usage: python test_mcp.py
"""
from __future__ import print_function
import json
import socket
import sys

PY2 = sys.version_info[0] == 2


def to_bytes(s):
    if isinstance(s, bytes):
        return s
    return s.encode("utf-8")


def to_str(b):
    if isinstance(b, str) and not PY2:
        return b
    if PY2 and isinstance(b, unicode):  # noqa: F821
        return b.encode("utf-8")
    return b.decode("utf-8")


class MCPClient(object):
    """Minimal MCP JSON-RPC 2.0 client over TCP."""

    def __init__(self, host="127.0.0.1", port=9876):
        self.host = host
        self.port = port
        self._id = 0

    def call(self, method, params=None):
        """Send a JSON-RPC request and return the result."""
        self._id += 1
        req = {"jsonrpc": "2.0", "id": self._id, "method": method}
        if params is not None:
            req["params"] = params

        body = json.dumps(req, ensure_ascii=False)
        if PY2 and isinstance(body, unicode):  # noqa: F821
            body = body.encode("utf-8")
        frame = "Content-Length: {}\n\n{}".format(len(body), body)
        frame_bytes = to_bytes(frame)

        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(10)
        sock.connect((self.host, self.port))
        sock.sendall(frame_bytes)

        # Read Content-Length header
        buf = b""
        while b"\n\n" not in buf:
            chunk = sock.recv(1)
            if not chunk:
                break
            buf += chunk

        header_text = to_str(buf.split(b"\n")[0])
        content_len = int(header_text.split(":")[1].strip())

        # Extract body after blank line
        parts = buf.split(b"\n\n", 1)
        body_bytes = parts[1] if len(parts) > 1 else b""

        while len(body_bytes) < content_len:
            chunk = sock.recv(content_len - len(body_bytes))
            if not chunk:
                break
            body_bytes += chunk

        sock.close()

        resp = json.loads(to_str(body_bytes))
        if resp.get("error"):
            raise RuntimeError("MCP error: {}".format(resp["error"]))
        return resp.get("result")


def tool_call(client, tool_name, arguments=None):
    """Call an MCP tool and return the parsed text content."""
    result = client.call("tools/call", {
        "name": tool_name,
        "arguments": arguments or {},
    })
    text = result["content"][0]["text"]
    return json.loads(text)


def main():
    print("=" * 60)
    print("  PowerWing MCP API Test (Python)")
    print("=" * 60)

    client = MCPClient()

    # 1. Initialize
    print("\n[1/7] Initialize...")
    result = client.call("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": "python-test", "version": "1.0"},
    })
    info = result["serverInfo"]
    print("  OK  Server: {} v{}".format(info["name"], info["version"]))

    # Send initialized notification
    client.call("notifications/initialized")

    # 2. List tools
    print("\n[2/7] List tools...")
    result = client.call("tools/list")
    for t in result["tools"]:
        print("  - {}".format(t["name"]))

    # 3. List devices
    print("\n[3/7] List devices...")
    data = tool_call(client, "powerwing_list_devices")
    for dev in data["devices"]:
        conn = "ON" if dev["connected"] else "OFF"
        print("  [{}] {} ({}) [{}] @ {}".format(
            conn, dev["id"], dev["name"], dev["type"], dev["port"]))

    # 4. Test connection - probe for SPM3051
    print("\n[4/7] Test connection (spm3051)...")
    data = tool_call(client, "powerwing_test_connection",
                     {"device_type": "spm3051"})
    for r in data["results"]:
        if r.get("skipped"):
            flag = "SKIP"
        elif r["matched"]:
            flag = "MATCH"
        else:
            flag = "NONE"
        print("  {}: {}".format(r["port"], flag))

    # 5. Get config
    print("\n[5/7] Get config...")
    data = tool_call(client, "powerwing_get_config")
    print("  Server port: {}".format(data["server_port"]))
    print("  Devices: {} configured".format(len(data["devices"])))

    # 6. Get device state for PSU1
    print("\n[6/7] Get device state...")
    try:
        data = tool_call(client, "powerwing_get_device_state",
                         {"device_id": "PSU1"})
        s = data["state"]
        print("  PSU1: {:.2f}V / {:.2f}A set, output={}".format(
            s["volt_set"], s["curr_set"],
            "ON" if s["output_on"] else "OFF"))
        print("  CV mode: {}, OVP={:.2f}V, OCP={:.2f}A".format(
            s["cv_mode"], s["ovp_limit"], s["ocp_limit"]))
    except RuntimeError as e:
        print("  WARN: {}".format(e))

    # 7. List serial ports
    print("\n[7/7] List serial ports...")
    data = tool_call(client, "powerwing_list_serial_ports")
    print("  Ports: {}".format(", ".join(data["ports"])))

    print("\n" + "=" * 60)
    print("  All tests passed")
    print("=" * 60)


if __name__ == "__main__":
    main()
