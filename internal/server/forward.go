package server

import (
	"fmt"
	"strconv"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
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

	metricID, metricErr := s.reg.StartConnection(sourceID, msg.Target, sourceID, fmt.Sprintf("tcp:%d", msg.Port), "negotiating", source.publicIP)
	if metricErr != nil {
		s.logf("record tunnel connection: %v", metricErr)
	}
	var traffic *trafficRecorder
	if metricErr == nil {
		traffic = newTrafficRecorder(s.reg, metricID)
	}
	sess := &forwardSession{source: sourceID, target: msg.Target, transport: "negotiating", traffic: traffic}
	s.sessionsMu.Lock()
	s.forwards[msg.SessionID] = sess
	s.sessionsMu.Unlock()
	_ = s.reg.RecordAudit(sourceID, "forward.connect", msg.Target, strconv.Itoa(msg.Port))

	// Give WebRTC ICE time to establish a direct connection. If it does not
	// succeed in time, fall back to relaying through the server.
	sess.timer = time.AfterFunc(directAttemptTimeout, func() {
		s.sessionsMu.Lock()
		current := s.forwards[msg.SessionID]
		if current == nil || current.direct || current.timer == nil {
			s.sessionsMu.Unlock()
			return
		}
		current.timer = nil
		current.transport = "relay"
		s.sessionsMu.Unlock()
		if current.traffic != nil {
			_ = current.traffic.registry.SetConnectionTransport(current.traffic.id, "relay")
		}
		_ = s.reg.RecordAudit(sourceID, "forward.relay", msg.Target, msg.SessionID)
		s.startRelayFallback(msg)
	})
}

func (s *Server) startRelayFallback(msg protocol.Message) {
	target := s.getAgent(msg.Target)
	if target == nil {
		s.closeForward(msg.SessionID, "failed")
		return
	}
	if err := target.write(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: msg.SessionID,
		Port:      msg.Port,
	}); err != nil {
		s.closeForward(msg.SessionID, "failed")
	}
}

func (s *Server) handleDirectOK(deviceID string, msg protocol.Message) {
	s.sessionsMu.Lock()
	sess := s.forwards[msg.SessionID]
	transitioned := sess != nil && !sess.direct
	if transitioned {
		sess.direct = true
		sess.transport = "direct"
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
	}
	s.sessionsMu.Unlock()
	if transitioned && sess.traffic != nil {
		_ = sess.traffic.registry.SetConnectionTransport(sess.traffic.id, "direct")
	}
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
		s.closeForward(msg.SessionID, "interrupted")
		return
	}
	if msg.Type == protocol.TypeForwardData && sess.transport == "relay" {
		if raw, err := protocol.DecodeData(msg.Data); err == nil {
			if fromID == sess.source {
				sess.traffic.Add(int64(len(raw)), 0)
			} else {
				sess.traffic.Add(0, int64(len(raw)))
			}
		}
	}
	if err := agent.write(msg); err != nil {
		s.closeForward(msg.SessionID, "interrupted")
		return
	}
	if msg.Type == protocol.TypeForwardClose || msg.Type == protocol.TypeForwardError {
		state := "closed"
		if msg.Type == protocol.TypeForwardError {
			state = "failed"
		}
		s.closeForward(msg.SessionID, state)
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

func (s *Server) closeForward(sessionID, state string) {
	s.sessionsMu.Lock()
	sess := s.forwards[sessionID]
	delete(s.forwards, sessionID)
	s.sessionsMu.Unlock()
	if sess != nil {
		if sess.timer != nil {
			sess.timer.Stop()
		}
		sess.traffic.Close(state)
	}
}

// closeAgentForwards tears down relay sessions when one side goes offline.
func (s *Server) closeAgentForwards(deviceID string) {
	s.sessionsMu.Lock()
	type closedForward struct {
		id      string
		other   string
		traffic *trafficRecorder
	}
	var closed []closedForward
	for id, sess := range s.forwards {
		if sess.source == deviceID || sess.target == deviceID {
			other := sess.target
			if sess.target == deviceID {
				other = sess.source
			}
			closed = append(closed, closedForward{id: id, other: other, traffic: sess.traffic})
		}
	}
	for _, cf := range closed {
		delete(s.forwards, cf.id)
	}
	s.sessionsMu.Unlock()
	for _, cf := range closed {
		cf.traffic.Close("interrupted")
		if agent := s.getAgent(cf.other); agent != nil {
			_ = agent.write(protocol.Message{Type: protocol.TypeForwardClose, SessionID: cf.id})
		}
	}
}
