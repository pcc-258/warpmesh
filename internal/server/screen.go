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
	agent := s.getAgent(deviceID)
	if agent == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device offline"})
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	sessionID := newID()
	sess := &screenSession{
		sessionID: sessionID,
		deviceID:  deviceID,
		browser:   ws,
		linkReady: make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.sessionsMu.Lock()
	s.screens[sessionID] = sess
	s.sessionsMu.Unlock()
	defer func() {
		s.sessionsMu.Lock()
		delete(s.screens, sessionID)
		s.sessionsMu.Unlock()
	}()

	_ = s.reg.RecordAudit(actor, "screen.start", deviceID, "")
	if err := agent.write(protocol.Message{Type: protocol.TypeScreenStart, SessionID: sessionID}); err != nil {
		return
	}

	select {
	case <-sess.linkReady:
	case <-time.After(10 * time.Second):
		_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeScreenError, SessionID: sessionID, Error: "screen link timeout"})
		return
	}

	relayScreen(ws, sess.link)
	close(sess.done)
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
	defer ws.Close()

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

func relayScreen(a, b *websocket.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(src, dst *websocket.Conn) {
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				break
			}
			if err := dst.WriteMessage(mt, data); err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
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
