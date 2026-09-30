// Package agent implements the outbound device agent.
package agent

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
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
	cfg               Config
	sessions          map[string]*TermSession
	uploads           map[string]*os.File
	forwards          map[string]*forwardConn
	forwardPending    map[string]chan protocol.Message
	directPending     map[string]chan struct{}
	peerConns         map[string]*webrtc.PeerConnection
	dataChannels      map[string]*webrtc.DataChannel
	browserChannels   map[string]*webrtc.DataChannel
	browserDirect     map[string]bool
	pendingCandidates map[string][]webrtc.ICECandidateInit
	localConns        map[string]net.Conn
	pendingData       map[string][][]byte
	mu                sync.Mutex
	ws                *websocket.Conn
	writeMu           sync.Mutex
	forwardListeners  map[string]*forwardListener
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
		cfg.STUNServers = []string{
			"stun:stun.l.google.com:19302",
			"stun:stun.miwifi.com:3478",
			"stun:stun.chat.bilibili.com:3478",
		}
	}
	if cfg.VNCPort == 0 {
		cfg.VNCPort = 5900
	}
	a := &Agent{
		cfg:               cfg,
		sessions:          make(map[string]*TermSession),
		uploads:           make(map[string]*os.File),
		forwards:          make(map[string]*forwardConn),
		forwardPending:    make(map[string]chan protocol.Message),
		directPending:     make(map[string]chan struct{}),
		peerConns:         make(map[string]*webrtc.PeerConnection),
		dataChannels:      make(map[string]*webrtc.DataChannel),
		browserChannels:   make(map[string]*webrtc.DataChannel),
		browserDirect:     make(map[string]bool),
		pendingCandidates: make(map[string][]webrtc.ICECandidateInit),
		localConns:        make(map[string]net.Conn),
		pendingData:       make(map[string][][]byte),
		forwardListeners:  make(map[string]*forwardListener),
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
		started := time.Now()
		err := a.connectOnce()
		if err != nil {
			// Report the next retry delay so a log reader can tell the
			// difference between a flapping link and an unreachable server.
			log.Printf("[agent] disconnected from %s after %s, retrying in %s: %v",
				a.cfg.ServerURL, time.Since(started).Round(time.Second), (backoff + backoff/5).Round(time.Second), err)
		}
		// A connection that survived this long means the network recovered;
		// restart the backoff so reconnects stay fast.
		if time.Since(started) > 60*time.Second {
			backoff = time.Second
		}
		time.Sleep(backoff + backoff/5)
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
			// Name the message: without the type and session this line is
			// impossible to attribute when several sessions run at once.
			log.Printf("[agent] handle %s session=%s: %v", msg.Type, msg.SessionID, err)
		}
	}
}

// debugRTC enables the per-candidate ICE diagnostics, which are essential when
// a direct path fails to establish and pure noise otherwise. Opt in with
// DEVICE_RELAY_DEBUG_RTC=1. State transitions that an operator needs to see
// (channel open, ICE failure) stay at the default level.
var debugRTC = os.Getenv("DEVICE_RELAY_DEBUG_RTC") == "1"

// logDebug records a diagnostic line only when debugging is enabled.
func logDebug(format string, args ...any) {
	if !debugRTC {
		return
	}
	log.Printf("[agent] "+format, args...)
}

// safeGo runs fn in its own goroutine and converts a panic into a logged error
// instead of letting it terminate the whole agent process.
func safeGo(what string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[agent] panic in %s: %v\n%s", what, r, debug.Stack())
			}
		}()
		fn()
	}()
}

