// Package mcp provides a minimal Model Context Protocol server that exposes
// PowerWing controls as MCP tools for AI clients.  Transports: stdio, raw TCP,
// and Streamable HTTP (see NewHTTP).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/manager"
	"github.com/yuguorong/power_wing/internal/serialport"
	"github.com/yuguorong/power_wing/internal/version"
)

const (
	rpcVersion      = "2.0"
	protocolVersion = "2024-11-05"
)

type Server struct {
	mgr *manager.Manager

	in  *bufio.Reader
	out io.Writer

	writeMu sync.Mutex
}

func New(mgr *manager.Manager, in io.Reader, out io.Writer) *Server {
	return &Server{
		mgr: mgr,
		in:  bufio.NewReader(in),
		out: out,
	}
}

// ServeTCP accepts MCP JSON-RPC clients over TCP. Use host "0.0.0.0" to allow
// connections from other machines on the LAN.
func ServeTCP(ctx context.Context, mgr *manager.Manager, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	log.Printf("[mcp] TCP server listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}

		go func(c net.Conn) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[mcp] panic in TCP handler for %s: %v", c.RemoteAddr().String(), r)
				}
			}()
			defer c.Close()
			remote := c.RemoteAddr().String()
			log.Printf("[mcp] TCP client connected: %s", remote)
			if err := New(mgr, c, c).Serve(ctx); err != nil {
				log.Printf("[mcp] TCP client %s error: %v", remote, err)
			}
			log.Printf("[mcp] TCP client disconnected: %s", remote)
		}(conn)
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ─── Streamable HTTP transport ────────────────────────────────────────────────

// NewHTTP returns an http.Handler exposing the MCP server via the Streamable
// HTTP transport: each POST carries one JSON-RPC message and gets a plain
// JSON response.  The handler is stateless (no session, no SSE stream), which
// is spec-compliant — clients fall back to per-request POST round-trips.
func NewHTTP(mgr *manager.Manager) http.Handler {
	return &httpHandler{srv: &Server{mgr: mgr}}
}

type httpHandler struct {
	srv *Server
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Allow browser-based MCP clients on other origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "MCP endpoint: POST a JSON-RPC message (no SSE stream)", http.StatusMethodNotAllowed)
	}
}

func (h *httpHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPC(w, rpcResponse{
			JSONRPC: rpcVersion,
			Error:   &rpcError{Code: -32700, Message: "parse error", Data: err.Error()},
		})
		return
	}
	res := h.srv.handleRequest(req)
	if req.ID == nil {
		// Notification (e.g. "notifications/initialized") — no response body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSONRPC(w, res)
}

func writeJSONRPC(w http.ResponseWriter, res rpcResponse) {
	data, err := json.Marshal(res)
	if err != nil {
		http.Error(w, "marshal response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (s *Server) Serve(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in MCP serve: %v", r)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		payload, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = s.writeResponse(rpcResponse{
				JSONRPC: rpcVersion,
				Error: &rpcError{
					Code:    -32700,
					Message: "parse error",
					Data:    err.Error(),
				},
			})
			continue
		}

		res := s.handleRequest(req)
		if req.ID != nil {
			if err := s.writeResponse(res); err != nil {
				return err
			}
		}
	}
}

func (s *Server) handleRequest(req rpcRequest) rpcResponse {
	res := rpcResponse{JSONRPC: rpcVersion, ID: req.ID}

	switch req.Method {
	case "initialize":
		res.Result = map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]string{
				"name":    "power-wing",
				"version": version.Version,
			},
		}

	case "notifications/initialized":
		// Notification; no response.

	case "ping":
		res.Result = map[string]interface{}{}

	case "tools/list":
		res.Result = map[string]interface{}{"tools": toolDefs()}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			res.Error = &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
			return res
		}
		if params.Arguments == nil {
			params.Arguments = map[string]interface{}{}
		}

		result, toolErr := s.callTool(params.Name, params.Arguments)
		if toolErr != nil {
			res.Result = map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": toolErr.Error()}},
				"isError": true,
			}
			return res
		}

		b, _ := json.MarshalIndent(result, "", "  ")
		res.Result = map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": string(b)}},
		}

	default:
		res.Error = &rpcError{Code: -32601, Message: "method not found"}
	}

	return res
}

