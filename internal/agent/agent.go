// Package agent implements the outbound device agent.
package agent

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

// Config configures a device agent.
type Config struct {
	ServerURL   string
	Token       string
	DeviceID    string
	Name        string
	Shell       string
	DataDir     string
	Insecure    bool
	AllowPaths  []string
	Forwards    []ForwardSpec
	STUNServers []string
	VNCPort     int
	UIPort      int
	AutoVNC     bool
}

// ForwardSpec exposes a local TCP listener that reaches a port on another
// device, direct-first with server relay fallback.
type ForwardSpec struct {
	LocalPort      int
	TargetDeviceID string
	TargetPort     int
}

type forwardConn struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

func (f *forwardConn) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, net.ErrClosed
	}
	return f.conn.Read(p)
}

func (f *forwardConn) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, net.ErrClosed
	}
	return f.conn.Write(p)
}

func (f *forwardConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	return f.conn.Close()
}

// Agent maintains one control connection to the relay server.
type Agent struct {
	cfg              Config
	sessions         map[string]*TermSession
	uploads          map[string]*os.File
	forwards         map[string]*forwardConn
	forwardPending   map[string]chan protocol.Message
	directPending    map[string]chan struct{}
	peerConns        map[string]*webrtc.PeerConnection
	dataChannels     map[string]*webrtc.DataChannel
	localConns       map[string]net.Conn
	pendingData      map[string][][]byte
	mu               sync.Mutex
	ws               *websocket.Conn
	writeMu          sync.Mutex
	forwardListeners map[string]*forwardListener
}

type forwardListener struct {
	spec ForwardSpec
	ln   net.Listener
}

// New creates an agent and ensures a stable device identity.
func New(cfg Config) (*Agent, error) {
	if cfg.DataDir != "" {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, "uploads"), 0o755); err != nil {
			return nil, err
		}
	}
	if cfg.Token == "" && cfg.DataDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "token")); err == nil {
			cfg.Token = strings.TrimSpace(string(raw))
		}
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("device token is required; enroll with warpmesh-agent enroll or pass -token")
	}
	deviceID, err := loadOrCreateID(cfg)
	if err != nil {
		return nil, err
	}
	cfg.DeviceID = deviceID
	if cfg.Name == "" {
		if hostname, err := os.Hostname(); err == nil {
			cfg.Name = hostname
		}
	}
	if cfg.Shell == "" {
		cfg.Shell = defaultShell()
	}
	if len(cfg.AllowPaths) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.AllowPaths = []string{home}
		}
	}
	if len(cfg.STUNServers) == 0 {
		cfg.STUNServers = []string{"stun:stun.l.google.com:19302"}
	}
	if cfg.VNCPort == 0 {
		cfg.VNCPort = 5900
	}
	a := &Agent{
		cfg:              cfg,
		sessions:         make(map[string]*TermSession),
		uploads:          make(map[string]*os.File),
		forwards:         make(map[string]*forwardConn),
		forwardPending:   make(map[string]chan protocol.Message),
		directPending:    make(map[string]chan struct{}),
		peerConns:        make(map[string]*webrtc.PeerConnection),
		dataChannels:     make(map[string]*webrtc.DataChannel),
		localConns:       make(map[string]net.Conn),
		pendingData:      make(map[string][][]byte),
		forwardListeners: make(map[string]*forwardListener),
	}
	a.startForwardListeners()
	if cfg.UIPort > 0 {
		a.startLocalUI()
	}
	if cfg.AutoVNC {
		go a.ensureVNCServer()
	}
	return a, nil
}

