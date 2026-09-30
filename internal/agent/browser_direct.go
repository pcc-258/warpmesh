package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (a *Agent) handleBrowserOffer(msg protocol.Message) {
	logDebug("rtc.offer.received session=%s service=%s", msg.SessionID, msg.Service)
	if msg.Service != "terminal" && msg.Service != "desktop" {
		_ = a.sendBrowserRTCError(msg.SessionID, msg.Service, "unsupported browser service")
		return
	}
	if msg.Service == "terminal" {
		a.mu.Lock()
		session := a.sessions[msg.SessionID]
		a.mu.Unlock()
		if session == nil {
			_ = a.sendBrowserRTCError(msg.SessionID, msg.Service, "terminal session is unavailable")
			return
		}
	}

	var serviceConn net.Conn
	if msg.Service == "desktop" {
		var err error
		serviceConn, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", a.cfg.VNCPort), 5*time.Second)
		if err != nil {
			_ = a.sendBrowserRTCError(msg.SessionID, msg.Service, "VNC service is unavailable: "+err.Error())
			return
		}
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: a.iceServers()})
	if err != nil {
		if serviceConn != nil {
			_ = serviceConn.Close()
		}
		_ = a.sendBrowserRTCError(msg.SessionID, msg.Service, err.Error())
		return
	}
	a.mu.Lock()
	previous := a.peerConns[msg.SessionID]
	a.peerConns[msg.SessionID] = pc
	if serviceConn != nil {
		a.localConns[msg.SessionID] = serviceConn
	}
	a.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		a.setupBrowserDataChannel(msg.SessionID, msg.Service, dc, serviceConn)
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[agent] browser direct %s (%s): ice state %s", msg.SessionID, msg.Service, state)
		if state == webrtc.ICEConnectionStateFailed {
			a.failBrowserDirect(msg.SessionID, msg.Service, "ICE connection failed")
		}
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		logDebug("rtc.candidate.sent session=%s service=%s candidate=%s", msg.SessionID, msg.Service, c.String())
		raw, err := json.Marshal(c.ToJSON())
		if err != nil {
			return
		}
		if err := a.send(protocol.Message{
			Type:      protocol.TypeRTCICE,
			SessionID: msg.SessionID,
			Service:   msg.Service,
			Data:      base64.StdEncoding.EncodeToString(raw),
		}); err != nil {
			a.failBrowserDirect(msg.SessionID, msg.Service, err.Error())
		}
	})

	var offer webrtc.SessionDescription
	raw, err := base64.StdEncoding.DecodeString(msg.Data)
	if err == nil {
		err = json.Unmarshal(raw, &offer)
	}
	if err != nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, "invalid browser offer")
		return
	}
	if err := pc.SetRemoteDescription(offer); err != nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, "invalid browser offer: "+err.Error())
		return
	}
	a.flushPendingCandidates(msg.SessionID, pc)
	answer, err := pc.CreateAnswer(nil)
	if err == nil {
		err = pc.SetLocalDescription(answer)
	}
	if err != nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, "could not create WebRTC answer: "+err.Error())
		return
	}
	local := pc.LocalDescription()
	if local == nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, "WebRTC answer was not created")
		return
	}
	raw, err = json.Marshal(local)
	if err != nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, err.Error())
		return
	}
	if err := a.send(protocol.Message{
		Type:      protocol.TypeRTCAnswer,
		SessionID: msg.SessionID,
		Service:   msg.Service,
		Data:      base64.StdEncoding.EncodeToString(raw),
	}); err != nil {
		a.failBrowserDirect(msg.SessionID, msg.Service, err.Error())
	}
	logDebug("rtc.answer.sent session=%s service=%s", msg.SessionID, msg.Service)
}

func (a *Agent) flushPendingCandidates(sessionID string, pc *webrtc.PeerConnection) {
	a.mu.Lock()
	pending := a.pendingCandidates[sessionID]
	delete(a.pendingCandidates, sessionID)
	a.mu.Unlock()
	for _, candidate := range pending {
		if err := pc.AddICECandidate(candidate); err != nil {
			logDebug("rtc.candidate.add.failed session=%s err=%v", sessionID, err)
		}
	}
}

func (a *Agent) handleBrowserICE(msg protocol.Message) {
	raw, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return
	}
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return
	}
	a.mu.Lock()
	pc := a.peerConns[msg.SessionID]
	ready := pc != nil && pc.RemoteDescription() != nil
	a.mu.Unlock()
	if !ready {
		// The offer is still being processed; buffer the candidate until the
		// remote description is in place.
		a.mu.Lock()
		a.pendingCandidates[msg.SessionID] = append(a.pendingCandidates[msg.SessionID], candidate)
		a.mu.Unlock()
		return
	}
	if err := pc.AddICECandidate(candidate); err != nil {
		logDebug("rtc.candidate.add.failed session=%s err=%v", msg.SessionID, err)
		return
	}
	logDebug("rtc.candidate.received session=%s candidate=%s", msg.SessionID, candidate.Candidate)
}

