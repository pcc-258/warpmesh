package server

import (
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/device-relay/internal/protocol"
)

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	if !s.validDevice(r.URL.Query().Get("token")) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// First message must be a hello carrying device identity.
	var hello protocol.Message
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != protocol.TypeHello || hello.DeviceID == "" {
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, Error: "expected hello message"})
		return
	}

	ips := hello.LANIPs
	if len(ips) == 0 {
		ips = localIPs()
	}
	dev, err := s.reg.Upsert(hello.DeviceID, hello.Name, hello.Hostname, hello.OS, hello.Arch, ips)
	if err != nil {
		s.logf("registry upsert failed for %s: %v", hello.DeviceID, err)
	}
	_ = dev

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