func (s *Server) callTool(name string, args map[string]interface{}) (interface{}, error) {
	switch name {
	case "powerwing_list_devices":
		devs := s.mgr.Devices()
		out := make([]map[string]interface{}, 0, len(devs))
		for _, d := range devs {
			entry := map[string]interface{}{
				"id":        d.ID,
				"name":      d.Name,
				"type":      d.Type,
				"connected": d.Connected,
				"port":      d.Port,
				"baud":      d.Baud,
			}
			if st, ok := s.mgr.LatestState(d.ID); ok {
				entry["state"] = st
			}
			out = append(out, entry)
		}
		return map[string]interface{}{"devices": out}, nil

	case "powerwing_get_device_state":
		id, err := stringArg(args, "device_id")
		if err != nil {
			return nil, err
		}
		st, ok := s.mgr.LatestState(id)
		if !ok {
			return nil, fmt.Errorf("device state not found: %s", id)
		}
		return map[string]interface{}{"device_id": id, "state": st}, nil

	case "powerwing_send_command":
		id, err := stringArg(args, "device_id")
		if err != nil {
			return nil, err
		}
		cmd, err := stringArg(args, "command")
		if err != nil {
			return nil, err
		}
		params := map[string]interface{}{}
		if raw, ok := args["params"]; ok {
			obj, ok := raw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("params must be an object")
			}
			params = obj
		}
		if err := s.mgr.SendCmd(manager.CmdRequest{DeviceID: id, Command: cmd, Params: params}); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "device_id": id, "command": cmd}, nil

	case "powerwing_list_serial_ports":
		ports, err := serialport.List()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"ports": ports}, nil

	case "powerwing_disconnect_device":
		id, err := stringArg(args, "device_id")
		if err != nil {
			return nil, err
		}
		if err := s.mgr.DisconnectDevice(id); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "device_id": id}, nil

	case "powerwing_reconnect_device":
		id, err := stringArg(args, "device_id")
		if err != nil {
			return nil, err
		}
		port, _ := optionalStringArg(args, "port")
		baud, err := optionalIntArg(args, "baud")
		if err != nil {
			return nil, err
		}
		if err := s.mgr.ReconnectDevice(id, port, baud); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "device_id": id, "port": port, "baud": baud}, nil

	case "powerwing_rename_device":
		id, err := stringArg(args, "device_id")
		if err != nil {
			return nil, err
		}
		nameArg, err := stringArg(args, "name")
		if err != nil {
			return nil, err
		}
		if err := s.mgr.RenameDevice(id, nameArg); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "device_id": id, "name": nameArg}, nil

	case "powerwing_test_connection":
		devType, err := stringArg(args, "device_type")
		if err != nil {
			return nil, err
		}
		results, err := probeConfiguredDevices(s.mgr, devType)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"device_type": devType,
			"results":     results,
		}, nil

	case "powerwing_get_config":
		cfg := s.mgr.Config()
		devs := make([]map[string]interface{}, 0, len(cfg.Devices))
		for _, d := range cfg.Devices {
			devs = append(devs, map[string]interface{}{
				"id":      d.ID,
				"name":    d.Name,
				"type":    d.Type,
				"port":    d.Port,
				"baud":    d.Baud,
				"enabled": d.Enabled,
			})
		}
		return map[string]interface{}{
			"devices":       devs,
			"server_port":   cfg.ServerPort,
			"active_device": cfg.ActiveDevice,
			"voice_enabled": cfg.VoiceEnabled,
		}, nil

	case "powerwing_set_config":
		return s.callSetConfig(args)

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// callSetConfig implements powerwing_set_config, the write counterpart of
// powerwing_get_config.  Two mutually exclusive forms are accepted:
//
//   - devices: replace the whole configured device list.  Entries carry
//     id/name/type/port/baud/enabled exactly as returned by get_config.
//   - device_id (plus optional name/port/baud/enabled): update a single
//     existing device.  Omitted fields keep their current values; port/baud
//     changes rebuild and reconnect the device.
func (s *Server) callSetConfig(args map[string]interface{}) (interface{}, error) {
	rawDevs, hasDevs := args["devices"]
	rawID, hasID := args["device_id"]
	if hasDevs == hasID {
		return nil, fmt.Errorf("provide either \"devices\" (full list) or \"device_id\" (single device), not both or neither")
	}

	if hasDevs {
		list, ok := rawDevs.([]interface{})
		if !ok {
			return nil, fmt.Errorf("devices must be an array of device objects")
		}
		devices := make([]config.DeviceConfig, 0, len(list))
		for i, item := range list {
			obj, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("devices[%d] must be an object", i)
			}
			dc, err := deviceConfigFromArgs(obj)
			if err != nil {
				return nil, fmt.Errorf("devices[%d]: %w", i, err)
			}
			devices = append(devices, dc)
		}
		if err := s.mgr.SetConfig(devices); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "updated": "all", "device_count": len(devices)}, nil
	}

	id, ok := rawID.(string)
	if !ok {
		return nil, fmt.Errorf("device_id must be a string")
	}
	name, _ := optionalStringArg(args, "name")
	port, _ := optionalStringArg(args, "port")
	baud, err := optionalIntArg(args, "baud")
	if err != nil {
		return nil, err
	}
	// enabled is tri-state: absent = keep current.
	enabled, hasEnabled := parseOptionalBool(args["enabled"])
	if hasEnabled {
		// Verify the device exists and apply the enable flag before the
		// connection update so the manager works on a known entry.
		if err := s.mgr.SetDeviceEnabled(id, enabled); err != nil {
			return nil, err
		}
	}
	if err := s.mgr.SetDeviceConfig(id, name, port, baud); err != nil {
		return nil, err
	}
	return map[string]interface{}{"status": "ok", "updated": id, "port": port, "baud": baud}, nil
}

