package server

import (
	"fmt"
	"strconv"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

const directAttemptTimeout = 5 * time.Second

// forwardSession fields are guarded by Server.sessionsMu. These accessors keep
// every read and write behind the lock so the timer callback and the message
// routing path cannot race with each other.

// isRelay reports whether the session is currently carried by the server relay.
func (s *forwardSession) isRelay() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport == "relay"
}

// markDirect promotes the session to a direct path and cancels the fallback
// timer. It reports whether this call performed the transition.
func (s *forwardSession) markDirect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.direct {
		return false
	}
	s.direct = true
	s.transport = "direct"
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	return true
}

// markRelay switches the session to the server relay if no direct path has been
// established yet, and returns the traffic recorder to flush.
func (s *forwardSession) markRelay() (*trafficRecorder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.direct || s.timer == nil {
		return nil, false
	}
	s.timer = nil
	s.transport = "relay"
	return s.traffic, true
}

// setTimer arms the direct-attempt fallback timer.
func (s *forwardSession) setTimer(timer *time.Timer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = timer
}

// stopTimer cancels the fallback timer and returns any traffic recorder so the
// caller can close it without holding the session lock.
func (s *forwardSession) stopTimer() *trafficRecorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	return s.traffic
}

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
	// Authorize before anything is offered to the target: a denied request must
	// not start a session, reach the peer, or consume a connection metric. The
	// audit is written first so it is durable even if the source has gone away.
	if !s.reg.ForwardAllowed(sourceID, msg.Target, msg.Port) {
		_ = s.reg.RecordAudit(sourceID, "forward.denied", msg.Target, fmt.Sprintf("%s:%d", msg.Target, msg.Port))
		_ = s.sendForwardError(sourceID, msg.SessionID,
			fmt.Sprintf("forward denied: %s is not permitted to reach %s:%d", sourceID, msg.Target, msg.Port))
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
	// succeed in time, fall back to relaying through the server. The timer is
	// published through setTimer, so the callback never reads a field the
	// connect path is still writing.
	sess.setTimer(time.AfterFunc(directAttemptTimeout, func() {
		current := s.getForward(msg.SessionID)
		if current == nil || current != sess {
			return
		}
		traffic, ok := sess.markRelay()
		if !ok {
			return
		}
		if traffic != nil {
			_ = traffic.registry.SetConnectionTransport(traffic.id, "relay")
		}
		_ = s.reg.RecordAudit(sourceID, "forward.relay", msg.Target, msg.SessionID)
		s.startRelayFallback(msg)
	}))
}

// getForward returns the forward session for a browser session id, or nil.
func (s *Server) getForward(sessionID string) *forwardSession {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.forwards[sessionID]
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
	sess := s.getForward(msg.SessionID)
	var traffic *trafficRecorder
	if sess != nil && sess.markDirect() {
		traffic = sess.traffic
	}
	if traffic != nil {
		_ = traffic.registry.SetConnectionTransport(traffic.id, "direct")
	}
	_ = s.reg.RecordAudit(deviceID, "forward.direct", msg.SessionID, "")
}

func (s *Server) relayForward(fromID string, msg protocol.Message) {
	sess := s.getForward(msg.SessionID)
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
	if msg.Type == protocol.TypeForwardData && sess.isRelay() {
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
		if traffic := sess.stopTimer(); traffic != nil {
			traffic.Close(state)
		}
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
