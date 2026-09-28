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
	metricID, metricErr := s.reg.StartConnection(actor, deviceID, "", "terminal", "negotiating", clientIP(r))
	if metricErr != nil {
		s.logf("record terminal connection: %v", metricErr)
	}
	var traffic *trafficRecorder
	if metricErr == nil {
		traffic = newTrafficRecorder(s.reg, metricID)
	}
	sess := &termSession{deviceID: deviceID, browser: ws, traffic: traffic, connection: metricID, transport: "negotiating"}
	s.sessionsMu.Lock()
	s.terms[sessionID] = sess
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(actor, "terminal.start", deviceID, "")
	defer traffic.Close("closed")
	defer func() {
		s.sessionsMu.Lock()
		delete(s.terms, sessionID)
		s.sessionsMu.Unlock()
		_ = agent.write(protocol.Message{Type: protocol.TypeRTCStop, SessionID: sessionID})
	}()

	cols, rows := queryInt(r, "cols", 120), queryInt(r, "rows", 30)
	if err := agent.write(protocol.Message{
		Type:      protocol.TypeTermStart,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}); err != nil {
		traffic.Close("failed")
		return
	}

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			_ = agent.write(protocol.Message{Type: protocol.TypeTermStop, SessionID: sessionID})
			return
		}
		switch msg.Type {
		case protocol.TypeRTCOffer, protocol.TypeRTCICE:
			s.logf("browser rtc %s session=%s service=terminal", msg.Type, sessionID)
			s.sessionsMu.RLock()
			fallback := sess.transport == "relay"
			s.sessionsMu.RUnlock()
			if fallback {
				continue
			}
			msg.SessionID = sessionID
			msg.Service = "terminal"
			if err := agent.write(msg); err != nil {
				return
			}
		case protocol.TypeRTCFallback:
			s.logf("browser rtc fallback session=%s service=terminal", sessionID)
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
			_ = agent.write(protocol.Message{Type: protocol.TypeRTCRelay, SessionID: sessionID})
			_ = sess.write(protocol.Message{Type: protocol.TypeRTCRelay, SessionID: sessionID})
		case protocol.TypeTermInput, protocol.TypeTermResize, protocol.TypeTermStop:
			msg.SessionID = sessionID
			if err := agent.write(msg); err != nil {
				traffic.Close("failed")
				return
			}
			s.sessionsMu.RLock()
			relay := sess.transport == "relay"
			s.sessionsMu.RUnlock()
			if msg.Type == protocol.TypeTermInput && relay {
				traffic.Add(int64(len(msg.Data)), 0)
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