// deviceConfigFromArgs converts a tool-argument object into a DeviceConfig.
func deviceConfigFromArgs(obj map[string]interface{}) (config.DeviceConfig, error) {
	var dc config.DeviceConfig
	id, err := stringArg(obj, "id")
	if err != nil {
		return dc, err
	}
	dc.ID = id
	if v, ok := optionalStringArg(obj, "name"); ok {
		dc.Name = v
	}
	devType, err := stringArg(obj, "type")
	if err != nil {
		return dc, err
	}
	dc.Type = devType
	if v, ok := optionalStringArg(obj, "port"); ok {
		dc.Port = v
	}
	if v, err := optionalIntArg(obj, "baud"); err != nil {
		return dc, err
	} else if v > 0 {
		dc.Baud = v
	}
	if b, ok := parseOptionalBool(obj["enabled"]); ok {
		dc.Enabled = b
	}
	return dc, nil
}

// parseOptionalBool reads a JSON boolean that may be absent.
func parseOptionalBool(v interface{}) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func toolDefs() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "powerwing_list_devices",
			"description": "List configured PowerWing devices with connection status and latest state.",
			"inputSchema": schemaObject(nil, nil),
		},
		{
			"name":        "powerwing_get_device_state",
			"description": "Get latest polled state for a specific device.",
			"inputSchema": schemaObject(
				map[string]interface{}{"device_id": schemaString("Device ID.")},
				[]string{"device_id"},
			),
		},
		{
			"name":        "powerwing_send_command",
			"description": "Send a device command (for example set_volt, set_curr, set_outp, release_control, set_usb_port).",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"device_id": schemaString("Device ID."),
					"command":   schemaString("Manager command name."),
					"params": map[string]interface{}{
						"type":        "object",
						"description": "Command parameters object.",
					},
				},
				[]string{"device_id", "command"},
			),
		},
		{
			"name":        "powerwing_list_serial_ports",
			"description": "List host serial ports that can be used for device reconnect.",
			"inputSchema": schemaObject(nil, nil),
		},
		{
			"name":        "powerwing_disconnect_device",
			"description": "Disconnect a device without removing it from configuration.",
			"inputSchema": schemaObject(
				map[string]interface{}{"device_id": schemaString("Device ID.")},
				[]string{"device_id"},
			),
		},
		{
			"name":        "powerwing_reconnect_device",
			"description": "Reconnect a device, optionally overriding serial port and baud rate.",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"device_id": schemaString("Device ID."),
					"port":      schemaString("Optional serial port, for example COM4."),
					"baud": map[string]interface{}{
						"type":        "integer",
						"description": "Optional baud rate.",
					},
				},
				[]string{"device_id"},
			),
		},
		{
			"name":        "powerwing_rename_device",
			"description": "Rename a configured device.",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"device_id": schemaString("Device ID."),
					"name":      schemaString("New device display name."),
				},
				[]string{"device_id", "name"},
			),
		},
		{
			"name":        "powerwing_test_connection",
			"description": "Check whether configured devices of the given type are connected on their configured serial ports. Only configured ports are opened; host ports are enumerated for presence reporting only.",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"device_type": schemaString("Device type: spm3051, pd_pocket, usbslim, sangenxian, usbalarm, f11, or powerz."),
				},
				[]string{"device_type"},
			),
		},
		{
			"name":        "powerwing_get_config",
			"description": "Return the full current configuration including all devices, server port, and feature flags.",
			"inputSchema": schemaObject(nil, nil),
		},
		{
			"name":        "powerwing_set_config",
			"description": "Set configuration, the counterpart of powerwing_get_config. Two forms: (1) pass \"devices\" as the full device list (as returned by get_config) to replace all configuration; (2) pass \"device_id\" plus optional \"name\"/\"port\"/\"baud\"/\"enabled\" to update a single device, with omitted fields keeping current values. Combine with powerwing_list_serial_ports to assign ports: enumerate host ports, then configure which device uses which port.",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"devices": map[string]interface{}{
						"type":        "array",
						"description": "Full device list to replace the configuration (mutually exclusive with device_id).",
						"items": schemaObject(
							map[string]interface{}{
								"id":      schemaString("Device ID, e.g. PSU1."),
								"name":    schemaString("Display name."),
								"type":    schemaString("Device type: spm3051, pd_pocket, usbslim, sangenxian, usbalarm, f11, or powerz."),
								"port":    schemaString("Serial port from powerwing_list_serial_ports, e.g. COM3 or /dev/ttyUSB0."),
								"baud":    map[string]interface{}{"type": "integer", "description": "Baud rate; 0 or omitted = driver default."},
								"enabled": map[string]interface{}{"type": "boolean", "description": "Whether the device is enabled."},
							},
							[]string{"id", "type"},
						),
					},
					"device_id": schemaString("Update a single existing device by ID (mutually exclusive with devices)."),
					"name":      schemaString("New display name (single-device form; omit to keep)."),
					"port":      schemaString("New serial port (single-device form; omit to keep)."),
					"baud":      map[string]interface{}{"type": "integer", "description": "New baud rate (single-device form; omit or 0 to keep)."},
					"enabled":   map[string]interface{}{"type": "boolean", "description": "Enable/disable flag (single-device form; omit to keep)."},
				},
				nil,
			),
		},
	}
}

