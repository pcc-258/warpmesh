package server

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// Config controls the relay server.
type Config struct {
	AdminToken  string
	DeviceToken string
	DataDir     string
	WebFS       fs.FS
}

type agentConn struct {
	deviceID string
	ws       *websocket.Conn
	writeMu  sync.Mutex
}

type termSession struct {
	deviceID string
	browser  *websocket.Conn
}

type fileSession struct {
	deviceID string
	browser  *websocket.Conn
	op       string
}

// Server is the relay control plane.
type Server struct {
	cfg       Config
	reg       *Registry
	upgrader  websocket.Upgrader
	agentsMu  sync.RWMutex
	agents    map[string]*agentConn
	sessionsMu sync.RWMutex
	terms     map[string]*termSession
	files     map[string]*fileSession
}

// NewServer creates a relay server with the embedded web UI.
func NewServer(cfg Config) (*Server, error) {
	reg, err := NewRegistry(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:      cfg,
		reg:      reg,
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		agents:   make(map[string]*agentConn),
		terms:    make(map[string]*termSession),
		files:    make(map[string]*fileSession),
	}, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/", s.handleDeviceByID)
	mux.HandleFunc("/ws/agent", s.handleAgentWS)
	mux.HandleFunc("/ws/terminal", s.handleTerminalWS)
	mux.HandleFunc("/ws/file", s.handleFileWS)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	var root fs.FS
	if s.cfg.WebFS != nil {
		root = s.cfg.WebFS
	}
	if root == nil {
		http.Error(w, "web ui not available", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	raw, err := fs.ReadFile(root, path)
	if err != nil {
		raw, err = fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		path = "index.html"
	}
	if ctype := mime.TypeByExtension(filepath.Ext(path)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Write(raw)
}

func (s *Server) validAdmin(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) == 1
}

func (s *Server) validDevice(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.DeviceToken)) == 1
}

func (s *Server) adminFromRequest(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return s.validAdmin(strings.TrimPrefix(auth, "Bearer "))
	}
	return s.validAdmin(r.URL.Query().Get("token"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) logf(format string, args ...any) {
	log.Printf("[relay] "+format, args...)
}

func (s *Server) removeAgent(id string) {
	s.agentsMu.Lock()
	delete(s.agents, id)
	s.agentsMu.Unlock()
	_ = s.reg.SetOnline(id, false)
}

func (s *Server) getAgent(id string) *agentConn {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.agents[id]
}

func (a *agentConn) write(v any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.ws.WriteJSON(v)
}
