package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config controls the relay server.
type Config struct {
	AdminToken    string
	DataDir       string
	WebFS         fs.FS
	AdminUser     string
	AdminPassword string
	AgentListen   string
	KeyTTL        time.Duration
}

type agentConn struct {
	deviceID   string
	publicIP   string
	directPort int
	ws         *websocket.Conn
	writeMu    sync.Mutex
	lastSyncMu sync.Mutex
	lastSync   time.Time
}

type termSession struct {
	deviceID   string
	browser    *websocket.Conn
	traffic    *trafficRecorder
	connection string
	transport  string
	direct     bool
	writeMu    sync.Mutex
}

func (s *termSession) write(msg any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.browser.WriteJSON(msg)
}

type fileSession struct {
	deviceID string
	browser  *websocket.Conn
	op       string
	traffic  *trafficRecorder
	writeMu  sync.Mutex
}

func (s *fileSession) write(msg any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.browser.WriteJSON(msg)
}

type managerSession struct {
	deviceID string
	browser  *websocket.Conn
	writeMu  sync.Mutex
}

func (s *managerSession) write(msg any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.browser.WriteJSON(msg)
}

type forwardSession struct {
	mu        sync.Mutex
	source    string
	target    string
	direct    bool
	transport string
	timer     *time.Timer
	traffic   *trafficRecorder
}

type screenSession struct {
	sessionID  string
	deviceID   string
	browser    *websocket.Conn
	link       *websocket.Conn
	linkReady  chan struct{}
	done       chan struct{}
	linkSet    bool
	traffic    *trafficRecorder
	connection string
	transport  string
	direct     bool
	writeMu    sync.Mutex
}

func (s *screenSession) write(msg any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.browser.WriteJSON(msg)
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

	// Browser keepalive. The pong wait is intentionally generous: a page busy
	// uploading or decoding desktop frames may be slow to answer a ping, and a
	// short deadline would evict healthy sessions.
	browserPingPeriod = 45 * time.Second
	browserPongWait   = 3 * time.Minute
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
	managers      map[string]*managerSession
	forwards      map[string]*forwardSession
	screens       map[string]*screenSession
	sessions      map[string]sessionEntry
	loginMu       sync.Mutex
	loginAttempts map[string]loginAttempt
	tickets       *ticketStore
	stop          chan struct{}
	stopOnce      sync.Once
	// wsWG tracks every WebSocket handler. Their teardown writes to the registry
	// (last_seen, agent.offline, connection state) and runs in the handler
	// goroutine, and httptest.Server.Close does not wait for hijacked
	// connections, so Close must drain them before closing the database.
	wsWG sync.WaitGroup
}