func schemaObject(props map[string]interface{}, required []string) map[string]interface{} {
	out := map[string]interface{}{"type": "object"}
	if props != nil {
		out["properties"] = props
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func schemaString(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func stringArg(args map[string]interface{}, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("argument %s cannot be empty", key)
	}
	return s, nil
}

func optionalStringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func optionalIntArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("argument %s must be an integer", key)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("argument %s must be an integer", key)
	}
}

func (s *Server) readMessage() ([]byte, error) {
	line, err := s.in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return s.readMessage()
	}

	if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid content-length header")
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid content-length value")
		}

		for {
			h, err := s.in.ReadString('\n')
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(h) == "" {
				break
			}
		}

		body := make([]byte, n)
		if _, err := io.ReadFull(s.in, body); err != nil {
			return nil, err
		}
		return body, nil
	}

	return []byte(trimmed), nil
}

func (s *Server) writeResponse(res rpcResponse) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Use \n (Unix LF) only — WSL /init bridge can split \r\n into separate
	// chunks, causing asyncio readline clients to parse Content-Length\r as
	// a broken header.  Pure \n avoids this pipe fragmentation issue.
	header := fmt.Sprintf("Content-Length: %d\n\n", len(data))
	if _, err := s.out.Write([]byte(header)); err != nil {
		return err
	}
	_, err = s.out.Write(data)
	return err
}

// probeConfiguredDevices tests whether configured devices of the given type
// are connected on their configured serial ports.  Host-wide enumeration is
// used only to report whether each configured port exists — non-configured
// ports are NEVER opened.  Ports of configured devices that are currently
// connected (held by the manager) are reported from live state instead of
// being probed, so the open handle is not disturbed.
func probeConfiguredDevices(mgr *manager.Manager, devType string) ([]map[string]interface{}, error) {
	baud, probeCmd, respWait := probeParams(devType)
	if probeCmd == nil {
		return nil, fmt.Errorf("unsupported device type: %s (valid: spm3051, pd_pocket, usbslim, sangenxian, usbalarm, f11, powerz)", devType)
	}

	// Enumerate host ports (list only — nothing is opened here).
	hostPorts, err := serialport.List()
	if err != nil {
		return nil, fmt.Errorf("list serial ports: %w", err)
	}
	exists := make(map[string]bool, len(hostPorts))
	for _, p := range hostPorts {
		exists[p] = true
	}

	// Ports held live by the manager — probing them would disturb the open
	// handle, so they are reported from live connection state instead.
	live := make(map[string]bool)
	for _, d := range mgr.Devices() {
		if d.Connected {
			live[d.Port] = true
		}
	}

	var results []map[string]interface{}
	for _, d := range mgr.Devices() {
		if !strings.EqualFold(d.Type, devType) {
			continue
		}
		entry := map[string]interface{}{
			"device_id": d.ID,
			"name":      d.Name,
			"port":      d.Port,
			"baud":      d.Baud,
		}
		switch {
		case d.Port == "":
			entry["matched"] = false
			entry["reason"] = "device has no port configured"
		case !exists[d.Port]:
			entry["matched"] = false
			entry["reason"] = "configured port is not present on the host"
		case d.Connected:
			// Manager already holds the port and confirmed the device.
			entry["matched"] = true
			entry["reason"] = "device is connected via manager"
		case live[d.Port]:
			// Port is open by another configured device of a different type.
			entry["matched"] = false
			entry["reason"] = "port is in use by another connected device"
		default:
			entry["matched"] = probePort("probe:"+devType, d.Port, baud, probeCmd, respWait)
		}
		results = append(results, entry)
	}
	return results, nil
}

