package server

import (
	"strconv"
	"time"

	"github.com/pcc-258/pylon/internal/protocol"
)

const directAttemptTimeout = 5 * time.Second

func (s *Server) handleForwardConnect(sourceID string, msg protocol.Message) {
	if msg.Target == "" || msg.Port <= 0 || msg.SessionID == "" {
		_ = s.sendForwardError(sourceID, msg.SessionID, "invalid forward request")
		return
	}
	source := s.getAgent(sourceID)
	target := s.getAgent(msg.Target)
	if source == nil || target == nil {
		_ = s.sendForwardError(sourceID, msg.SessionID, "target device offline")
		return
	}

	sess := &forwardSession{source: sourceID, target: msg.Target}
	s.sessionsMu.Lock()
	s.forwards[msg.SessionID] = sess
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(sourceID, "forward.connect", msg.Target, strconv.Itoa(msg.Port))

	// Give WebRTC ICE time to establish a direct connection. If it does not
	// succeed in time, fall back to relaying through the server.
	sess.timer = time.AfterFunc(directAttemptTimeout, func() {
		s.sessionsMu.Lock()
		current := s.forwards[msg.SessionID]
		if current == nil || current.direct {
			s.sessionsMu.Unlock()
			return
		}
		s.sessionsMu.Unlock()
		_ = s.reg.RecordAudit(sourceID, "forward.relay", msg.Target, msg.SessionID)
		s.startRelayFallback(msg)
	})
}

func (s *Server) startRelayFallback(msg protocol.Message) {
	target := s.getAgent(msg.Target)
	if target == nil {
		return
	}
	_ = target.write(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: msg.SessionID,
		Port:      msg.Port,
	})
}

func (s *Server) handleDirectOK(deviceID string, msg protocol.Message) {
	s.sessionsMu.Lock()
	sess := s.forwards[msg.SessionID]
	if sess != nil && !sess.direct {
		sess.direct = true
		if sess.timer != nil {
			sess.timer.Stop()
		}
	}
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(deviceID, "forward.direct", msg.SessionID, "")
}

func (s *Server) relayForward(fromID string, msg protocol.Message) {
	s.sessionsMu.RLock()
	sess := s.forwards[msg.SessionID]
	s.sessionsMu.RUnlock()
	if sess == nil {
		return
	}
	var toID string
	switch fromID {
	case sess.source:
		toID = sess.target
	case sess.target:
		toID = sess.source
	default:
		return
	}
	agent := s.getAgent(toID)
	if agent == nil {
		s.closeForward(msg.SessionID)
		return
	}
	_ = agent.write(msg)
	if msg.Type == protocol.TypeForwardClose || msg.Type == protocol.TypeForwardError {
		s.closeForward(msg.SessionID)
	}
}

func (s *Server) sendForwardError(deviceID, sessionID, errMsg string) error {
	agent := s.getAgent(deviceID)
	if agent == nil {
		return nil
	}
	return agent.write(protocol.Message{
		Type:      protocol.TypeForwardError,
		SessionID: sessionID,
		Error:     errMsg,
	})
}

func (s *Server) closeForward(sessionID string) {
	s.sessionsMu.Lock()
	delete(s.forwards, sessionID)
	s.sessionsMu.Unlock()
}

// closeAgentForwards tears down relay sessions when one side goes offline.
func (s *Server) closeAgentForwards(deviceID string) {
	s.sessionsMu.Lock()
	type closedForward struct {
		id    string
		other string
	}
	var closed []closedForward
	for id, sess := range s.forwards {
		if sess.source == deviceID || sess.target == deviceID {
			other := sess.target
			if sess.target == deviceID {
				other = sess.source
			}
			closed = append(closed, closedForward{id: id, other: other})
		}
	}
	for _, cf := range closed {
		delete(s.forwards, cf.id)
	}
	s.sessionsMu.Unlock()
	for _, cf := range closed {
		if agent := s.getAgent(cf.other); agent != nil {
			_ = agent.write(protocol.Message{Type: protocol.TypeForwardClose, SessionID: cf.id})
		}
	}
}