// NewServer creates a relay server with the embedded web UI.
func NewServer(cfg Config) (*Server, error) {
	// Fail closed on a missing admin token. An empty token would compare equal
	// to an empty request credential, turning every endpoint into an open door.
	if strings.TrimSpace(cfg.AdminToken) == "" {
		return nil, errors.New("admin token must not be empty")
	}
	reg, err := NewRegistry(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if cfg.AdminUser != "" && cfg.AdminPassword != "" {
		if err := reg.EnsureUser(cfg.AdminUser, cfg.AdminPassword, "admin"); err != nil {
			return nil, err
		}
	}
	s := &Server{
		cfg: cfg,
		reg: reg,
		upgrader: websocket.Upgrader{
			CheckOrigin: sameOriginCheck,
		},
		agents:        make(map[string]*agentConn),
		terms:         make(map[string]*termSession),
		files:         make(map[string]*fileSession),
		managers:      make(map[string]*managerSession),
		forwards:      make(map[string]*forwardSession),
		screens:       make(map[string]*screenSession),
		sessions:      make(map[string]sessionEntry),
		loginAttempts: make(map[string]loginAttempt),
		tickets:       newTicketStore(),
		stop:          make(chan struct{}),
	}
	s.startJanitor()
	return s, nil
}

// sweepInterval is how often expired sessions and stale login attempts are
// reaped. Without it both maps grow for the lifetime of the process.
const sweepInterval = 10 * time.Minute

// sweepAuthState drops expired sessions and expired login-lockout entries.
func (s *Server) sweepAuthState() {
	now := time.Now()
	s.sessionsMu.Lock()
	for token, entry := range s.sessions {
		if now.After(entry.expiresAt) {
			delete(s.sessions, token)
		}
	}
	s.sessionsMu.Unlock()

	s.loginMu.Lock()
	for key, attempt := range s.loginAttempts {
		if attempt.count >= maxLoginAttempts && now.After(attempt.until) {
			delete(s.loginAttempts, key)
		}
	}
	s.loginMu.Unlock()

	// Tickets are normally removed on first use; this catches the ones that were
	// issued and never presented.
	s.tickets.sweep()
}

// startJanitor reaps expired in-memory auth state until the server is closed.
func (s *Server) startJanitor() {
	safeGo("auth state janitor", func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.sweepAuthState()
			case <-s.stop:
				return
			}
		}
	})
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
	mux.HandleFunc("/api/analytics", s.handleAnalytics)
	mux.HandleFunc("/api/device-keys", s.handleDeviceKeys)
	mux.HandleFunc("/api/device-keys/", s.handleDeviceKeyByID)
	mux.HandleFunc("/api/invites", s.handleInvites)
	mux.HandleFunc("/api/invites/", s.handleInviteByCode)
	mux.HandleFunc("/api/enroll", s.handleEnroll)
	mux.HandleFunc("/api/users", s.handleUsers)
	mux.HandleFunc("/api/users/", s.handleUserByUsername)
	mux.HandleFunc("/api/audit", s.handleAudit)
	mux.HandleFunc("/api/agent-config", s.handleAgentConfig)
	mux.HandleFunc("/api/ws-ticket", s.handleWSTicket)
	mux.HandleFunc("/api/forward-policy", s.handleForwardPolicy)
	mux.HandleFunc("/api/forward-grants", s.handleForwardGrants)
	if includeAgent {
		mux.HandleFunc("/ws/agent", s.handleAgentWS)
	}
	mux.HandleFunc("/ws/terminal", s.handleTerminalWS)
	mux.HandleFunc("/ws/file", s.handleFileWS)
	mux.HandleFunc("/ws/files", s.handleFilesWS)
	mux.HandleFunc("/ws/screen", s.handleScreenWS)
	if includeAgent {
		mux.HandleFunc("/ws/screen-link", s.handleScreenLinkWS)
	}
	mux.HandleFunc("/", s.handleStatic)
	return s.withRequestLog(mux)
}

// AgentHandler serves only the agent-facing data plane.
func (s *Server) AgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/agent-config", s.handleAgentConfig)
	mux.HandleFunc("/ws/agent", s.handleAgentWS)
	mux.HandleFunc("/ws/screen-link", s.handleScreenLinkWS)
	return s.withRequestLog(mux)
}

// Close releases server-owned resources.
func (s *Server) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	// A WebSocket handler only returns once its peer goes away, so close the
	// live sockets to unblock them. Draining matters because their teardown
	// writes to the registry (last_seen, agent.offline, connection state), and
	// httptest.Server.Close does not wait for hijacked connections.
	s.closeWebSockets()
	s.waitForHandlers(handlerDrainTimeout)
	return s.reg.Close()
}

// handlerDrainTimeout bounds how long Close waits for handler teardown. A
// handler that is stuck on something other than the socket must not hang
// shutdown forever.
const handlerDrainTimeout = 5 * time.Second