// probeParams returns the baud rate, probe command bytes, and read timeout
// for a given device type string.
func probeParams(devType string) (baud int, cmd []byte, wait time.Duration) {
	switch strings.ToLower(devType) {
	case "spm3051":
		return 9600, []byte("*IDN?\n"), 300 * time.Millisecond
	case "pd_pocket":
		return 115200, []byte("SYST:VERS?\r\n"), 200 * time.Millisecond
	case "usbslim":
		// Query-all-ports frame: 55 5A 00 0F 00 0F
		return 9600, []byte{0x55, 0x5A, 0x00, 0x0F, 0x00, 0x0F}, 300 * time.Millisecond
	case "sangenxian":
		// Device-ID query: 7E 85 E6 (4800-8-N-1, CRC8 poly 0x07).
		return 4800, []byte{0x7E, 0x85, 0xE6}, 300 * time.Millisecond
	case "usbalarm":
		// Send buzzer-off command as probe: 01 06 00 C2 00 41 + CRC
		return 4800, []byte{0x01, 0x06, 0x00, 0xC2, 0x00, 0x41, 0xE8, 0x19}, 300 * time.Millisecond
	case "f11":
		// F-11 (AFU IoT) meter: streams "current,voltage" lines and answers
		// ASCII sys commands at 38400-8-N-1.  Probe with sys version.
		return 38400, []byte("sys version\r\n"), 400 * time.Millisecond
	case "powerz":
		// POWER-Z (KM002C/KM003C) meter over its CDC port: Connect followed by
		// GetData(ADC) = 02 00 00 00 0C 00 02 00.  The device answers with a
		// PutData frame (0x41).  Baud rate is ignored by the device.
		return 115200, []byte{0x02, 0x00, 0x00, 0x00, 0x0C, 0x00, 0x02, 0x00}, 400 * time.Millisecond
	default:
		return 0, nil, 0
	}
}

// probePort opens one serial port — always a port from the configuration —
// sends the probe command, and reads a response.  Returns true if any
// non-empty response was received.  The entire operation is bounded by a
// 3-second timeout; if the probe is stuck by then (e.g. a blocking driver
// Open or a hung read), the port is force closed so the OS handle is never
// leaked.
func probePort(owner, port string, baud int, cmd []byte, wait time.Duration) bool {
	type result struct {
		found bool
	}
	ch := make(chan result, 1)

	// mu guards the port handle so the timeout path can force a Close even
	// while the probe goroutine is blocked inside Open/Read.
	var mu sync.Mutex
	var p serialport.Port
	forceClose := func() {
		mu.Lock()
		defer mu.Unlock()
		if p != nil {
			_ = p.Close()
			p = nil
		}
	}

	go func() {
		mode := serialport.Mode(baud)
		sp, err := serialport.OpenFor(owner, port, mode)
		mu.Lock()
		if err != nil {
			mu.Unlock()
			ch <- result{false}
			return
		}
		p = sp
		mu.Unlock()
		defer forceClose()

		// Discard any stale data in the buffer.  Bounded: a continuously
		// streaming port (e.g. a diag port spewing logs) would otherwise
		// never hit the n==0 exit and the port would never be closed.
		p.SetReadTimeout(50 * time.Millisecond)
		dummy := make([]byte, 256)
		for i := 0; i < 8; i++ {
			n, _ := p.Read(dummy)
			if n == 0 {
				break
			}
		}

		_, err = p.Write(cmd)
		if err != nil {
			ch <- result{false}
			return
		}

		p.SetReadTimeout(wait)
		buf := make([]byte, 128)
		n, err := p.Read(buf)
		if err != nil || n == 0 {
			ch <- result{false}
			return
		}

		resp := strings.TrimSpace(string(buf[:n]))
		ch <- result{resp != ""}
	}()

	select {
	case r := <-ch:
		return r.found
	case <-time.After(3 * time.Second):
		// Probe is stuck — force release the handle instead of abandoning it.
		forceClose()
		return false
	}
}
