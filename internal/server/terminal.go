package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
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
	defer func() { _ = ws.Close() }()

	sessionID := newID()
	sess := &termSession{deviceID: deviceID, browser: ws}
	s.sessionsMu.Lock()
	s.terms[sessionID] = sess
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(actor, "terminal.start", deviceID, "")
	defer func() {
		s.sessionsMu.Lock()
		delete(s.terms, sessionID)
		s.sessionsMu.Unlock()
	}()

	cols, rows := queryInt(r, "cols", 120), queryInt(r, "rows", 30)
	if err := agent.write(protocol.Message{
		Type:      protocol.TypeTermStart,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}); err != nil {
		return
	}

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			_ = agent.write(protocol.Message{Type: protocol.TypeTermStop, SessionID: sessionID})
			return
		}
		switch msg.Type {
		case protocol.TypeTermInput, protocol.TypeTermResize, protocol.TypeTermStop:
			msg.SessionID = sessionID
			if err := agent.write(msg); err != nil {
				return
			}
			if msg.Type == protocol.TypeTermStop {
				return
			}
		}
	}
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