// Run connects and reconnects until the process exits.
func (a *Agent) Run() {
	backoff := time.Second
	for {
		err := a.connectOnce()
		if err != nil {
			log.Printf("agent disconnected from %s: %v", a.cfg.ServerURL, err)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) connectOnce() error {
	u, err := url.Parse(a.cfg.ServerURL)
	if err != nil {
		return err
	}

	dialer := websocket.DefaultDialer
	if a.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Token)
	ws, _, err := dialer.Dial(u.String(), header)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.ws = ws
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.ws = nil
		a.mu.Unlock()
		_ = ws.Close()
		a.cleanupSessions()
	}()

	if err := a.send(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: a.cfg.DeviceID,
		Name:     a.cfg.Name,
		Hostname: a.cfg.Name,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		LANIPs:   lanIPs(),
	}); err != nil {
		return err
	}
	log.Printf("agent %s connected to %s", a.cfg.DeviceID, u.Host)

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			return err
		}
		if err := a.handleMessage(msg); err != nil {
			log.Printf("handle message: %v", err)
		}
	}
}

func (a *Agent) handleMessage(msg protocol.Message) error {
	switch msg.Type {
	case protocol.TypePing:
		return a.send(protocol.Message{Type: protocol.TypePong})
	case protocol.TypeTermStart:
		session, err := startTerminalSession(func(m protocol.Message) error {
			return a.send(m)
		}, msg.SessionID, a.cfg.Shell, msg.Cols, msg.Rows)
		if err != nil {
			return a.send(protocol.Message{
				Type:      protocol.TypeTermError,
				SessionID: msg.SessionID,
				Error:     err.Error(),
			})
		}
		a.mu.Lock()
		a.sessions[msg.SessionID] = session
		a.mu.Unlock()
		return nil
	case protocol.TypeTermInput:
		return a.writeSession(msg.SessionID, func(s *TermSession) error { return s.Input(msg.Data) })
	case protocol.TypeTermResize:
		return a.writeSession(msg.SessionID, func(s *TermSession) error { return s.Resize(msg.Cols, msg.Rows) })
	case protocol.TypeTermStop:
		a.mu.Lock()
		session := a.sessions[msg.SessionID]
		delete(a.sessions, msg.SessionID)
		a.mu.Unlock()
		if session != nil {
			return session.Close()
		}
		return nil
	case protocol.TypeFileUpload:
		name := filepath.Base(msg.Name)
		f, err := os.Create(filepath.Join(a.cfg.DataDir, "uploads", name))
		if err != nil {
			return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
		}
		a.mu.Lock()
		a.uploads[msg.SessionID] = f
		a.mu.Unlock()
		return nil
	case protocol.TypeFileChunk:
		raw, err := protocol.DecodeData(msg.Data)
		if err != nil {
			return err
		}
		a.mu.Lock()
		f := a.uploads[msg.SessionID]
		a.mu.Unlock()
		if f == nil {
			return nil
		}
		_, err = f.Write(raw)
		return err
	case protocol.TypeFileUploadEnd, protocol.TypeFileError:
		a.mu.Lock()
		f := a.uploads[msg.SessionID]
		delete(a.uploads, msg.SessionID)
		a.mu.Unlock()
		if f == nil {
			return nil
		}
		return f.Close()
	case protocol.TypeFileDownload:
		return a.streamFile(msg)
	case protocol.TypeForwardConnect:
		return a.handleForwardTarget(msg)
	case protocol.TypeForwardOffer:
		go a.handleDirectOffer(msg)
		return nil
	case protocol.TypeForwardAnswer:
		a.handleDirectAnswer(msg)
		return nil
	case protocol.TypeForwardICE:
		a.handleDirectICE(msg)
		return nil
	case protocol.TypeForwardDirectOK:
		return nil
	case protocol.TypeScreenStart:
		go a.startScreenLink(msg.SessionID)
		return nil
	case protocol.TypeForwardOpen, protocol.TypeForwardError:
		a.mu.Lock()
		pending := a.forwardPending[msg.SessionID]
		a.mu.Unlock()
		if pending != nil {
			pending <- msg
		}
		return nil
	case protocol.TypeForwardData:
		raw, err := protocol.DecodeData(msg.Data)
		if err != nil {
			return err
		}
		a.mu.Lock()
		fc := a.forwards[msg.SessionID]
		a.mu.Unlock()
		if fc != nil {
			_, _ = fc.Write(raw)
		}
		return nil
	case protocol.TypeForwardClose:
		a.mu.Lock()
		fc := a.forwards[msg.SessionID]
		delete(a.forwards, msg.SessionID)
		a.mu.Unlock()
		if fc != nil {
			_ = fc.Close()
		}
		return nil
	}
	return nil
}