func (a *Agent) handleMessage(msg protocol.Message) (err error) {
	// A malformed or hostile frame must not be able to crash the agent: that
	// would drop every session on this device. Convert a panic into a logged
	// error and keep the control connection alive.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] panic handling %s: %v\n%s", msg.Type, r, debug.Stack())
			err = fmt.Errorf("panic handling %s: %v", msg.Type, r)
		}
	}()
	switch msg.Type {
	case protocol.TypePing:
		return a.send(protocol.Message{Type: protocol.TypePong})
	case protocol.TypeTermStart:
		session, err := startTerminalSession(func(m protocol.Message) error {
			if m.Type == protocol.TypeTermOutput {
				raw, decodeErr := protocol.DecodeData(m.Data)
				if decodeErr != nil {
					return decodeErr
				}
				a.sendTerminalOutput(msg.SessionID, raw)
				return nil
			}
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
		dir := filepath.Join(a.cfg.DataDir, "uploads")
		if msg.Path != "" {
			dir = msg.Path
		}
		if !a.pathAllowed(dir) {
			return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is outside the allowed directories"})
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
		}
		f, err := os.Create(filepath.Join(dir, name))
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
		// Stream in the background so a large transfer cannot block the control
		// loop, which must keep answering server pings.
		safeGo("file download", func() {
			if err := a.streamFile(msg); err != nil {
				log.Printf("[agent] stream %s: %v", msg.SessionID, err)
			}
		})
		return nil
	case protocol.TypeFileList:
		return a.handleFileList(msg)
	case protocol.TypeFileMkdir:
		return a.handleFileMkdir(msg)
	case protocol.TypeFileDelete:
		return a.handleFileDelete(msg)
	case protocol.TypeFileRename:
		return a.handleFileRename(msg)
	case protocol.TypeFileRoots:
		return a.handleFileRoots(msg)
	case protocol.TypeForwardConnect:
		return a.handleForwardTarget(msg)
	case protocol.TypeForwardOffer:
		safeGo("forward offer", func() { a.handleDirectOffer(msg) })
		return nil
	case protocol.TypeForwardAnswer:
		a.handleDirectAnswer(msg)
		return nil
	case protocol.TypeForwardICE:
		a.handleDirectICE(msg)
		return nil
	case protocol.TypeRTCOffer:
		safeGo("browser offer", func() { a.handleBrowserOffer(msg) })
		return nil
	case protocol.TypeRTCRelay:
		a.closeBrowserDirect(msg.SessionID)
		return nil
	case protocol.TypeRTCReady:
		a.startBrowserDesktop(msg.SessionID)
		return nil
	case protocol.TypeRTCStop:
		a.closeBrowserDirect(msg.SessionID)
		return nil
	case protocol.TypeRTCICE:
		a.handleBrowserICE(msg)
		return nil
	case protocol.TypeForwardDirectOK:
		return nil
	case protocol.TypeScreenStart:
		safeGo("screen link", func() { a.startScreenLink(msg.SessionID) })
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
	// Stream instead of buffering the whole file: a large download used to be
	// read into memory and pushed from the control loop, which both risked OOM
	// and starved the read deadline until the server dropped the connection.
	f, err := os.Open(msg.Path)
	if err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	defer func() { _ = f.Close() }()

	if info, err := f.Stat(); err == nil && info.IsDir() {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is a directory"})
	}

	const chunkSize = 64 * 1024
	buf := make([]byte, chunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if err := a.send(protocol.Message{
				Type:      protocol.TypeFileChunk,
				SessionID: msg.SessionID,
				Data:      protocol.EncodeData(buf[:n]),
			}); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: readErr.Error()})
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
	// Resolve symlinks before comparing. A purely lexical prefix check can be
	// escaped by a symlink planted inside an allowed root that points outside
	// it, which would expose the whole filesystem.
	resolved := resolvePath(abs)
	for _, root := range a.cfg.AllowPaths {
		r, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		realRoot := resolvePath(r)
		if resolved == realRoot || strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolvePath returns the real filesystem path for abs, resolving symlinks for
// the longest existing prefix. Paths that do not exist yet (an upload target,
// for example) are resolved through their nearest existing ancestor so that a
// symlinked parent directory cannot be used to escape an allowed root.
func resolvePath(abs string) string {
	current := abs
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if len(suffix) == 0 {
				return resolved
			}
			parts := append([]string{resolved}, suffix...)
			return filepath.Join(parts...)
		} else if !errors.Is(err, fs.ErrNotExist) {
			// Permission or IO trouble: fail closed for this candidate.
			return abs
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func (a *Agent) handleFileList(msg protocol.Message) error {
	if !a.pathAllowed(msg.Path) {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is outside the allowed directories"})
	}
	entries, err := a.listDir(msg.Path)
	if err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	return a.send(protocol.Message{Type: protocol.TypeFileListResult, SessionID: msg.SessionID, Path: msg.Path, Entries: entries})
}

func (a *Agent) listDir(path string) ([]protocol.FileEntry, error) {
	raw, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.FileEntry, 0, len(raw))
	for _, entry := range raw {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, protocol.FileEntry{
			Name:    entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (a *Agent) handleFileMkdir(msg protocol.Message) error {
	if !a.pathAllowed(msg.Path) {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is outside the allowed directories"})
	}
	if err := os.MkdirAll(msg.Path, 0o755); err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	return a.send(protocol.Message{Type: protocol.TypeFileDone, SessionID: msg.SessionID})
}

func (a *Agent) handleFileDelete(msg protocol.Message) error {
	if !a.pathAllowed(msg.Path) {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is outside the allowed directories"})
	}
	if err := os.RemoveAll(msg.Path); err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	return a.send(protocol.Message{Type: protocol.TypeFileDone, SessionID: msg.SessionID})
}

func (a *Agent) handleFileRename(msg protocol.Message) error {
	if !a.pathAllowed(msg.Path) || !a.pathAllowed(msg.Target) {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: "path is outside the allowed directories"})
	}
	if err := os.Rename(msg.Path, msg.Target); err != nil {
		return a.send(protocol.Message{Type: protocol.TypeFileError, SessionID: msg.SessionID, Error: err.Error()})
	}
	return a.send(protocol.Message{Type: protocol.TypeFileDone, SessionID: msg.SessionID})
}

func (a *Agent) handleFileRoots(msg protocol.Message) error {
	roots := make([]protocol.FileEntry, 0, len(a.cfg.AllowPaths))
	for _, root := range a.cfg.AllowPaths {
		roots = append(roots, protocol.FileEntry{Name: root, Path: root, IsDir: true})
	}
	return a.send(protocol.Message{Type: protocol.TypeFileRootsResult, SessionID: msg.SessionID, Entries: roots})
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
	sessions := a.sessions
	uploads := a.uploads
	forwards := a.forwards
	peerConns := a.peerConns
	dataChannels := a.dataChannels
	browserChannels := a.browserChannels
	localConns := a.localConns
	a.sessions = make(map[string]*TermSession)
	a.uploads = make(map[string]*os.File)
	a.forwards = make(map[string]*forwardConn)
	a.peerConns = make(map[string]*webrtc.PeerConnection)
	a.dataChannels = make(map[string]*webrtc.DataChannel)
	a.browserChannels = make(map[string]*webrtc.DataChannel)
	a.browserDirect = make(map[string]bool)
	a.pendingCandidates = make(map[string][]webrtc.ICECandidateInit)
	a.localConns = make(map[string]net.Conn)
	clear(a.pendingData)
	clear(a.forwardPending)
	clear(a.directPending)
	a.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
	for _, f := range uploads {
		_ = f.Close()
	}
	for _, fc := range forwards {
		_ = fc.Close()
	}
	for _, pc := range peerConns {
		_ = pc.Close()
	}
	for _, dc := range dataChannels {
		_ = dc.Close()
	}
	for _, dc := range browserChannels {
		_ = dc.Close()
	}
	for _, conn := range localConns {
		_ = conn.Close()
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
