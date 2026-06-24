// Package mcp provides a minimal stdio Model Context Protocol server that
// exposes PowerWing controls as MCP tools for AI clients.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuguorong/power_wing/internal/manager"
	"github.com/yuguorong/power_wing/internal/serialport"
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
				"version": "1.0.0",
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
		// Collect ports already configured in the manager to skip them
		skipPorts := make(map[string]bool)
		for _, d := range s.mgr.Devices() {
			if d.Port != "" {
				skipPorts[d.Port] = true
			}
		}
		results, err := probeDevice(devType, skipPorts)
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

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
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
			"description": "Send a device command (for example set_volt, set_curr, set_outp, set_usb_port).",
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
			"description": "Probe every serial port to find which one a device type responds on. Use this to discover where a device is physically connected.",
			"inputSchema": schemaObject(
				map[string]interface{}{
					"device_type": schemaString("Device type: spm3051, pd_pocket, or usbslim."),
				},
				[]string{"device_type"},
			),
		},
		{
			"name":        "powerwing_get_config",
			"description": "Return the full current configuration including all devices, server port, and feature flags.",
			"inputSchema": schemaObject(nil, nil),
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
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\n\n", len(data)); err != nil {
		return err
	}
	_, err = s.out.Write(data)
	return err
}

// probeDevice opens each serial port and tries a device-type-specific probe
// command to see whether a compatible device is attached.  It returns the list
// of ports where the device responded.  Ports in skipPorts are skipped (e.g.
// they are already opened by the manager).
func probeDevice(devType string, skipPorts map[string]bool) ([]map[string]interface{}, error) {
	ports, err := serialport.List()
	if err != nil {
		return nil, fmt.Errorf("list serial ports: %w", err)
	}
	if len(ports) == 0 {
		return []map[string]interface{}{}, nil
	}

	baud, probeCmd, respWait := probeParams(devType)
	if probeCmd == nil {
		return nil, fmt.Errorf("unsupported device type: %s (valid: spm3051, pd_pocket, usbslim)", devType)
	}

	var results []map[string]interface{}
	for _, port := range ports {
		if skipPorts[port] {
			results = append(results, map[string]interface{}{
				"port":    port,
				"matched": false,
				"skipped": true,
				"reason":  "port already in use by a configured device",
			})
			continue
		}
		found := probePort(port, baud, probeCmd, respWait)
		entry := map[string]interface{}{
			"port":    port,
			"matched": found,
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
	default:
		return 0, nil, 0
	}
}

// probePort tries opening a serial port, sending a probe command, and reading
// a response.  Returns true if any non-empty response was received.
// The entire operation is bounded by a 3-second timeout to avoid hanging on
// non-responsive virtual COM ports.
func probePort(port string, baud int, cmd []byte, wait time.Duration) bool {
	type result struct {
		found bool
	}
	ch := make(chan result, 1)
	go func() {
		mode := serialport.Mode(baud)
		p, err := serialport.Open(port, mode)
		if err != nil {
			ch <- result{false}
			return
		}
		defer p.Close()

		// Discard any stale data in the buffer.
		p.SetReadTimeout(50 * time.Millisecond)
		dummy := make([]byte, 256)
		for {
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
		return false
	}
}
