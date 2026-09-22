package server

import (
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/device-relay/internal/protocol"
)

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// The first message must carry device identity so the server can check
	// the per-device credential instead of trusting a shared token alone.
	var hello protocol.Message
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != protocol.TypeHello || hello.DeviceID == "" {
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, Error: "expected hello message"})
		return
	}
	if !s.authorizeAgent(hello.DeviceID, r.URL.Query().Get("token")) {
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

	conn := &agentConn{deviceID: hello.DeviceID, ws: ws}
	s.agentsMu.Lock()
	old := s.agents[hello.DeviceID]
	s.agents[hello.DeviceID] = conn
	s.agentsMu.Unlock()
	if old != nil {
		_ = old.ws.Close()
	}

	s.logf("agent online: %s (%s@%s)", hello.DeviceID, hello.OS, hello.Arch)

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-pingTicker.C:
				if err := conn.write(protocol.Message{Type: protocol.TypePing}); err != nil {
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
		s.sessionsMu.RUnlock()
		if sess == nil || sess.deviceID != deviceID {
			return
		}
		if err := sess.browser.WriteJSON(msg); err != nil {
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
		if sess == nil || sess.deviceID != deviceID {
			return
		}
		if msg.Type == protocol.TypeFileChunk {
			raw, err := protocol.DecodeData(msg.Data)
			if err != nil {
				return
			}
			_ = sess.browser.WriteMessage(websocket.BinaryMessage, raw)
			return
		}
		_ = sess.browser.WriteJSON(msg)
		if msg.Type == protocol.TypeFileDone || msg.Type == protocol.TypeFileError {
			s.sessionsMu.Lock()
			delete(s.files, msg.SessionID)
			s.sessionsMu.Unlock()
			_ = sess.browser.Close()
		}
	}
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
