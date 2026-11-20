package server

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

const (
	nodePongWait   = 70 * time.Second
	nodePingPeriod = 30 * time.Second
)

// maxRelayMessageBytes bounds a single WebSocket frame. File chunks are the
// largest legitimate payload, so 4 MiB leaves generous headroom while denying
// an unbounded allocation to any peer. Comparable projects settle in the same
// range (Coder uses 4 MiB; chisel caps pre-auth frames at 512 KiB).
const maxRelayMessageBytes = 4 << 20

// hardenBrowserConn applies the read limit and keepalive policy shared by every
// browser-facing WebSocket.
//
// The protocol-level deadline is deliberately much longer than the ping period:
// browsers answer pings with pongs, but a busy page (for example one uploading a
// file) may not deliver a pong for a while, and a short deadline would evict
// healthy sessions.
func hardenBrowserConn(ws *websocket.Conn) {
	ws.SetReadLimit(maxRelayMessageBytes)
	_ = ws.SetReadDeadline(time.Now().Add(browserPongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(browserPongWait))
	})
}

// pingBrowserConn keeps a browser WebSocket warm and lets the read deadline
// reap a connection whose peer vanished without a close handshake. It stops
// with the connection and never blocks shutdown.
func pingBrowserConn(ws *websocket.Conn, writeMu *sync.Mutex) {
	go func() {
		ticker := time.NewTicker(browserPingPeriod)
		defer ticker.Stop()
		for range ticker.C {
			writeMu.Lock()
			err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()
}

func (s *Server) handleNodeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()
	// Close() waits on this so registry writes in the teardown below cannot
	// outlive the database handle.
	s.wsWG.Add(1)
	defer s.wsWG.Done()
	ws.SetReadLimit(maxRelayMessageBytes)

	// The first message must carry device identity so the server can check
	// the per-device credential instead of trusting a shared token alone.
	var hello protocol.Message
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != protocol.TypeHello || hello.DeviceID == "" {
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, Error: "expected hello message"})
		return
	}
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if !s.authorizeNode(hello.DeviceID, token) {
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, Error: "invalid device credential"})
		_ = s.reg.RecordAudit(hello.DeviceID, "node.auth-failed", hello.DeviceID, "invalid device credential")
		ip := ""
		if host, _, err := net.SplitHostPort(ws.RemoteAddr().String()); err == nil {
			ip = host
		}
		s.logEvent("auth.node.failed", "device", hello.DeviceID, "ip", ip)
		return
	}

	ips := hello.LANIPs
	if len(ips) == 0 {
		ips = localIPs()
	}
	deviceBefore, _ := s.reg.Get(hello.DeviceID)
	if hello.UpdateService != "" {
		_ = s.reg.SetUpdateService(hello.DeviceID, hello.UpdateService)
	}
	device, err := s.reg.Upsert(hello.DeviceID, hello.Name, hello.Hostname, hello.OS, hello.Arch, ips, hello.Version)
	if err != nil {
		s.logf("registry upsert failed for %s: %v", hello.DeviceID, err)
	}
	if device != nil && hello.Version != "" && deviceBefore.UpdateVersion == hello.Version &&
		(deviceBefore.UpdateState == "restarting" || deviceBefore.UpdateState == "installing" || deviceBefore.UpdateState == "staged") {
		_ = s.reg.SetUpdateState(hello.DeviceID, "succeeded", hello.Version, "", deviceBefore.UpdateService)
	}
	_ = s.reg.RecordAudit(hello.DeviceID, "node.online", hello.DeviceID, hello.OS+"/"+hello.Arch)

	// The registry is already online from the hello upsert above, so the first
	// throttled refresh is only due after nodeOnlineSyncInterval.
	conn := &nodeConn{deviceID: hello.DeviceID, ws: ws, directPort: hello.DirectPort, lastSync: time.Now(), capabilities: hello.Capabilities}
	if host, _, err := net.SplitHostPort(ws.RemoteAddr().String()); err == nil {
		conn.publicIP = host
	}
	s.nodesMu.Lock()
	old := s.nodes[hello.DeviceID]
	s.nodes[hello.DeviceID] = conn
	s.nodesMu.Unlock()
	if old != nil {
		_ = old.ws.Close()
	}

	s.logf("node online: %s (%s@%s)", hello.DeviceID, hello.OS, hello.Arch)

	_ = ws.SetReadDeadline(time.Now().Add(nodePongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(nodePongWait))
	})
	pingTicker := time.NewTicker(nodePingPeriod)
	defer pingTicker.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-pingTicker.C:
				conn.writeMu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				conn.writeMu.Unlock()
				if err != nil {
					_ = ws.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			break
		}
		// Refresh the liveness deadline on every inbound frame, not only on
		// pongs: a device streaming heavy terminal or file output is alive.
		_ = ws.SetReadDeadline(time.Now().Add(nodePongWait))
		// Stop as soon as a newer connection for this device has taken over, so
		// the replaced connection cannot keep writing state on its behalf.
		if s.getNode(hello.DeviceID) != conn {
			s.logf("node connection replaced mid-stream: %s", hello.DeviceID)
			break
		}
		s.touchNode(hello.DeviceID)
		s.routeNodeMessage(hello.DeviceID, msg)
	}
	close(done)
	if s.removeNode(hello.DeviceID, conn) {
		// Only the connection that is actually current may tear down its live
		// sessions; a replaced connection must leave the new one untouched.
		s.closeNodeForwards(hello.DeviceID)
		s.closeNodeScreens(hello.DeviceID)
		_ = s.reg.RecordAudit(hello.DeviceID, "node.offline", hello.DeviceID, "")
		s.logf("node offline: %s", hello.DeviceID)
		return
	}
	s.logf("node connection replaced, keeping the newer one: %s", hello.DeviceID)
}

