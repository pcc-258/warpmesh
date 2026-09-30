package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleFileWS(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizeWS(w, r, "files")
	if !ok {
		return
	}
	deviceID := r.URL.Query().Get("device")
	op := r.URL.Query().Get("op")
	if op != "upload" && op != "download" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "op (upload|download) is required"})
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
	// Track the handler so Close() can drain its teardown before the store is
	// closed; httptest does not wait for hijacked connections.
	s.wsWG.Add(1)
	defer s.wsWG.Done()

	sessionID := newID()
	metricID, metricErr := s.reg.StartConnection(actor, deviceID, "", "files-"+op, "relay", clientIP(r))
	if metricErr != nil {
		s.logf("record file connection: %v", metricErr)
	}
	var traffic *trafficRecorder
	if metricErr == nil {
		traffic = newTrafficRecorder(s.reg, metricID)
	}
	endState := "closed"
	defer func() { traffic.Close(endState) }()
	session := &fileSession{deviceID: deviceID, browser: ws, op: op, traffic: traffic}
	s.sessionsMu.Lock()
	s.files[sessionID] = session
	s.sessionsMu.Unlock()
	hardenBrowserConn(ws)
	pingBrowserConn(ws, &session.writeMu)
	startedAt := time.Now()
	s.logEvent("session.start", "kind", "file:"+op, "session", sessionID, "actor", actor, "device", deviceID, "ip", clientIP(r))
	defer func() {
		s.sessionsMu.Lock()
		delete(s.files, sessionID)
		s.sessionsMu.Unlock()
		s.logEvent("session.end",
			"kind", "file:"+op,
			"session", sessionID,
			"actor", actor,
			"device", deviceID,
			"state", endState,
			"dur", time.Since(startedAt).Round(time.Millisecond).String(),
		)
	}()

	// Validate before the session is registered: once it is reachable through
	// s.files, the agent router may write to this socket too, and concurrent
	// writes on one websocket are not allowed.
	name := sanitizeName(r.URL.Query().Get("name"))
	path := r.URL.Query().Get("path")
	switch op {
	case "upload":
		if name == "" {
			_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "missing file name"})
			return
		}
	case "download":
		if path == "" {
			_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "missing path"})
			return
		}
	}

	switch op {
	case "upload":
		_ = s.reg.RecordAudit(actor, "file.upload", deviceID, name)
		if err := agent.write(protocol.Message{
			Type:      protocol.TypeFileUpload,
			SessionID: sessionID,
			Name:      name,
			Size:      queryInt64(r, "size", 0),
			Path:      path,
		}); err != nil {
			return
		}
		for {
			kind, raw, err := ws.ReadMessage()
			if err != nil {
				_ = agent.write(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "upload aborted"})
				return
			}
			if kind == websocket.TextMessage {
				var msg protocol.Message
				if err := json.Unmarshal(raw, &msg); err != nil {
					continue
				}
				msg.SessionID = sessionID
				if msg.Type == protocol.TypeFileUploadEnd || msg.Type == protocol.TypeFileError {
					_ = agent.write(msg)
					if msg.Type == protocol.TypeFileError {
						endState = "failed"
					}
					return
				}
				continue
			}
			if err := agent.write(protocol.Message{
				Type:      protocol.TypeFileChunk,
				SessionID: sessionID,
				Data:      protocol.EncodeData(raw),
			}); err != nil {
				endState = "failed"
				return
			}
			traffic.Add(int64(len(raw)), 0)
		}
	case "download":
		_ = s.reg.RecordAudit(actor, "file.download", deviceID, path)
		if err := agent.write(protocol.Message{
			Type:      protocol.TypeFileDownload,
			SessionID: sessionID,
			Path:      path,
		}); err != nil {
			return
		}
		for {
			var msg protocol.Message
			if err := ws.ReadJSON(&msg); err != nil {
				_ = agent.write(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "download aborted"})
				return
			}
			if msg.Type == protocol.TypeFileError {
				return
			}
		}
	}
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

func queryInt64(r *http.Request, key string, def int64) int64 {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
