package server

import (
	"net/http"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleFilesWS(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticate(r)
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
	defer func() { _ = ws.Close() }()

	sessionID := newID()
	s.sessionsMu.Lock()
	s.managers[sessionID] = &managerSession{deviceID: deviceID, browser: ws}
	s.sessionsMu.Unlock()
	defer func() {
		s.sessionsMu.Lock()
		delete(s.managers, sessionID)
		s.sessionsMu.Unlock()
	}()

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			return
		}
		msg.SessionID = sessionID
		if err := agent.write(msg); err != nil {
			return
		}
	}
}