func (a *Agent) writeSession(sessionID string, fn func(*TermSession) error) error {
	a.mu.Lock()
	session := a.sessions[sessionID]
	a.mu.Unlock()
	if session == nil {
		return nil
	}
	return fn(session)
}

func (a *Agent) startForwardListeners() {
	for _, spec := range a.cfg.Forwards {
		if err := a.StartForward(spec); err != nil {
			log.Printf("forward %d failed: %v", spec.LocalPort, err)
		}
	}
}

// StartForward begins a dynamic local listener that relays to another device.
func (a *Agent) StartForward(spec ForwardSpec) error {
	key := fmt.Sprintf("%d", spec.LocalPort)
	a.mu.Lock()
	if _, ok := a.forwardListeners[key]; ok {
		a.mu.Unlock()
		return fmt.Errorf("forward %d already exists", spec.LocalPort)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spec.LocalPort))
	if err != nil {
		a.mu.Unlock()
		return err
	}
	a.forwardListeners[key] = &forwardListener{spec: spec, ln: ln}
	a.mu.Unlock()
	log.Printf("forward 127.0.0.1:%d -> %s:%d", spec.LocalPort, spec.TargetDeviceID, spec.TargetPort)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go a.handleForwardClient(conn, spec)
		}
	}()
	return nil
}

// StopForward removes a dynamic local listener.
func (a *Agent) StopForward(localPort int) error {
	key := fmt.Sprintf("%d", localPort)
	a.mu.Lock()
	fwd, ok := a.forwardListeners[key]
	if ok {
		delete(a.forwardListeners, key)
	}
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("forward %d not found", localPort)
	}
	return fwd.ln.Close()
}

// ListForwards returns the active local forwarding listeners.
func (a *Agent) ListForwards() []ForwardSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ForwardSpec, 0, len(a.forwardListeners))
	for _, fwd := range a.forwardListeners {
		out = append(out, fwd.spec)
	}
	return out
}

func (a *Agent) handleForwardClient(conn net.Conn, spec ForwardSpec) {
	sessionID := newSessionID()
	relayReady := make(chan protocol.Message, 2)
	directReady := make(chan struct{}, 1)
	a.mu.Lock()
	a.forwardPending[sessionID] = relayReady
	a.directPending[sessionID] = directReady
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.forwardPending, sessionID)
		delete(a.directPending, sessionID)
		a.mu.Unlock()
	}()

	if err := a.send(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: sessionID,
		Target:    spec.TargetDeviceID,
		Port:      spec.TargetPort,
	}); err != nil {
		_ = conn.Close()
		return
	}
	// Direct path uses WebRTC ICE; the server stays on standby to fall back.
	if err := a.startDirectOffer(sessionID, spec.TargetPort); err != nil {
		log.Printf("forward %s: direct offer failed, waiting for relay: %v", sessionID, err)
	}

	select {
	case msg := <-relayReady:
		if msg.Type == protocol.TypeForwardError || msg.Type == protocol.TypeForwardClose {
			_ = conn.Close()
			return
		}
	case <-directReady:
		a.mu.Lock()
		dc := a.dataChannels[sessionID]
		a.localConns[sessionID] = conn
		a.mu.Unlock()
		if dc == nil {
			_ = conn.Close()
			return
		}
		a.copyLocalToChannel(sessionID, conn, dc)
		return
	case <-time.After(10 * time.Second):
		_ = conn.Close()
		return
	}

	fc := &forwardConn{conn: conn}
	a.mu.Lock()
	a.forwards[sessionID] = fc
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.forwards, sessionID)
		a.mu.Unlock()
		_ = fc.Close()
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if serr := a.send(protocol.Message{
				Type:      protocol.TypeForwardData,
				SessionID: sessionID,
				Data:      protocol.EncodeData(buf[:n]),
			}); serr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = a.send(protocol.Message{Type: protocol.TypeForwardClose, SessionID: sessionID})
}