// closeWebSockets closes every live browser and agent socket.
func (s *Server) closeWebSockets() {
	s.sessionsMu.Lock()
	var conns []*websocket.Conn
	for _, sess := range s.terms {
		conns = append(conns, sess.browser)
	}
	for _, sess := range s.screens {
		conns = append(conns, sess.browser)
	}
	for _, sess := range s.files {
		conns = append(conns, sess.browser)
	}
	for _, sess := range s.managers {
		conns = append(conns, sess.browser)
	}
	s.sessionsMu.Unlock()

	s.agentsMu.RLock()
	for _, conn := range s.agents {
		conns = append(conns, conn.ws)
	}
	s.agentsMu.RUnlock()

	for _, conn := range conns {
		_ = conn.Close()
	}
}

// waitForHandlers blocks until every WebSocket handler has finished its
// teardown, or the timeout expires.
func (s *Server) waitForHandlers(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.wsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.logf("shutdown: handlers still running after %s", timeout)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleAgentConfig publishes the agent-plane endpoint so clients do not need
// to hardcode the relay port.
func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	port := strings.TrimPrefix(s.cfg.AgentListen, ":")
	if port == "" {
		port = "443"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agentWSS": fmt.Sprintf("%s://%s:%s/ws/agent", scheme, host, port),
	})
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
	if !checkCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request rejected"})
		return
	}
	key := req.Username + "|" + clientIP(r)
	if !s.allowLoginAttempt(key) {
		_ = s.reg.RecordAudit(req.Username, "login.locked", req.Username, "too many failures")
		s.logEvent("auth.login.locked", "user", req.Username, "ip", clientIP(r))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts, try again later"})
		return
	}
	user, err := s.reg.ValidateUser(req.Username, req.Password)
	if err != nil {
		s.recordLoginFailure(key)
		_ = s.reg.RecordAudit(req.Username, "login.failed", req.Username, "bad credentials")
		// Also put it on the process log: an operator watching journald should
		// not have to query the audit table to notice a brute-force run.
		s.logEvent("auth.login.failed", "user", req.Username, "ip", clientIP(r), "reason", "bad-credentials")
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
	// The cookie is the primary credential; the JSON token is still returned
	// for CLI clients that authenticate with a header.
	setSessionCookie(w, r, token)
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
	clearSessionCookie(w, r)
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
	// Hashed build assets are immutable; the HTML entry must revalidate so a
	// new deploy is picked up without a manual cache clear.
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	_, _ = w.Write(raw)
}

func (s *Server) validAdmin(token string) bool {
	// Guard the empty-config case explicitly: ConstantTimeCompare reports two
	// empty slices as equal, which would authenticate an empty credential.
	if token == "" || s.cfg.AdminToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) == 1
}

