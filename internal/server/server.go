package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config controls the relay server.
type Config struct {
	AdminToken    string
	DeviceToken   string
	DataDir       string
	WebFS         fs.FS
	AdminUser     string
	AdminPassword string
}

type agentConn struct {
	deviceID   string
	publicIP   string
	directPort int
	ws         *websocket.Conn
	writeMu    sync.Mutex
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

type forwardSession struct {
	source string
	target string
	direct bool
	timer  *time.Timer
}

type screenSession struct {
	sessionID string
	deviceID  string
	browser   *websocket.Conn
	link      *websocket.Conn
	linkReady chan struct{}
	done      chan struct{}
	linkSet   bool
}

type sessionEntry struct {
	username  string
	expiresAt time.Time
}

type loginAttempt struct {
	count int
	until time.Time
}

const (
	maxLoginAttempts = 5
	loginLockoutTime = time.Minute
)

// Server is the relay control plane.
type Server struct {
	cfg           Config
	reg           *Registry
	upgrader      websocket.Upgrader
	agentsMu      sync.RWMutex
	agents        map[string]*agentConn
	sessionsMu    sync.RWMutex
	terms         map[string]*termSession
	files         map[string]*fileSession
	forwards      map[string]*forwardSession
	screens       map[string]*screenSession
	sessions      map[string]sessionEntry
	loginMu       sync.Mutex
	loginAttempts map[string]loginAttempt
}

// NewServer creates a relay server with the embedded web UI.
func NewServer(cfg Config) (*Server, error) {
	reg, err := NewRegistry(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if cfg.AdminUser != "" && cfg.AdminPassword != "" {
		if err := reg.EnsureUser(cfg.AdminUser, cfg.AdminPassword, "admin"); err != nil {
			return nil, err
		}
	}
	return &Server{
		cfg: cfg,
		reg: reg,
		upgrader: websocket.Upgrader{
			CheckOrigin: sameOriginCheck,
		},
		agents:        make(map[string]*agentConn),
		terms:         make(map[string]*termSession),
		files:         make(map[string]*fileSession),
		forwards:      make(map[string]*forwardSession),
		screens:       make(map[string]*screenSession),
		sessions:      make(map[string]sessionEntry),
		loginAttempts: make(map[string]loginAttempt),
	}, nil
}

// Handler returns the full HTTP handler used by tests and single-listener
// deployments.
func (s *Server) Handler() http.Handler {
	return s.routes(true)
}

// WebHandler serves the console API, browser WebSockets and static UI.
func (s *Server) WebHandler() http.Handler {
	return s.routes(false)
}

func (s *Server) routes(includeAgent bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/", s.handleDeviceByID)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/device-keys", s.handleDeviceKeys)
	mux.HandleFunc("/api/device-keys/", s.handleDeviceKeyByID)
	mux.HandleFunc("/api/audit", s.handleAudit)
	if includeAgent {
		mux.HandleFunc("/ws/agent", s.handleAgentWS)
	}
	mux.HandleFunc("/ws/terminal", s.handleTerminalWS)
	mux.HandleFunc("/ws/file", s.handleFileWS)
	mux.HandleFunc("/ws/screen", s.handleScreenWS)
	if includeAgent {
		mux.HandleFunc("/ws/screen-link", s.handleScreenLinkWS)
	}
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

// AgentHandler serves only the agent-facing data plane.
func (s *Server) AgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/ws/agent", s.handleAgentWS)
	mux.HandleFunc("/ws/screen-link", s.handleScreenLinkWS)
	return mux
}

// Close releases server-owned resources.
func (s *Server) Close() error {
	return s.reg.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	key := req.Username + "|" + clientIP(r)
	if !s.allowLoginAttempt(key) {
		_ = s.reg.RecordAudit(req.Username, "login.locked", req.Username, "too many failures")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts, try again later"})
		return
	}
	user, err := s.reg.ValidateUser(req.Username, req.Password)
	if err != nil {
		s.recordLoginFailure(key)
		_ = s.reg.RecordAudit(req.Username, "login.failed", req.Username, "bad credentials")
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	s.clearLoginAttempts(key)
	token, err := newSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "session creation failed"})
		return
	}
	s.sessionsMu.Lock()
	s.sessions[token] = sessionEntry{username: user.Username, expiresAt: time.Now().Add(24 * time.Hour)}
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(user.Username, "login.ok", user.Username, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": user.Username,
		"role":     user.Role,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}
	username := ""
	s.sessionsMu.Lock()
	if entry, ok := s.sessions[token]; ok {
		username = entry.username
		delete(s.sessions, token)
	}
	s.sessionsMu.Unlock()
	if username != "" {
		_ = s.reg.RecordAudit(username, "logout", username, "")
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) authorizeAgent(deviceID, token string) bool {
	if s.validDevice(token) {
		return true
	}
	return s.reg.ValidateDeviceKey(deviceID, token)
}

func (s *Server) authenticate(r *http.Request) (string, bool) {
	token := r.URL.Query().Get("token")
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}
	if s.validAdmin(token) {
		return "admin-token", true
	}
	return s.validSession(token)
}

func (s *Server) validSession(token string) (string, bool) {
	s.sessionsMu.RLock()
	entry, ok := s.sessions[token]
	s.sessionsMu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			s.sessionsMu.Lock()
			delete(s.sessions, token)
			s.sessionsMu.Unlock()
		}
		return "", false
	}
	return entry.username, true
}

func (s *Server) allowLoginAttempt(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	attempt, ok := s.loginAttempts[key]
	if ok && time.Now().Before(attempt.until) && attempt.count >= maxLoginAttempts {
		return false
	}
	return true
}

func (s *Server) recordLoginFailure(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	attempt := s.loginAttempts[key]
	attempt.count++
	if attempt.count >= maxLoginAttempts {
		attempt.until = time.Now().Add(loginLockoutTime)
	}
	s.loginAttempts[key] = attempt
}

func (s *Server) clearLoginAttempts(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginAttempts, key)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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

func newSessionToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sameOriginCheck accepts browser WebSockets from the page's own host and
// non-browser clients (agents and tests) that send no Origin header.
func sameOriginCheck(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
