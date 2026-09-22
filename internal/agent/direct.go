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

// startDirectOffer creates the WebRTC offer side for a forward session.
func (a *Agent) startDirectOffer(sessionID string, servicePort int) error {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: a.iceServers()})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.peerConns[sessionID] = pc
	a.mu.Unlock()

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		a.sendICE(sessionID, c)
	})
	dc, err := pc.CreateDataChannel("warpmesh", nil)
	if err != nil {
		return err
	}
	a.setupDataChannel(sessionID, dc, 0)

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	raw, err := json.Marshal(offer)
	if err != nil {
		return err
	}
	return a.send(protocol.Message{
		Type:      protocol.TypeForwardOffer,
		SessionID: sessionID,
		Port:      servicePort,
		Data:      base64.StdEncoding.EncodeToString(raw),
	})
}

// handleDirectOffer answers a WebRTC offer and bridges to the local service.
func (a *Agent) handleDirectOffer(msg protocol.Message) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: a.iceServers()})
	if err != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: err.Error()})
		return
	}
	a.mu.Lock()
	a.peerConns[msg.SessionID] = pc
	a.mu.Unlock()

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		a.setupDataChannel(msg.SessionID, dc, msg.Port)
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		a.sendICE(msg.SessionID, c)
	})

	var offer webrtc.SessionDescription
	raw, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil || json.Unmarshal(raw, &offer) != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: "invalid offer"})
		return
	}
	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: err.Error()})
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: err.Error()})
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardError, SessionID: msg.SessionID, Error: err.Error()})
		return
	}
	_ = a.sendSignaling(protocol.TypeForwardAnswer, msg.SessionID, answer)
}

// handleDirectAnswer feeds the offerer with the answer SDP.
func (a *Agent) handleDirectAnswer(msg protocol.Message) {
	a.mu.Lock()
	pc := a.peerConns[msg.SessionID]
	a.mu.Unlock()
	if pc == nil {
		return
	}
	var answer webrtc.SessionDescription
	raw, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil || json.Unmarshal(raw, &answer) != nil {
		return
	}
	_ = pc.SetRemoteDescription(answer)
}

// handleDirectICE feeds an ICE candidate into the matching peer connection.
func (a *Agent) handleDirectICE(msg protocol.Message) {
	a.mu.Lock()
	pc := a.peerConns[msg.SessionID]
	a.mu.Unlock()
	if pc == nil {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return
	}
	var candidate webrtc.ICECandidateInit
	if json.Unmarshal(raw, &candidate) != nil {
		return
	}
	_ = pc.AddICECandidate(candidate)
}

func (a *Agent) setupDataChannel(sessionID string, dc *webrtc.DataChannel, servicePort int) {
	a.mu.Lock()
	a.dataChannels[sessionID] = dc
	a.mu.Unlock()

	dc.OnOpen(func() {
		_ = a.send(protocol.Message{Type: protocol.TypeForwardDirectOK, SessionID: sessionID})
		a.mu.Lock()
		ch := a.directPending[sessionID]
		a.mu.Unlock()
		if ch != nil {
			ch <- struct{}{}
		}
		if servicePort > 0 {
			go a.bridgeTargetChannel(sessionID, servicePort)
		}
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		a.mu.Lock()
		conn := a.localConns[sessionID]
		if conn == nil {
			buf := append([]byte(nil), msg.Data...)
			a.pendingData[sessionID] = append(a.pendingData[sessionID], buf)
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		_, _ = conn.Write(msg.Data)
	})
}

func (a *Agent) bridgeTargetChannel(sessionID string, port int) {
	service, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), 5*time.Second)
	if err != nil {
		log.Printf("direct bridge: service dial failed: %v", err)
		_ = a.send(protocol.Message{Type: protocol.TypeForwardClose, SessionID: sessionID})
		return
	}
	a.mu.Lock()
	a.localConns[sessionID] = service
	pending := a.pendingData[sessionID]
	delete(a.pendingData, sessionID)
	a.mu.Unlock()
	defer service.Close()
	for _, chunk := range pending {
		_, _ = service.Write(chunk)
	}

	a.mu.Lock()
	dc := a.dataChannels[sessionID]
	a.mu.Unlock()
	if dc == nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := service.Read(buf)
		if n > 0 {
			if serr := dc.Send(buf[:n]); serr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = a.send(protocol.Message{Type: protocol.TypeForwardClose, SessionID: sessionID})
}

func (a *Agent) sendICE(sessionID string, c *webrtc.ICECandidate) {
	raw, err := json.Marshal(c.ToJSON())
	if err != nil {
		return
	}
	_ = a.send(protocol.Message{
		Type:      protocol.TypeForwardICE,
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString(raw),
	})
}

func (a *Agent) sendSignaling(msgType, sessionID string, desc webrtc.SessionDescription) error {
	raw, err := json.Marshal(desc)
	if err != nil {
		return err
	}
	return a.send(protocol.Message{
		Type:      msgType,
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString(raw),
	})
}

func (a *Agent) iceServers() []webrtc.ICEServer {
	if len(a.cfg.STUNServers) == 0 {
		return nil
	}
	return []webrtc.ICEServer{{URLs: a.cfg.STUNServers}}
}

func (a *Agent) copyLocalToChannel(sessionID string, conn net.Conn, dc *webrtc.DataChannel) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if serr := dc.Send(buf[:n]); serr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = a.send(protocol.Message{Type: protocol.TypeForwardClose, SessionID: sessionID})
	_ = dc.Close()
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