func (s *Server) authorizeAgent(deviceID, token string) bool {
	return s.reg.ValidateDeviceKey(deviceID, token)
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func (s *Server) authenticate(r *http.Request) (string, bool) {
	// Cookie-based sessions are sent automatically by the browser, so a
	// state-changing request carrying one must be same-origin. Enforcing it in
	// the single authentication chokepoint means a new endpoint cannot forget
	// it. Header/query callers (CLI, tests) send no cookie and are unaffected.
	if !checkCSRF(r) {
		return "", false
	}
	token := requestToken(r)
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
	user, err := s.reg.GetUser(entry.username)
	if err != nil {
		// The account no longer exists (deleted or renamed): the session must
		// not survive it. Previously this fell through and stayed valid until
		// its 24h expiry.
		s.sessionsMu.Lock()
		delete(s.sessions, token)
		s.sessionsMu.Unlock()
		return "", false
	}
	if !user.ExpiresAt.IsZero() && time.Now().After(user.ExpiresAt) {
		s.sessionsMu.Lock()
		delete(s.sessions, token)
		s.sessionsMu.Unlock()
		return "", false
	}
	return entry.username, true
}

func (s *Server) actorInfo(actor string) (User, bool) {
	if actor == "admin-token" {
		return User{Role: "admin"}, true
	}
	user, err := s.reg.GetUser(actor)
	if err != nil {
		return User{}, false
	}
	if !user.ExpiresAt.IsZero() && time.Now().After(user.ExpiresAt) {
		return User{}, false
	}
	return user, true
}

func (s *Server) isAdmin(actor string) bool {
	user, ok := s.actorInfo(actor)
	return ok && user.Role == "admin"
}

func (s *Server) canAccessDevice(actor, deviceID string) bool {
	user, ok := s.actorInfo(actor)
	if !ok {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	for _, id := range user.DeviceIDs {
		if id == deviceID {
			return true
		}
	}
	return false
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

// logf writes one relay event. The destination is swappable so tests can
// capture the log instead of the process stderr.
func (s *Server) logf(format string, args ...any) {
	// All relay logging goes to the standard logger, which already writes to
	// stderr; a request-log test swaps it for a buffer via log.SetOutput.
	log.Printf("[relay] "+format, args...)
}

// bind renders a compact structured event: logBind("session.start", "actor", a, "device", d).
//
// The relay emits key=value pairs so a line can be read by a human and still
// be grepped or parsed without a logging pipeline: it has no external
// dependency, and systemd's journal already stamps time and unit.
func bind(kv ...any) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		key := fmt.Sprint(kv[i])
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(formatValue(kv[i+1]))
	}
	return b.String()
}

// formatValue quotes values containing spaces so a field stays one token.
func formatValue(v any) string {
	s := fmt.Sprint(v)
	if s == "" {
		return "-"
	}
	if strings.ContainsAny(s, " \"=") {
		return strconv.Quote(s)
	}
	return s
}

func (s *Server) logEvent(event string, kv ...any) {
	if len(kv) == 0 {
		s.logf("%s", event)
		return
	}
	s.logf("%s %s", event, bind(kv...))
}

// debugRTC enables the per-candidate ICE diagnostics. They are invaluable when
// a direct connection fails to establish and pure noise otherwise, so they are
// opt-in via DEVICE_RELAY_DEBUG_RTC=1.
var debugRTC = os.Getenv("DEVICE_RELAY_DEBUG_RTC") == "1"

// logDebug records a diagnostic event only when debugging is enabled.
func (s *Server) logDebug(event string, kv ...any) {
	if !debugRTC {
		return
	}
	s.logEvent("debug."+event, kv...)
}

// safeGo runs fn in its own goroutine and converts a panic into a logged error.
// Anything spawned alongside an HTTP handler lives outside net/http's own panic
// recovery, so without this a single bad frame could take down the relay and
// disconnect every device.
func safeGo(what string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[relay] panic in %s: %v\n%s", what, r, debug.Stack())
			}
		}()
		fn()
	}()
}

// removeAgent drops the registry entry for a device only if it still points at
// the connection that is shutting down. A reconnect replaces the entry before
// the old handler unwinds, so an unconditional delete would evict the fresh,
// healthy connection and mark the device offline.
func (s *Server) removeAgent(id string, conn *agentConn) bool {
	s.agentsMu.Lock()
	current, ok := s.agents[id]
	if !ok || current != conn {
		s.agentsMu.Unlock()
		return false
	}
	delete(s.agents, id)
	s.agentsMu.Unlock()
	_ = s.reg.SetOnline(id, false)
	return true
}

func (s *Server) getAgent(id string) *agentConn {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.agents[id]
}

// agentOnlineSyncInterval throttles the last_seen write. Terminal and desktop
// output produce thousands of inbound frames per second; writing on every one
// would serialize the whole control plane behind SQLite.
const agentOnlineSyncInterval = 10 * time.Second

// touchAgent refreshes a device's liveness without issuing a database write on
// every inbound frame.
func (s *Server) touchAgent(id string) {
	conn := s.getAgent(id)
	if conn == nil {
		return
	}
	conn.lastSyncMu.Lock()
	due := time.Since(conn.lastSync) >= agentOnlineSyncInterval
	if due {
		conn.lastSync = time.Now()
	}
	conn.lastSyncMu.Unlock()
	if !due {
		return
	}
	if err := s.reg.SetOnline(id, true); err != nil {
		s.logf("update last seen for %s: %v", id, err)
	}
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
