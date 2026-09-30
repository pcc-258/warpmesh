package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizeWS(w, r, "terminal")
	if !ok {
		return
	}
	deviceID := r.URL.Query().Get("device")
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
	hardenBrowserConn(ws)
	pingBrowserConn(ws, &sess.writeMu)
	_ = s.reg.RecordAudit(actor, "terminal.start", deviceID, "")
	// endReason is read by the deferred log below, so it must be declared before
	// that defer is registered.
	endReason := "closed"
	s.logEvent("session.start", "kind", "terminal", "session", sessionID, "actor", actor, "device", deviceID, "ip", clientIP(r))
	defer traffic.Close("closed")
	startedAt := time.Now()
	defer func() {
		s.sessionsMu.Lock()
		delete(s.terms, sessionID)
		s.sessionsMu.Unlock()
		_ = agent.write(protocol.Message{Type: protocol.TypeRTCStop, SessionID: sessionID})
		s.logEvent("session.end",
			"kind", "terminal",
			"session", sessionID,
			"actor", actor,
			"device", deviceID,
			"dur", time.Since(startedAt).Round(time.Millisecond).String(),
			"reason", endReason,
		)
	}()

	cols, rows := queryInt(r, "cols", 120), queryInt(r, "rows", 30)
	if err := agent.write(protocol.Message{
		Type:      protocol.TypeTermStart,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}); err != nil {
		endReason = "device-write-failed"
		traffic.Close("failed")
		return
	}

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			endReason = "browser-disconnected"
			_ = agent.write(protocol.Message{Type: protocol.TypeTermStop, SessionID: sessionID})
			return
		}
		switch msg.Type {
		case protocol.TypeRTCOffer, protocol.TypeRTCICE:
			s.logDebug("rtc.signal", "direction", "browser", "type", msg.Type, "session", sessionID, "service", "terminal")
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
			s.logDebug("rtc.fallback", "session", sessionID, "service", "terminal")
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
				endReason = "device-write-failed"
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
				endReason = "stopped-by-client"
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
