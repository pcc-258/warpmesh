package server

import (
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

const (
	agentPongWait   = 70 * time.Second
	agentPingPeriod = 30 * time.Second
)

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()

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
	if !s.authorizeAgent(hello.DeviceID, token) {
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, Error: "invalid device credential"})
		_ = s.reg.RecordAudit(hello.DeviceID, "agent.auth-failed", hello.DeviceID, "invalid device credential")
		return
	}

	ips := hello.LANIPs
	if len(ips) == 0 {
		ips = localIPs()
	}
	if _, err := s.reg.Upsert(hello.DeviceID, hello.Name, hello.Hostname, hello.OS, hello.Arch, ips); err != nil {
		s.logf("registry upsert failed for %s: %v", hello.DeviceID, err)
	}
	_ = s.reg.RecordAudit(hello.DeviceID, "agent.online", hello.DeviceID, hello.OS+"/"+hello.Arch)

	conn := &agentConn{deviceID: hello.DeviceID, ws: ws, directPort: hello.DirectPort}
	if host, _, err := net.SplitHostPort(ws.RemoteAddr().String()); err == nil {
		conn.publicIP = host
	}
	s.agentsMu.Lock()
	old := s.agents[hello.DeviceID]
	s.agents[hello.DeviceID] = conn
	s.agentsMu.Unlock()
	if old != nil {
		_ = old.ws.Close()
	}

	s.logf("agent online: %s (%s@%s)", hello.DeviceID, hello.OS, hello.Arch)

	_ = ws.SetReadDeadline(time.Now().Add(agentPongWait))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(agentPongWait))
	})
	pingTicker := time.NewTicker(agentPingPeriod)
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
		_ = s.reg.SetOnline(hello.DeviceID, true)
		s.routeAgentMessage(hello.DeviceID, msg)
	}
	close(done)
	s.removeAgent(hello.DeviceID)
	s.closeAgentForwards(hello.DeviceID)
	s.closeAgentScreens(hello.DeviceID)
	_ = s.reg.RecordAudit(hello.DeviceID, "agent.offline", hello.DeviceID, "")
	s.logf("agent offline: %s", hello.DeviceID)
}

func (s *Server) routeAgentMessage(deviceID string, msg protocol.Message) {
	switch msg.Type {
	case protocol.TypePong:
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
				_ = manager.browser.WriteJSON(msg)
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
			_ = sess.browser.WriteMessage(websocket.BinaryMessage, raw)
			return
		}
		_ = sess.browser.WriteJSON(msg)
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
			_ = manager.browser.WriteJSON(msg)
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
			s.logf("agent rtc %s session=%s dropped: session already on relay", msg.Type, msg.SessionID)
		}
		s.sessionsMu.Unlock()
		return
	}
	s.logf("agent rtc %s session=%s service=%s forwarded to browser", msg.Type, msg.SessionID, msg.Service)
	if msg.Type == protocol.TypeRTCDirectOK && connection != "" {
		_ = s.reg.SetConnectionTransport(connection, "direct")
	}
	_ = browser.write(msg)
	s.sessionsMu.Unlock()
}

// localIPs returns non-loopback interface addresses for agent metadata.
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