func (s *Server) routeNodeMessage(deviceID string, msg protocol.Message) {
	switch msg.Type {
	case protocol.TypeStatus:
		if conn := s.getNode(deviceID); conn != nil {
			conn.capMu.Lock()
			conn.capabilities = msg.Capabilities
			conn.capMu.Unlock()
		}
		if msg.Version != "" {
			_ = s.reg.SetVersion(deviceID, msg.Version)
		}
		if msg.UpdateService != "" {
			_ = s.reg.SetUpdateService(deviceID, msg.UpdateService)
		}
	case protocol.TypePong:
		return
	case protocol.TypeNodeUpdateState:
		_ = s.reg.SetUpdateState(deviceID, msg.UpdateState, msg.UpdateVersion, msg.UpdateError, msg.UpdateService)
		return
	case protocol.TypeTermOutput, protocol.TypeTermExit, protocol.TypeTermError:
		s.sessionsMu.RLock()
		sess := s.terms[msg.SessionID]
		relay := sess != nil && sess.transport == "relay"
		s.sessionsMu.RUnlock()
		if sess == nil || sess.deviceID != deviceID {
			return
		}
		if msg.Type == protocol.TypeTermOutput {
			if relay {
				if raw, err := protocol.DecodeData(msg.Data); err == nil {
					sess.traffic.Add(0, int64(len(raw)))
				}
			}
		}
		if msg.Type == protocol.TypeTermExit || msg.Type == protocol.TypeTermError {
			state := "closed"
			if msg.Type == protocol.TypeTermError {
				state = "failed"
			}
			sess.traffic.Close(state)
		}
		if err := sess.write(msg); err != nil {
			_ = sess.browser.Close()
		}
		if msg.Type == protocol.TypeTermExit || msg.Type == protocol.TypeTermError {
			s.sessionsMu.Lock()
			delete(s.terms, msg.SessionID)
			s.sessionsMu.Unlock()
			_ = sess.browser.Close()
		}
	case protocol.TypeFileChunk, protocol.TypeFileUploadEnd, protocol.TypeFileDone, protocol.TypeFileError:
		s.sessionsMu.RLock()
		sess := s.files[msg.SessionID]
		s.sessionsMu.RUnlock()
		if sess == nil {
			s.sessionsMu.RLock()
			manager := s.managers[msg.SessionID]
			s.sessionsMu.RUnlock()
			if manager != nil && manager.deviceID == deviceID {
				_ = manager.write(msg)
				if msg.Type == protocol.TypeFileDone || msg.Type == protocol.TypeFileError {
					s.sessionsMu.Lock()
					delete(s.managers, msg.SessionID)
					s.sessionsMu.Unlock()
					_ = manager.browser.Close()
				}
			}
			return
		}
		if sess == nil || sess.deviceID != deviceID {
			return
		}
		if msg.Type == protocol.TypeFileChunk {
			raw, err := protocol.DecodeData(msg.Data)
			if err != nil {
				return
			}
			sess.traffic.Add(0, int64(len(raw)))
			if err := sess.writeBinary(raw); err != nil {
				_ = sess.browser.Close()
			}
			return
		}
		if err := sess.write(msg); err != nil {
			_ = sess.browser.Close()
		}
		if msg.Type == protocol.TypeFileDone || msg.Type == protocol.TypeFileError {
			state := "closed"
			if msg.Type == protocol.TypeFileError {
				state = "failed"
			}
			sess.traffic.Close(state)
			s.sessionsMu.Lock()
			delete(s.files, msg.SessionID)
			s.sessionsMu.Unlock()
			_ = sess.browser.Close()
		}
	case protocol.TypeFileListResult, protocol.TypeFileRootsResult:
		s.sessionsMu.RLock()
		manager := s.managers[msg.SessionID]
		s.sessionsMu.RUnlock()
		if manager != nil && manager.deviceID == deviceID {
			_ = manager.write(msg)
		}
	case protocol.TypeForwardConnect:
		s.handleForwardConnect(deviceID, msg)
	case protocol.TypeForwardDirectOK:
		s.handleDirectOK(deviceID, msg)
	case protocol.TypeForwardOpen, protocol.TypeForwardData, protocol.TypeForwardClose, protocol.TypeForwardError,
		protocol.TypeForwardOffer, protocol.TypeForwardAnswer, protocol.TypeForwardICE:
		s.relayForward(deviceID, msg)
	case protocol.TypeRTCAnswer, protocol.TypeRTCICE, protocol.TypeRTCDirectOK, protocol.TypeRTCDirectError:
		s.routeBrowserRTC(deviceID, msg)
	case protocol.TypeScreenError:
		s.sessionsMu.RLock()
		sess := s.screens[msg.SessionID]
		if sess != nil && sess.deviceID == deviceID && sess.transport != "relay" {
			_ = sess.write(msg)
		}
		s.sessionsMu.RUnlock()
	}
}

