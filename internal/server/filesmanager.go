package server

import (
	"net/http"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

// managerAllowedTypes is the set of message types the file manager console may
// send to an agent. Everything else is rejected, so a compromised or curious
// console session cannot reach the terminal, tunnel or upload paths through
// the manager channel.
var managerAllowedTypes = map[string]bool{
	protocol.TypeFileList:     true,
	protocol.TypeFileRoots:    true,
	protocol.TypeFileMkdir:    true,
	protocol.TypeFileDelete:   true,
	protocol.TypeFileRename:   true,
	protocol.TypeFileDownload: true,
}

func (s *Server) handleFilesWS(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizeWS(w, r, "files")
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
	hardenBrowserConn(ws)

	sessionID := newID()
	session := &managerSession{deviceID: deviceID, browser: ws}
	s.sessionsMu.Lock()
	s.managers[sessionID] = session
	s.sessionsMu.Unlock()
	pingBrowserConn(ws, &session.writeMu)
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
		if !managerAllowedTypes[msg.Type] {
			// The console opens one manager socket per operation, so a
			// connection-level lifecycle log would be pure noise. A rejected
			// message is different: it is a security event worth recording.
			s.logEvent("filemanager.rejected",
				"session", sessionID, "actor", actor, "device", deviceID, "type", msg.Type)
			_ = session.write(protocol.Message{
				Type:      protocol.TypeFileError,
				SessionID: sessionID,
				Error:     "operation not allowed",
			})
			continue
		}
		msg.SessionID = sessionID
		if err := agent.write(msg); err != nil {
			return
		}
	}
}