func (a *Agent) handleForwardTarget(msg protocol.Message) error {
	if msg.SessionID == "" || msg.Port <= 0 {
		return a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: "invalid forward request"})
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", msg.Port), 5*time.Second)
	if err != nil {
		return a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: err.Error()})
	}
	fc := &forwardConn{conn: conn}
	a.mu.Lock()
	a.forwards[msg.SessionID] = fc
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.forwards, msg.SessionID)
		a.mu.Unlock()
		_ = fc.Close()
	}()

	if err := a.send(protocol.Message{Type: protocol.TypeForwardOpen, SessionID: msg.SessionID}); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if serr := a.send(protocol.Message{
				Type:      protocol.TypeForwardData,
				SessionID: msg.SessionID,
				Data:      protocol.EncodeData(buf[:n]),
			}); serr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = a.send(protocol.Message{Type: protocol.TypeForwardClose, SessionID: msg.SessionID})
	return nil
}

func (a *Agent) streamFile(msg protocol.Message) error {
	if !a.pathAllowed(msg.Path) {
		return a.send(protocol.Message{
			Type:      protocol.TypeFileError,
			SessionID: msg.SessionID,
			Error:     "path is outside the allowed directories",
		})
	}
	raw, err := os.ReadFile(msg.Path)
	if err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	const chunkSize = 64 * 1024
	for off := 0; off < len(raw); off += chunkSize {
		end := off + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		if err := a.send(protocol.Message{
			Type:      protocol.TypeFileChunk,
			SessionID: msg.SessionID,
			Data:      protocol.EncodeData(raw[off:end]),
		}); err != nil {
			return err
		}
	}
	return a.send(protocol.Message{Type: protocol.TypeFileDone, SessionID: msg.SessionID})
}

func (a *Agent) pathAllowed(path string) bool {
	if len(a.cfg.AllowPaths) == 0 {
		return true
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	for _, root := range a.cfg.AllowPaths {
		r, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if abs == r || strings.HasPrefix(abs, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (a *Agent) send(v any) error {
	a.mu.Lock()
	ws := a.ws
	a.mu.Unlock()
	if ws == nil {
		return net.ErrClosed
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return ws.WriteJSON(v)
}

func (a *Agent) cleanupSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, session := range a.sessions {
		_ = session.Close()
		delete(a.sessions, id)
	}
	for id, f := range a.uploads {
		_ = f.Close()
		delete(a.uploads, id)
	}
	for id, fc := range a.forwards {
		_ = fc.Close()
		delete(a.forwards, id)
	}
	for id, pc := range a.peerConns {
		_ = pc.Close()
		delete(a.peerConns, id)
	}
	for id, dc := range a.dataChannels {
		_ = dc.Close()
		delete(a.dataChannels, id)
	}
	for id, conn := range a.localConns {
		_ = conn.Close()
		delete(a.localConns, id)
	}
	for id := range a.pendingData {
		delete(a.pendingData, id)
	}
	for id := range a.forwardPending {
		delete(a.forwardPending, id)
	}
	for id := range a.directPending {
		delete(a.directPending, id)
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func loadOrCreateID(cfg Config) (string, error) {
	if cfg.DeviceID != "" {
		return cfg.DeviceID, nil
	}
	path := filepath.Join(cfg.DataDir, "device-id")
	if raw, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "dev-" + hex.EncodeToString(b)
	if cfg.DataDir != "" {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			return "", err
		}
	}
	return id, nil
}

func lanIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "windows" {
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			return comspec
		}
		return "cmd.exe"
	}
	return "/bin/sh"
}