func (s *Server) routeBrowserRTC(deviceID string, msg protocol.Message) {
	s.sessionsMu.Lock()
	var browser interface{ write(any) error }
	var connection string
	var relaying bool
	switch msg.Service {
	case "terminal":
		if sess := s.terms[msg.SessionID]; sess != nil && sess.deviceID == deviceID {
			relaying = sess.transport == "relay"
			if msg.Type == protocol.TypeRTCDirectOK && sess.transport != "relay" {
				sess.direct = true
				sess.transport = "direct"
			}
			browser = sess
			connection = sess.connection
		}
	case "desktop":
		if sess := s.screens[msg.SessionID]; sess != nil && sess.deviceID == deviceID {
			relaying = sess.transport == "relay"
			if msg.Type == protocol.TypeRTCDirectOK && sess.transport != "relay" {
				sess.direct = true
				sess.transport = "direct"
			}
			browser = sess
			connection = sess.connection
		}
	}
	if browser == nil || relaying {
		if relaying {
			s.logDebug("rtc.signal.dropped", "direction", "node", "type", msg.Type, "session", msg.SessionID)
		}
		s.sessionsMu.Unlock()
		return
	}
	s.logDebug("rtc.signal", "direction", "node", "type", msg.Type, "session", msg.SessionID, "service", msg.Service)
	if msg.Type == protocol.TypeRTCDirectOK && connection != "" {
		_ = s.reg.SetConnectionTransport(connection, "direct")
	}
	_ = browser.write(msg)
	s.sessionsMu.Unlock()
}

// localIPs returns non-loopback interface addresses for node metadata.
func localIPs() []string {
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
