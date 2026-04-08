// Package server provides the HTTP and WebSocket server for the PowerWing UI.
//
// Endpoints:
//
//	GET  /                          – web UI (embedded static files)
//	GET  /ws                        – WebSocket: real-time state stream
//	GET  /api/devices               – list all device infos + latest state
//	GET  /api/device/{id}/state     – latest state for one device
//	POST /api/device/{id}/cmd       – execute a command (JSON body)
//	GET  /api/serial-ports          – list available serial ports
//	POST /api/config/device         – add/update a device config
//	DELETE /api/config/device/{id}  – remove a device config
//	GET  /api/config                – dump full config JSON
//	POST /api/config                – replace full config JSON
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"

	"github.com/yuguorong/power_wing/internal/config"
	"github.com/yuguorong/power_wing/internal/manager"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true }, // localhost only
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

// Server wraps the HTTP mux and owns WebSocket client connections.
type Server struct {
	mgr     *manager.Manager
	port    int
	webFS   fs.FS
	mux     *http.ServeMux
	httpSrv *http.Server

	clientsMu sync.Mutex
	clients   map[*wsClient]struct{}
}

// New creates a Server.  webFS is the embedded filesystem rooted at the web/
// directory so that index.html is directly accessible.
func New(mgr *manager.Manager, port int, webFS fs.FS) *Server {
	s := &Server{
		mgr:     mgr,
		port:    port,
		webFS:   webFS,
		clients: make(map[*wsClient]struct{}),
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) routes() {
	// static files
	s.mux.Handle("/", http.FileServer(http.FS(s.webFS)))

	// WebSocket
	s.mux.HandleFunc("/ws", s.handleWS)

	// REST API
	s.mux.HandleFunc("/api/devices", s.handleDevices)
	s.mux.HandleFunc("/api/device/", s.handleDevice)
	s.mux.HandleFunc("/api/serial-ports", s.handleSerialPorts)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/config/device", s.handleConfigDevice)
	s.mux.HandleFunc("/api/config/device/", s.handleConfigDevice)
}

// Start begins listening.  Blocks until the server is shut down.
func (s *Server) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	// Fan out state updates to all WebSocket clients.
	sub := s.mgr.Subscribe()
	go s.fanOut(sub)

	log.Printf("[server] listening on http://%s", addr)
	return s.httpSrv.ListenAndServe()
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

type wsMsg struct {
	Type interface{} `json:"type"`
	// varies by type
	Devices  interface{} `json:"devices,omitempty"`
	DeviceID string      `json:"device_id,omitempty"`
	DevType  string      `json:"device_type,omitempty"`
	DevName  string      `json:"device_name,omitempty"`
	State    interface{} `json:"state,omitempty"`
	Message  string      `json:"message,omitempty"`
	// command fields
	Command string                 `json:"command,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type wsClient struct {
	conn   *websocket.Conn
	sendCh chan wsMsg
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade: %v", err)
		return
	}
	client := &wsClient{conn: conn, sendCh: make(chan wsMsg, 64)}
	s.clientsMu.Lock()
	s.clients[client] = struct{}{}
	s.clientsMu.Unlock()

	// Send current device list immediately after connect.
	devs := s.mgr.Devices()
	devList := make([]map[string]interface{}, 0, len(devs))
	for _, d := range devs {
		entry := map[string]interface{}{
			"id": d.ID, "type": d.Type, "name": d.Name, "connected": d.Connected,
			"port": d.Port, "baud": d.Baud,
		}
		if st, ok := s.mgr.LatestState(d.ID); ok {
			entry["state"] = st
		}
		devList = append(devList, entry)
	}
	client.sendCh <- wsMsg{Type: "init", Devices: devList}

	go client.writeLoop()

	// Read loop – handles incoming commands.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg wsMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			client.sendCh <- wsMsg{Type: "error", Message: fmt.Sprintf("bad JSON: %v", err)}
			continue
		}
		if msg.Type == "cmd" {
			go func() {
				req := manager.CmdRequest{
					DeviceID: msg.DeviceID,
					Command:  msg.Command,
					Params:   msg.Params,
				}
				if err := s.mgr.SendCmd(req); err != nil {
					client.sendCh <- wsMsg{Type: "error", Message: err.Error()}
				}
			}()
		}
	}

	s.clientsMu.Lock()
	delete(s.clients, client)
	s.clientsMu.Unlock()
	close(client.sendCh)
}

func (c *wsClient) writeLoop() {
	for msg := range c.sendCh {
		data, _ := json.Marshal(msg)
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			break
		}
	}
	c.conn.Close()
}

func (s *Server) fanOut(sub chan manager.StateUpdate) {
	for upd := range sub {
		msg := wsMsg{
			Type:     "state",
			DeviceID: upd.DeviceID,
			DevType:  upd.DeviceType,
			DevName:  upd.DeviceName,
			State:    upd.State,
		}
		s.clientsMu.Lock()
		for c := range s.clients {
			select {
			case c.sendCh <- msg:
			default:
			}
		}
		s.clientsMu.Unlock()
	}
}

// ─── REST handlers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	devs := s.mgr.Devices()
	result := make([]map[string]interface{}, 0, len(devs))
	for _, d := range devs {
		entry := map[string]interface{}{
			"id": d.ID, "type": d.Type, "name": d.Name, "connected": d.Connected,
			"port": d.Port, "baud": d.Baud,
		}
		if st, ok := s.mgr.LatestState(d.ID); ok {
			entry["state"] = st
		}
		result = append(result, entry)
	}
	writeJSON(w, result)
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	// /api/device/{id}/state  or  /api/device/{id}/cmd
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/device/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "path: /api/device/{id}/{state|cmd}")
		return
	}
	id, action := parts[0], parts[1]

	switch action {
	case "state":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		st, ok := s.mgr.LatestState(id)
		if !ok {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		writeJSON(w, st)

	case "cmd":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot read body")
			return
		}
		var msg wsMsg
		if err := json.Unmarshal(body, &msg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		req := manager.CmdRequest{
			DeviceID: id,
			Command:  msg.Command,
			Params:   msg.Params,
		}
		if err := s.mgr.SendCmd(req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	case "connect":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
		var req struct {
			Port string `json:"port"`
			Baud int    `json:"baud"`
		}
		_ = json.Unmarshal(body, &req)
		if err := s.mgr.ReconnectDevice(id, req.Port, req.Baud); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	case "disconnect":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		if err := s.mgr.DisconnectDevice(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		writeError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *Server) handleSerialPorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ports, err := serial.GetPortsList()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ports)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Config()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, cfg)
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot read body")
			return
		}
		var newCfg config.Config
		if err := json.Unmarshal(body, &newCfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.mgr.ReplaceConfig(&newCfg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleConfigDevice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot read body")
			return
		}
		var dc config.DeviceConfig
		if err := json.Unmarshal(body, &dc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.mgr.AddDevice(dc); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	case http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/api/config/device/")
		if err := s.mgr.RemoveDevice(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "POST or DELETE only")
	}
}
