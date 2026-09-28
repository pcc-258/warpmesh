package server

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleScreenWS(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device"})
		return
	}
	if !s.canAccessDevice(actor, deviceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "device not granted"})
		return
	}
	agent := s.getAgent(deviceID)
	if agent == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device offline"})
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()

	sessionID := newID()
	metricID, metricErr := s.reg.StartConnection(actor, deviceID, "", "desktop", "negotiating", clientIP(r))
	if metricErr != nil {
		s.logf("record desktop connection: %v", metricErr)
	}
	var traffic *trafficRecorder
	if metricErr == nil {
		traffic = newTrafficRecorder(s.reg, metricID)
	}
	endState := "closed"
	defer func() { traffic.Close(endState) }()
	sess := &screenSession{
		sessionID:  sessionID,
		deviceID:   deviceID,
		browser:    ws,
		linkReady:  make(chan struct{}),
		done:       make(chan struct{}),
		traffic:    traffic,
		connection: metricID,
		transport:  "negotiating",
	}
	s.sessionsMu.Lock()
	s.screens[sessionID] = sess
	s.sessionsMu.Unlock()
	defer func() {
		s.sessionsMu.Lock()
		delete(s.screens, sessionID)
		s.sessionsMu.Unlock()
		close(sess.done)
		_ = agent.write(protocol.Message{Type: protocol.TypeRTCStop, SessionID: sessionID})
	}()

	_ = s.reg.RecordAudit(actor, "screen.start", deviceID, "")
	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case protocol.TypeRTCOffer, protocol.TypeRTCICE:
			s.logf("browser rtc %s session=%s service=desktop", msg.Type, sessionID)
			s.sessionsMu.RLock()
			fallback := sess.transport == "relay"
			s.sessionsMu.RUnlock()
			if fallback {
				continue
			}
			msg.SessionID = sessionID
			msg.Service = "desktop"
			if err := agent.write(msg); err != nil {
				endState = "failed"
				return
			}
		case protocol.TypeRTCReady:
			if err := agent.write(protocol.Message{Type: protocol.TypeRTCReady, SessionID: sessionID}); err != nil {
				endState = "failed"
				return
			}
		case protocol.TypeRTCFallback:
			s.logf("browser rtc fallback session=%s service=desktop", sessionID)
			s.sessionsMu.Lock()
			alreadyRelay := sess.transport == "relay"
			sess.transport = "relay"
			sess.direct = false
			s.sessionsMu.Unlock()
			if alreadyRelay {
				continue
			}
			if sess.traffic != nil {
				_ = s.reg.SetConnectionTransport(sess.connection, "relay")
			}
			if err := agent.write(protocol.Message{Type: protocol.TypeRTCRelay, SessionID: sessionID}); err != nil {
				endState = "failed"
				return
			}
			if err := agent.write(protocol.Message{Type: protocol.TypeScreenStart, SessionID: sessionID}); err != nil {
				endState = "failed"
				return
			}
			select {
			case <-sess.linkReady:
			case <-time.After(10 * time.Second):
				_ = sess.write(protocol.Message{Type: protocol.TypeScreenError, SessionID: sessionID, Error: "screen relay link timeout"})
				endState = "failed"
				return
			}
			_ = sess.write(protocol.Message{Type: protocol.TypeRTCRelay, SessionID: sessionID})
			relayScreen(ws, sess.link, traffic)
			return
		case protocol.TypeRTCStop:
			return
		}
	}
}

func (s *Server) handleScreenLinkWS(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if !s.authorizeAgent(r.URL.Query().Get("device"), token) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	deviceID := r.URL.Query().Get("device")
	sessionID := r.URL.Query().Get("session")
	if deviceID == "" || sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device and session are required"})
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()

	s.sessionsMu.Lock()
	sess := s.screens[sessionID]
	if sess == nil || sess.deviceID != deviceID {
		s.sessionsMu.Unlock()
		return
	}
	if sess.linkSet {
		s.sessionsMu.Unlock()
		return
	}
	sess.link = ws
	sess.linkSet = true
	s.sessionsMu.Unlock()
	close(sess.linkReady)

	<-sess.done
}

func relayScreen(a, b *websocket.Conn, traffic *trafficRecorder) {
	done := make(chan struct{}, 2)
	copyOne := func(src, dst *websocket.Conn, fromBrowser bool) {
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				break
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				break
			}
			if fromBrowser {
				traffic.Add(int64(len(data)), 0)
			} else {
				traffic.Add(0, int64(len(data)))
			}
		}
		done <- struct{}{}
	}
	go copyOne(a, b, true)
	go copyOne(b, a, false)
	<-done
	<-done
	_ = a.Close()
	_ = b.Close()
}

// closeAgentScreens terminates desktop sessions when an agent goes offline.
func (s *Server) closeAgentScreens(deviceID string) {
	s.sessionsMu.Lock()
	var browsers []*websocket.Conn
	for id, sess := range s.screens {
		if sess.deviceID == deviceID {
			browsers = append(browsers, sess.browser)
			delete(s.screens, id)
		}
	}
	s.sessionsMu.Unlock()
	for _, ws := range browsers {
		_ = ws.Close()
	}
}
