// Package agent implements the outbound device agent.
package agent

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
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

	"github.com/pcc-258/device-relay/internal/protocol"
)

// Config configures a device agent.
type Config struct {
	ServerURL string
	Token     string
	DeviceID  string
	Name      string
	Shell     string
	DataDir   string
	Insecure  bool
	AllowPaths []string
}

// Agent maintains one control connection to the relay server.
type Agent struct {
	cfg      Config
	sessions map[string]*TermSession
	uploads  map[string]*os.File
	mu       sync.Mutex
	ws       *websocket.Conn
	writeMu  sync.Mutex
}

// New creates an agent and ensures a stable device identity.
func New(cfg Config) (*Agent, error) {
	if cfg.DataDir != "" {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, "uploads"), 0o755); err != nil {
			return nil, err
		}
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
	return &Agent{
		cfg:      cfg,
		sessions: make(map[string]*TermSession),
		uploads:  make(map[string]*os.File),
	}, nil
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
	q := u.Query()
	q.Set("token", a.cfg.Token)
	u.RawQuery = q.Encode()

	dialer := websocket.DefaultDialer
	if a.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	ws, _, err := dialer.Dial(u.String(), http.Header{})
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
		a.mu.Lock()
		session := a.sessions[msg.SessionID]
		a.mu.Unlock()
		if session == nil {
			return nil
		}
		return session.Input(msg.Data)
	case protocol.TypeTermResize:
		a.mu.Lock()
		session := a.sessions[msg.SessionID]
		a.mu.Unlock()
		if session == nil {
			return nil
		}
		return session.Resize(msg.Cols, msg.Rows)
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
	}
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
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.ws.WriteJSON(v)
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
