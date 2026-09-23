package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (s *Server) handleFileWS(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	deviceID := r.URL.Query().Get("device")
	op := r.URL.Query().Get("op")
	if deviceID == "" || (op != "upload" && op != "download") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device and op (upload|download) are required"})
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
	s.sessionsMu.Lock()
	s.files[sessionID] = &fileSession{deviceID: deviceID, browser: ws, op: op}
	s.sessionsMu.Unlock()
	defer func() {
		s.sessionsMu.Lock()
		delete(s.files, sessionID)
		s.sessionsMu.Unlock()
	}()

	switch op {
	case "upload":
		name := sanitizeName(r.URL.Query().Get("name"))
		if name == "" {
			_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "missing file name"})
			return
		}
		_ = s.reg.RecordAudit(actor, "file.upload", deviceID, name)
		if err := agent.write(protocol.Message{
			Type:      protocol.TypeFileUpload,
			SessionID: sessionID,
			Name:      name,
			Size:      queryInt64(r, "size", 0),
			Path:      r.URL.Query().Get("path"),
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
					return
				}
				continue
			}
			if err := agent.write(protocol.Message{
				Type:      protocol.TypeFileChunk,
				SessionID: sessionID,
				Data:      protocol.EncodeData(raw),
			}); err != nil {
				return
			}
		}
	case "download":
		path := r.URL.Query().Get("path")
		if path == "" {
			_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeFileError, SessionID: sessionID, Error: "missing path"})
			return
		}
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