func (a *Agent) setupBrowserDataChannel(sessionID, service string, dc *webrtc.DataChannel, serviceConn net.Conn) {
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if service == "terminal" {
			_ = a.writeSession(sessionID, func(session *TermSession) error {
				return session.Input(string(msg.Data))
			})
			return
		}
		if serviceConn != nil {
			if _, err := serviceConn.Write(msg.Data); err != nil {
				a.failBrowserDirect(sessionID, service, "VNC write failed: "+err.Error())
			}
		}
	})
	dc.OnOpen(func() {
		log.Printf("browser direct %s (%s): data channel open", sessionID, service)
		if service == "terminal" {
			a.mu.Lock()
			if a.sessions[sessionID] == nil {
				a.mu.Unlock()
				a.failBrowserDirect(sessionID, service, "terminal session ended during connection")
				return
			}
			a.browserChannels[sessionID] = dc
			a.browserDirect[sessionID] = true
			a.mu.Unlock()
		} else {
			if serviceConn == nil {
				a.failBrowserDirect(sessionID, service, "VNC service is unavailable")
				return
			}
			a.mu.Lock()
			a.browserChannels[sessionID] = dc
			a.browserDirect[sessionID] = true
			a.mu.Unlock()
		}
		if err := a.send(protocol.Message{Type: protocol.TypeRTCDirectOK, SessionID: sessionID, Service: service}); err != nil {
			a.failBrowserDirect(sessionID, service, err.Error())
		}
	})
	dc.OnClose(func() {
		a.mu.Lock()
		if a.browserChannels[sessionID] == dc {
			delete(a.browserChannels, sessionID)
			delete(a.browserDirect, sessionID)
		}
		a.mu.Unlock()
	})
	dc.OnError(func(err error) {
		a.failBrowserDirect(sessionID, service, err.Error())
	})
}

func (a *Agent) startBrowserDesktop(sessionID string) {
	a.mu.Lock()
	dc := a.browserChannels[sessionID]
	conn := a.localConns[sessionID]
	direct := a.browserDirect[sessionID]
	a.mu.Unlock()
	if dc == nil || conn == nil || !direct || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	go a.bridgeBrowserDesktop(sessionID, conn, dc)
}

func (a *Agent) bridgeBrowserDesktop(sessionID string, conn net.Conn, dc *webrtc.DataChannel) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if sendErr := dc.Send(buf[:n]); sendErr != nil {
				a.failBrowserDirect(sessionID, "desktop", "VNC data channel failed: "+sendErr.Error())
				return
			}
		}
		if err != nil {
			if a.isBrowserDirect(sessionID) {
				a.failBrowserDirect(sessionID, "desktop", "VNC connection closed: "+err.Error())
			}
			return
		}
	}
}

func (a *Agent) sendTerminalOutput(sessionID string, data []byte) {
	a.mu.Lock()
	dc := a.browserChannels[sessionID]
	direct := a.browserDirect[sessionID]
	a.mu.Unlock()
	if direct && dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
		if err := dc.Send(data); err == nil {
			return
		} else {
			a.failBrowserDirect(sessionID, "terminal", "terminal data channel failed: "+err.Error())
		}
	}
	_ = a.send(protocol.Message{Type: protocol.TypeTermOutput, SessionID: sessionID, Data: protocol.EncodeData(data)})
}

func (a *Agent) isBrowserDirect(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.browserDirect[sessionID]
}

func (a *Agent) failBrowserDirect(sessionID, service, reason string) {
	a.mu.Lock()
	pc := a.peerConns[sessionID]
	active := pc != nil
	a.mu.Unlock()
	if !active {
		return
	}
	_ = a.sendBrowserRTCError(sessionID, service, reason)
	a.closeBrowserDirect(sessionID)
}

func (a *Agent) sendBrowserRTCError(sessionID, service, reason string) error {
	log.Printf("browser direct %s (%s): %s", sessionID, service, reason)
	return a.send(protocol.Message{Type: protocol.TypeRTCDirectError, SessionID: sessionID, Service: service, Error: reason})
}

func (a *Agent) closeBrowserDirect(sessionID string) {
	a.mu.Lock()
	pc := a.peerConns[sessionID]
	delete(a.peerConns, sessionID)
	dc := a.browserChannels[sessionID]
	delete(a.browserChannels, sessionID)
	delete(a.browserDirect, sessionID)
	conn := a.localConns[sessionID]
	delete(a.localConns, sessionID)
	delete(a.pendingCandidates, sessionID)
	a.mu.Unlock()
	if dc != nil {
		_ = dc.Close()
	}
	if pc != nil {
		_ = pc.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}
