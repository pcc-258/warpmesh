package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func newTestKey(t *testing.T, srv *Server) DeviceKey {
	t.Helper()
	key, err := srv.reg.CreateDeviceKey("test-device", 0)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestTerminalRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "test box",
		Hostname: "test-box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	waitFor(t, func() bool {
		resp, err := http.Get(ts.URL + "/api/health")
		return err == nil && resp.StatusCode == http.StatusOK
	})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/devices", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var devices []Device
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || !devices[0].Online {
		t.Fatalf("expected one online device, got %+v", devices)
	}

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/terminal?token=admin-token&device="+key.DeviceID+"&cols=80&rows=24", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browserWS.Close() }()

	start := readAgentMessage(t, agentWS)
	if start.Type != protocol.TypeTermStart || start.SessionID == "" || start.Cols != 80 {
		t.Fatalf("unexpected start message: %+v", start)
	}
	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCOffer, Data: "offer-sdp"}); err != nil {
		t.Fatal(err)
	}
	offer := readAgentMessage(t, agentWS)
	if offer.Type != protocol.TypeRTCOffer || offer.SessionID != start.SessionID || offer.Service != "terminal" {
		t.Fatalf("unexpected browser offer: %+v", offer)
	}
	if err := agentWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCAnswer, SessionID: start.SessionID, Service: "terminal", Data: "answer-sdp"}); err != nil {
		t.Fatal(err)
	}
	var answer protocol.Message
	if err := browserWS.ReadJSON(&answer); err != nil || answer.Type != protocol.TypeRTCAnswer || answer.Data != "answer-sdp" {
		t.Fatalf("unexpected browser answer: %+v err=%v", answer, err)
	}
	if err := agentWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCDirectOK, SessionID: start.SessionID, Service: "terminal"}); err != nil {
		t.Fatal(err)
	}
	var direct protocol.Message
	if err := browserWS.ReadJSON(&direct); err != nil || direct.Type != protocol.TypeRTCDirectOK {
		t.Fatalf("unexpected direct status: %+v err=%v", direct, err)
	}
	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCFallback}); err != nil {
		t.Fatal(err)
	}
	if relay := readAgentMessage(t, agentWS); relay.Type != protocol.TypeRTCRelay || relay.SessionID != start.SessionID {
		t.Fatalf("unexpected relay fallback request: %+v", relay)
	}
	var relayStatus protocol.Message
	if err := browserWS.ReadJSON(&relayStatus); err != nil || relayStatus.Type != protocol.TypeRTCRelay {
		t.Fatalf("unexpected relay status: %+v err=%v", relayStatus, err)
	}

	if err := agentWS.WriteJSON(protocol.Message{
		Type:      protocol.TypeTermOutput,
		SessionID: start.SessionID,
		Data:      protocol.EncodeData([]byte("hello\n")),
	}); err != nil {
		t.Fatal(err)
	}

	var out protocol.Message
	if err := browserWS.ReadJSON(&out); err != nil {
		t.Fatal(err)
	}
	raw, _ := protocol.DecodeData(out.Data)
	if string(raw) != "hello\n" {
		t.Fatalf("unexpected output: %q", raw)
	}

	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeTermInput, Data: "ls\r"}); err != nil {
		t.Fatal(err)
	}
	input := readAgentMessage(t, agentWS)
	if input.Type != protocol.TypeTermInput || input.Data != "ls\r" {
		t.Fatalf("unexpected input message: %+v", input)
	}

	if err := agentWS.WriteJSON(protocol.Message{Type: protocol.TypeTermExit, SessionID: start.SessionID, Code: 0}); err != nil {
		t.Fatal(err)
	}
	var exit protocol.Message
	if err := browserWS.ReadJSON(&exit); err != nil {
		t.Fatal(err)
	}
	if exit.Type != protocol.TypeTermExit {
		t.Fatalf("expected exit, got %+v", exit)
	}

	analyticsReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/analytics?range=24h", nil)
	analyticsReq.Header.Set("Authorization", "Bearer admin-token")
	analyticsResp, err := http.DefaultClient.Do(analyticsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = analyticsResp.Body.Close() }()
	var analytics struct {
		Sessions      int                `json:"sessions"`
		Active        int                `json:"active"`
		RelaySessions int                `json:"relaySessions"`
		RelayBytes    int64              `json:"relayBytes"`
		Recent        []ConnectionRecord `json:"recent"`
	}
	if err := json.NewDecoder(analyticsResp.Body).Decode(&analytics); err != nil {
		t.Fatal(err)
	}
	if analyticsResp.StatusCode != http.StatusOK || analytics.Sessions != 1 || analytics.Active != 0 || analytics.RelaySessions != 1 || analytics.RelayBytes != 9 {
		t.Fatalf("unexpected terminal analytics: status=%d response=%+v", analyticsResp.StatusCode, analytics)
	}
	if len(analytics.Recent) != 1 || analytics.Recent[0].Service != "terminal" || analytics.Recent[0].Transport != "relay" || analytics.Recent[0].State != "closed" {
		t.Fatalf("unexpected terminal connection record: %+v", analytics.Recent)
	}
}

func TestBrowserRTCTrickleICE(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "rtc box",
		Hostname: "rtc-box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/terminal?token=admin-token&device="+key.DeviceID+"&cols=80&rows=24", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browserWS.Close() }()

	start := readAgentMessage(t, agentWS)
	if start.Type != protocol.TypeTermStart || start.SessionID == "" {
		t.Fatalf("unexpected start message: %+v", start)
	}
	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCOffer, Data: "offer-sdp"}); err != nil {
		t.Fatal(err)
	}
	offer := readAgentMessage(t, agentWS)
	if offer.Type != protocol.TypeRTCOffer || offer.SessionID != start.SessionID || offer.Service != "terminal" {
		t.Fatalf("unexpected browser offer: %+v", offer)
	}

	// The browser trickles a candidate; the server must forward it with the
	// session and service attached.
	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCICE, Data: "browser-candidate"}); err != nil {
		t.Fatal(err)
	}
	candidate := readAgentMessage(t, agentWS)
	if candidate.Type != protocol.TypeRTCICE || candidate.SessionID != start.SessionID || candidate.Service != "terminal" || candidate.Data != "browser-candidate" {
		t.Fatalf("unexpected browser candidate: %+v", candidate)
	}

	// The agent trickles a candidate; the browser must receive it unchanged.
	if err := agentWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCICE, SessionID: start.SessionID, Service: "terminal", Data: "agent-candidate"}); err != nil {
		t.Fatal(err)
	}
	var remote protocol.Message
	if err := browserWS.ReadJSON(&remote); err != nil || remote.Type != protocol.TypeRTCICE || remote.Data != "agent-candidate" {
		t.Fatalf("unexpected agent candidate: %+v err=%v", remote, err)
	}
}

func TestLoginStatsAndDeviceKeys(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatal(err)
	}
	if loginResp.Token == "" {
		t.Fatal("expected session token")
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/device-keys", bytes.NewReader([]byte(`{"name":"home pc"}`)))
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	keyResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyResp.Body.Close() }()
	var key DeviceKey
	if err := json.NewDecoder(keyResp.Body).Decode(&key); err != nil {
		t.Fatal(err)
	}
	if key.DeviceID == "" || key.Token == "" {
		t.Fatalf("expected device key with token, got %+v", key)
	}

	// The per-device key must be able to bring an agent online.
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "home pc",
		Hostname: "home-pc",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	statsReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/stats", nil)
	statsReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	statsResp, err := http.DefaultClient.Do(statsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = statsResp.Body.Close() }()
	var stats struct {
		Total  int `json:"total"`
		Online int `json:"online"`
	}
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Online != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	auditReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/audit?limit=20", nil)
	auditReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	auditResp, err := http.DefaultClient.Do(auditReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = auditResp.Body.Close() }()
	var audit []AuditEntry
	if err := json.NewDecoder(auditResp.Body).Decode(&audit); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if entry.Action == "device-key.create" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected device-key.create in audit log, got %+v", audit)
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	bad := []byte(`{"username":"admin","password":"wrong"}`)
	for i := 0; i < 5; i++ {
		resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, resp.StatusCode)
		}
	}
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after lockout, got %d", resp.StatusCode)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/logout", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	logoutResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from logout, got %d", logoutResp.StatusCode)
	}

	statsReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/stats", nil)
	statsReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	statsResp, err := http.DefaultClient.Do(statsReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = statsResp.Body.Close()
	if statsResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", statsResp.StatusCode)
	}
}

func TestDeviceToDeviceForwardRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	keyA := newTestKey(t, srv)
	keyB := newTestKey(t, srv)
	dialAgent := func(key DeviceKey) *websocket.Conn {
		ws, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.WriteJSON(protocol.Message{
			Type:     protocol.TypeHello,
			DeviceID: key.DeviceID,
			Name:     key.DeviceID,
			Hostname: key.DeviceID,
			OS:       "linux",
			Arch:     "amd64",
		}); err != nil {
			t.Fatal(err)
		}
		return ws
	}

	agentA := dialAgent(keyA)
	defer func() { _ = agentA.Close() }()
	agentB := dialAgent(keyB)
	defer func() { _ = agentB.Close() }()
	waitFor(t, func() bool {
		_, okA := srv.reg.Get(keyA.DeviceID)
		_, okB := srv.reg.Get(keyB.DeviceID)
		return okA && okB
	})

	if err := agentA.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: "s1",
		Target:    keyB.DeviceID,
		Port:      22,
	}); err != nil {
		t.Fatal(err)
	}

	connectB := readAgentMessage(t, agentB)
	if connectB.Type != protocol.TypeForwardConnect || connectB.SessionID != "s1" || connectB.Port != 22 {
		t.Fatalf("unexpected connect for target: %+v", connectB)
	}

	if err := agentB.WriteJSON(protocol.Message{Type: protocol.TypeForwardOpen, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	openA := readAgentMessage(t, agentA)
	if openA.Type != protocol.TypeForwardOpen {
		t.Fatalf("expected forward:open, got %+v", openA)
	}

	if err := agentA.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardData,
		SessionID: "s1",
		Data:      protocol.EncodeData([]byte("ping")),
	}); err != nil {
		t.Fatal(err)
	}
	dataB := readAgentMessage(t, agentB)
	raw, _ := protocol.DecodeData(dataB.Data)
	if dataB.Type != protocol.TypeForwardData || string(raw) != "ping" {
		t.Fatalf("unexpected data at target: %+v", dataB)
	}

	if err := agentB.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardData,
		SessionID: "s1",
		Data:      protocol.EncodeData([]byte("pong")),
	}); err != nil {
		t.Fatal(err)
	}
	dataA := readAgentMessage(t, agentA)
	raw, _ = protocol.DecodeData(dataA.Data)
	if dataA.Type != protocol.TypeForwardData || string(raw) != "pong" {
		t.Fatalf("unexpected data at source: %+v", dataA)
	}

	if err := agentA.WriteJSON(protocol.Message{Type: protocol.TypeForwardClose, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	closeB := readAgentMessage(t, agentB)
	if closeB.Type != protocol.TypeForwardClose {
		t.Fatalf("expected forward:close, got %+v", closeB)
	}
}

func TestForwardDirectAttemptSuppressesRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	keyA := newTestKey(t, srv)
	keyB := newTestKey(t, srv)
	dialAgent := func(key DeviceKey) *websocket.Conn {
		ws, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.WriteJSON(protocol.Message{
			Type:     protocol.TypeHello,
			DeviceID: key.DeviceID,
			Name:     key.DeviceID,
			Hostname: key.DeviceID,
			OS:       "linux",
			Arch:     "amd64",
		}); err != nil {
			t.Fatal(err)
		}
		return ws
	}

	agentA := dialAgent(keyA)
	defer func() { _ = agentA.Close() }()
	agentB := dialAgent(keyB)
	defer func() { _ = agentB.Close() }()
	waitFor(t, func() bool {
		_, okA := srv.reg.Get(keyA.DeviceID)
		_, okB := srv.reg.Get(keyB.DeviceID)
		return okA && okB
	})

	if err := agentA.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: "s-direct",
		Target:    keyB.DeviceID,
		Port:      22,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		srv.sessionsMu.RLock()
		_, ok := srv.forwards["s-direct"]
		srv.sessionsMu.RUnlock()
		return ok
	})

	if err := agentB.WriteJSON(protocol.Message{Type: protocol.TypeForwardDirectOK, SessionID: "s-direct"}); err != nil {
		t.Fatal(err)
	}

	// The direct-ok must cancel the relay fallback: no forward:connect should
	// reach the target within the timeout window.
	_ = agentB.SetReadDeadline(time.Now().Add(directAttemptTimeout + 2*time.Second))
	for {
		var unexpected protocol.Message
		err := agentB.ReadJSON(&unexpected)
		if err != nil {
			break
		}
		if unexpected.Type == protocol.TypePing {
			continue
		}
		t.Fatalf("relay fallback should be cancelled after direct-ok, got %+v", unexpected)
	}
}

// newForwardTestServer starts a relay that accepts an admin bearer token.
func newForwardTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, "ws" + strings.TrimPrefix(ts.URL, "http")
}

// enrollTestAgent issues a device key, attaches the agent socket, and waits
// until the relay lists the device as online.
func enrollTestAgent(t *testing.T, srv *Server, baseWS string) (*websocket.Conn, string) {
	t.Helper()
	key, err := srv.reg.CreateDeviceKey("test-device", 0)
	if err != nil {
		t.Fatal(err)
	}
	ws, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if err := ws.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     key.DeviceID,
		Hostname: key.DeviceID,
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})
	return ws, key.DeviceID
}

// expectForwardRelayed asserts the relay accepted a session and forwarded the
// target-bound message. A real agent sends forward:connect and then its WebRTC
// offer, so the test mirrors that sequence and closes the session afterwards to
// disarm the relay fallback timer.
func expectForwardRelayed(t *testing.T, srv *Server, source, target *websocket.Conn, targetID, sessionID string, port int) {
	t.Helper()
	if err := source.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: sessionID,
		Target:    targetID,
		Port:      port,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		srv.sessionsMu.RLock()
		defer srv.sessionsMu.RUnlock()
		return srv.forwards[sessionID] != nil
	})
	if err := source.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardOffer,
		SessionID: sessionID,
		Port:      port,
		Data:      "offer",
	}); err != nil {
		t.Fatal(err)
	}
	offer := readAgentMessage(t, target)
	if offer.Type != protocol.TypeForwardOffer || offer.SessionID != sessionID || offer.Port != port {
		t.Fatalf("expected the relay to forward session %s on port %d, got %+v", sessionID, port, offer)
	}
	if err := source.WriteJSON(protocol.Message{Type: protocol.TypeForwardClose, SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if closed := readAgentMessage(t, target); closed.Type != protocol.TypeForwardClose {
		t.Fatalf("expected forward:close, got %+v", closed)
	}
}

// expectForwardRefused asserts the source is refused, no session is started, no
// connection metric is consumed, and the refusal is audited. It deliberately
// does not read the target socket: gorilla caches the error of a timed-out read,
// so the silence check (expectNoAgentMessage) must be the last thing a target
// socket is used for.
func expectForwardRefused(t *testing.T, srv *Server, source *websocket.Conn, targetID, sessionID string, port int) {
	t.Helper()
	before := len(srv.reg.ListConnections(time.Now().Add(-time.Hour), 100))

	if err := source.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: sessionID,
		Target:    targetID,
		Port:      port,
	}); err != nil {
		t.Fatal(err)
	}
	// A denied source must not be able to smuggle the session in through a
	// follow-up offer either: without a session the relay has nowhere to send it.
	if err := source.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardOffer,
		SessionID: sessionID,
		Port:      port,
		Data:      "offer",
	}); err != nil {
		t.Fatal(err)
	}

	refusal := readAgentMessage(t, source)
	if refusal.Type != protocol.TypeForwardError || refusal.SessionID != sessionID {
		t.Fatalf("expected forward:error for session %s, got %+v", sessionID, refusal)
	}
	if !strings.Contains(refusal.Error, "denied") {
		t.Fatalf("refusal should explain the denial, got %q", refusal.Error)
	}

	srv.sessionsMu.RLock()
	_, live := srv.forwards[sessionID]
	srv.sessionsMu.RUnlock()
	if live {
		t.Fatalf("denied forward %s must not start a session", sessionID)
	}
	if after := len(srv.reg.ListConnections(time.Now().Add(-time.Hour), 100)); after != before {
		t.Fatalf("denied forward must not consume a connection metric: %d -> %d", before, after)
	}
	entry, ok := findAudit(srv.reg.ListAudit(50), "forward.denied")
	if !ok {
		t.Fatal("expected a forward.denied audit entry")
	}
	if entry.Target != targetID {
		t.Fatalf("forward.denied should name the target, got %+v", entry)
	}
	if !strings.Contains(entry.Detail, targetID) || !strings.Contains(entry.Detail, strconv.Itoa(port)) {
		t.Fatalf("forward.denied detail should carry target and port, got %q", entry.Detail)
	}
}

// expectNoAgentMessage fails if any application message reached the agent.
// Traffic heartbeats are ignored. A timed-out read permanently poisons the
// socket for reads, so callers must not read it again afterwards.
func expectNoAgentMessage(t *testing.T, ws *websocket.Conn, wait time.Duration) {
	t.Helper()
	if err := ws.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	for {
		var msg protocol.Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return
			}
			t.Fatalf("target connection ended unexpectedly: %v", err)
		}
		if msg.Type != protocol.TypePing {
			t.Fatalf("expected no message at the target, got %+v", msg)
		}
	}
}

func findAudit(entries []AuditEntry, action string) (AuditEntry, bool) {
	for _, e := range entries {
		if e.Action == action {
			return e, true
		}
	}
	return AuditEntry{}, false
}

// TestForwardDefaultPolicyAllowsConnect pins the zero-configuration default: two
// devices that never touched the ACL can forward immediately.
func TestForwardDefaultPolicyAllowsConnect(t *testing.T) {
	srv, _, baseWS := newForwardTestServer(t)
	agentA, idA := enrollTestAgent(t, srv, baseWS)
	agentB, idB := enrollTestAgent(t, srv, baseWS)

	if dev, ok := srv.reg.Get(idA); !ok || dev.ForwardPolicy != ForwardPolicyAny {
		t.Fatalf("a fresh device must default to %q, got %+v", ForwardPolicyAny, dev)
	}
	if err := agentA.WriteJSON(protocol.Message{
		Type:      protocol.TypeForwardConnect,
		SessionID: "s1",
		Target:    idB,
		Port:      22,
	}); err != nil {
		t.Fatal(err)
	}
	connect := readAgentMessage(t, agentB)
	if connect.Type != protocol.TypeForwardConnect || connect.SessionID != "s1" || connect.Port != 22 {
		t.Fatalf("default policy must relay the connect, got %+v", connect)
	}
}

// TestForwardRestrictedDeviceWithoutGrantIsDenied covers the core hardening:
// a restricted device with no grants cannot turn the relay into an open proxy.
func TestForwardRestrictedDeviceWithoutGrantIsDenied(t *testing.T) {
	srv, _, baseWS := newForwardTestServer(t)
	agentA, idA := enrollTestAgent(t, srv, baseWS)
	agentB, idB := enrollTestAgent(t, srv, baseWS)
	if err := srv.reg.SetForwardPolicy(idA, ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}

	expectForwardRefused(t, srv, agentA, idB, "s-denied", 22)
	// Nothing may reach the target — the denial happens before the offer.
	expectNoAgentMessage(t, agentB, 300*time.Millisecond)

	// The denial must be recorded even when the target is online and reachable.
	if entry, ok := findAudit(srv.reg.ListAudit(50), "forward.denied"); !ok || entry.Target != idB {
		t.Fatalf("expected an audit entry naming %s, got %+v", idB, entry)
	}
	if _, ok := findAudit(srv.reg.ListAudit(50), "forward.connect"); ok {
		t.Fatal("a denied forward must not be audited as an accepted connect")
	}
}

// TestForwardGrantMatchesTargetAndPort checks the exact-match rule for a
// restricted source: same target and a port inside the granted range pass,
// everything else is denied.
func TestForwardGrantMatchesTargetAndPort(t *testing.T) {
	srv, _, baseWS := newForwardTestServer(t)
	agentA, idA := enrollTestAgent(t, srv, baseWS)
	agentB, idB := enrollTestAgent(t, srv, baseWS)
	agentC, idC := enrollTestAgent(t, srv, baseWS)
	agentD, idD := enrollTestAgent(t, srv, baseWS)
	if err := srv.reg.SetForwardPolicy(idA, ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}
	if err := srv.reg.AddForwardGrant(idA, idB, 22, 22); err != nil {
		t.Fatal(err)
	}

	// A device outside the grant is refused, as are ports outside its range.
	expectForwardRefused(t, srv, agentA, idC, "s-other-target", 22)
	expectForwardRefused(t, srv, agentA, idB, "s-wrong-port-low", 21)
	expectForwardRefused(t, srv, agentA, idB, "s-wrong-port-high", 23)

	// The granted pair is relayed.
	expectForwardRelayed(t, srv, agentA, agentB, idB, "s-granted", 22)

	// A second grant extends access to another exact target.
	if err := srv.reg.AddForwardGrant(idA, idD, 22, 22); err != nil {
		t.Fatal(err)
	}
	expectForwardRelayed(t, srv, agentA, agentD, idD, "s-granted-d", 22)

	// Both refused targets must be silent. These drains come last because a
	// timed-out read leaves the socket unusable.
	expectNoAgentMessage(t, agentB, 300*time.Millisecond)
	expectNoAgentMessage(t, agentC, 300*time.Millisecond)
}

// TestForwardWildcardGrantAllowsAnyTargetInRange checks that a "*" target grant
// covers every device but still honours the inclusive port range.
func TestForwardWildcardGrantAllowsAnyTargetInRange(t *testing.T) {
	srv, _, baseWS := newForwardTestServer(t)
	agentA, idA := enrollTestAgent(t, srv, baseWS)
	agentB, idB := enrollTestAgent(t, srv, baseWS)
	agentC, idC := enrollTestAgent(t, srv, baseWS)
	if err := srv.reg.SetForwardPolicy(idA, ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}
	if err := srv.reg.AddForwardGrant(idA, "*", 8000, 9000); err != nil {
		t.Fatal(err)
	}

	for _, allowed := range []struct {
		agent  *websocket.Conn
		device string
		port   int
	}{
		{agentB, idB, 8080},
		{agentC, idC, 8000},
		{agentB, idB, 9000},
	} {
		expectForwardRelayed(t, srv, agentA, allowed.agent, allowed.device, fmt.Sprintf("s-%d", allowed.port), allowed.port)
	}
	expectForwardRefused(t, srv, agentA, idB, "s-below", 7999)
	expectForwardRefused(t, srv, agentA, idC, "s-above", 9001)

	// The out-of-range requests must not have reached either target. These
	// drains come last because a timed-out read leaves the socket unusable.
	expectNoAgentMessage(t, agentB, 300*time.Millisecond)
	expectNoAgentMessage(t, agentC, 300*time.Millisecond)
}

// TestDeviceJSONAlwaysIncludesForwardPolicy pins the API contract: the field the
// console types as required is serialized even when it holds the default.
func TestDeviceJSONAlwaysIncludesForwardPolicy(t *testing.T) {
	srv, ts, baseWS := newForwardTestServer(t)
	_, idA := enrollTestAgent(t, srv, baseWS)

	get := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	patch := func(id, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/devices/"+id, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer admin-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := get("/api/devices")
	defer func() { _ = resp.Body.Close() }()
	var devices []map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected one device, got %+v", devices)
	}
	raw, ok := devices[0]["forwardPolicy"]
	if !ok {
		t.Fatal("device response is missing forwardPolicy, which the console types as required")
	}
	if string(raw) != `"`+ForwardPolicyAny+`"` {
		t.Fatalf("fresh device should report %q, got %s", ForwardPolicyAny, raw)
	}

	patched := patch(idA, `{"forwardPolicy":"restricted"}`)
	defer func() { _ = patched.Body.Close() }()
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch policy returned %d", patched.StatusCode)
	}
	var updated map[string]json.RawMessage
	if err := json.NewDecoder(patched.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if string(updated["forwardPolicy"]) != `"`+ForwardPolicyRestricted+`"` {
		t.Fatalf("patch response should report the new policy, got %s", updated["forwardPolicy"])
	}
	if dev, _ := srv.reg.Get(idA); dev.ForwardPolicy != ForwardPolicyRestricted {
		t.Fatalf("policy was not persisted: %+v", dev)
	}

	invalid := patch(idA, `{"forwardPolicy":"sometimes"}`)
	defer func() { _ = invalid.Body.Close() }()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid policy should be rejected with 400, got %d", invalid.StatusCode)
	}
	if dev, _ := srv.reg.Get(idA); dev.ForwardPolicy != ForwardPolicyRestricted {
		t.Fatalf("a rejected patch must not change the policy: %+v", dev)
	}
}

// TestForwardPolicyAndGrantsAPI drives the admin-only forwarding endpoints over
// the real HTTP router and pins their JSON contract.
func TestForwardPolicyAndGrantsAPI(t *testing.T) {
	srv, ts, baseWS := newForwardTestServer(t)
	_, idA := enrollTestAgent(t, srv, baseWS)
	_, idB := enrollTestAgent(t, srv, baseWS)

	call := func(method, path, token, body string) (*http.Response, string) {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(raw)
	}

	if resp, _ := call(http.MethodGet, "/api/forward-policy", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated policy read should be 401, got %d", resp.StatusCode)
	}
	if resp, _ := call(http.MethodGet, "/api/forward-grants", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated grant read should be 401, got %d", resp.StatusCode)
	}

	// An operator is authenticated but not an admin.
	if err := srv.reg.CreateUser("operator1", "pw", "operator", []string{idA}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	loginBody, _ := json.Marshal(map[string]string{"username": "operator1", "password": "pw"})
	loginResp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	_ = loginResp.Body.Close()
	if session.Token == "" {
		t.Fatal("operator login failed")
	}
	if resp, _ := call(http.MethodGet, "/api/forward-policy", session.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator policy read should be 403, got %d", resp.StatusCode)
	}
	if resp, _ := call(http.MethodGet, "/api/forward-grants", session.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator grant read should be 403, got %d", resp.StatusCode)
	}

	// Every device is reported with a policy and a grant count, never null.
	resp, body := call(http.MethodGet, "/api/forward-policy", "admin-token", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("policy read returned %d: %s", resp.StatusCode, body)
	}
	var policies []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &policies); err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected two devices in the policy list, got %s", body)
	}
	for _, p := range policies {
		if _, ok := p["forwardPolicy"]; !ok {
			t.Fatalf("policy list entry is missing forwardPolicy: %v", p)
		}
		if _, ok := p["grantCount"]; !ok {
			t.Fatalf("policy list entry is missing grantCount: %v", p)
		}
	}

	// An empty grant list must serialize as [] rather than null.
	if _, body := call(http.MethodGet, "/api/forward-grants", "admin-token", ""); strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty grant list should be [], got %s", body)
	}

	// Creating a grant returns the stored row, timestamps included.
	createBody := fmt.Sprintf(`{"sourceDevice":%q,"targetDevice":%q,"portMin":22,"portMax":22}`, idA, idB)
	resp, body = call(http.MethodPost, "/api/forward-grants", "admin-token", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("grant create returned %d: %s", resp.StatusCode, body)
	}
	var created ForwardGrant
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	if created.SourceDevice != idA || created.TargetDevice != idB || created.PortMin != 22 || created.PortMax != 22 || created.CreatedAt.IsZero() {
		t.Fatalf("unexpected created grant: %+v", created)
	}

	_, body = call(http.MethodGet, "/api/forward-grants", "admin-token", "")
	var listed []ForwardGrant
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].SourceDevice != idA {
		t.Fatalf("expected the created grant in the list, got %s", body)
	}
	if _, body := call(http.MethodGet, "/api/forward-grants?sourceDevice="+idB, "admin-token", ""); strings.TrimSpace(body) != "[]" {
		t.Fatalf("filtering by a source without grants should be [], got %s", body)
	}

	// Invalid grants are refused before they reach the database.
	badBody := fmt.Sprintf(`{"sourceDevice":%q,"targetDevice":%q,"portMin":0,"portMax":0}`, idA, idB)
	if resp, body := call(http.MethodPost, "/api/forward-grants", "admin-token", badBody); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid grant should be 400, got %d: %s", resp.StatusCode, body)
	}
	if resp, _ := call(http.MethodPut, "/api/forward-grants", "admin-token", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method should be 405, got %d", resp.StatusCode)
	}

	// The grant authorizes the restricted source only inside its range.
	if err := srv.reg.SetForwardPolicy(idA, ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}
	if !srv.reg.ForwardAllowed(idA, idB, 22) || srv.reg.ForwardAllowed(idA, idB, 23) {
		t.Fatal("the created grant should authorize only port 22")
	}

	// Deleting by query string, then by JSON body, removes the grant.
	deleteTarget := fmt.Sprintf("/api/forward-grants?sourceDevice=%s&targetDevice=%s&portMin=22&portMax=22", idA, idB)
	if resp, body := call(http.MethodDelete, deleteTarget, "admin-token", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("grant delete returned %d: %s", resp.StatusCode, body)
	}
	if resp, _ := call(http.MethodDelete, deleteTarget, "admin-token", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting a missing grant should be 404, got %d", resp.StatusCode)
	}
	if resp, _ := call(http.MethodPost, "/api/forward-grants", "admin-token", createBody); resp.StatusCode != http.StatusCreated {
		t.Fatalf("recreating the grant returned %d", resp.StatusCode)
	}
	if resp, body := call(http.MethodDelete, "/api/forward-grants", "admin-token", createBody); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deleting by body returned %d: %s", resp.StatusCode, body)
	}
	if _, body := call(http.MethodGet, "/api/forward-grants", "admin-token", ""); strings.TrimSpace(body) != "[]" {
		t.Fatalf("grant list should be empty after deletion, got %s", body)
	}
	if entry, ok := findAudit(srv.reg.ListAudit(50), "forward-grant.create"); !ok || !strings.Contains(entry.Detail, "22-22") {
		t.Fatalf("grant creation should be audited, got %+v", entry)
	}
	if _, ok := findAudit(srv.reg.ListAudit(50), "forward-grant.delete"); !ok {
		t.Fatal("grant deletion should be audited")
	}
}

func TestScreenRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "screen box",
		Hostname: "screen-box",
		OS:       "windows",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/screen?token=admin-token&device="+key.DeviceID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browserWS.Close() }()
	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeRTCFallback}); err != nil {
		t.Fatal(err)
	}

	if relay := readAgentMessage(t, agentWS); relay.Type != protocol.TypeRTCRelay {
		t.Fatalf("expected relay switch, got %+v", relay)
	}
	start := readAgentMessage(t, agentWS)
	if start.Type != protocol.TypeScreenStart || start.SessionID == "" {
		t.Fatalf("unexpected screen start: %+v", start)
	}

	linkWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/screen-link?token="+key.Token+"&device="+key.DeviceID+"&session="+start.SessionID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = linkWS.Close() }()
	var relayStatus protocol.Message
	if err := browserWS.ReadJSON(&relayStatus); err != nil || relayStatus.Type != protocol.TypeRTCRelay {
		t.Fatalf("expected browser relay status, got %+v err=%v", relayStatus, err)
	}

	if err := browserWS.WriteMessage(websocket.BinaryMessage, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	mt, data, err := linkWS.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "abc" {
		t.Fatalf("expected relayed abc, got mt=%d data=%q err=%v", mt, data, err)
	}

	if err := linkWS.WriteMessage(websocket.BinaryMessage, []byte("def")); err != nil {
		t.Fatal(err)
	}
	mt, data, err = browserWS.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "def" {
		t.Fatalf("expected relayed def, got mt=%d data=%q err=%v", mt, data, err)
	}
}

func TestAgentConfigEndpoint(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:  "admin-token",
		DataDir:     t.TempDir(),
		AgentListen: ":18443",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/agent-config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var cfg struct {
		AgentWSS string `json:"agentWSS"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.AgentWSS, ":18443/ws/agent") {
		t.Fatalf("unexpected agent endpoint: %s", cfg.AgentWSS)
	}
}

func TestFileManagerRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "files box",
		Hostname: "files-box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/files?token=admin-token&device="+key.DeviceID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browserWS.Close() }()

	if err := browserWS.WriteJSON(protocol.Message{Type: protocol.TypeFileList, Path: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	list := readAgentMessage(t, agentWS)
	if list.Type != protocol.TypeFileList || list.Path != "/tmp" {
		t.Fatalf("unexpected file:list forwarded to agent: %+v", list)
	}
	if err := agentWS.WriteJSON(protocol.Message{
		Type:      protocol.TypeFileListResult,
		SessionID: list.SessionID,
		Path:      "/tmp",
		Entries:   []protocol.FileEntry{{Name: "notes.txt", Path: "/tmp/notes.txt", Size: 12}},
	}); err != nil {
		t.Fatal(err)
	}
	var result protocol.Message
	if err := browserWS.ReadJSON(&result); err != nil {
		t.Fatal(err)
	}
	if result.Type != protocol.TypeFileListResult || len(result.Entries) != 1 {
		t.Fatalf("unexpected file list result: %+v", result)
	}
}

// TestFileManagerRejectsUngrantedDevice covers the authorization gate on the
// file manager channel: an operator scoped to one device must not be able to
// browse or mutate another device's filesystem.
func TestFileManagerRejectsUngrantedDevice(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if _, err := srv.reg.Upsert("dev-a", "a", "a", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	// dev-a is granted to the operator, so it needs a live agent; dev-b is only
	// ever addressed to prove the authorization gate rejects it first.
	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "scoped box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})
	grantedID := key.DeviceID

	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}

	adminToken := login("admin", "secret")
	createBody := []byte(`{"username":"operator1","password":"pw","role":"operator","deviceIds":["` + grantedID + `"]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user failed: %d", resp.StatusCode)
	}
	opToken := login("operator1", "pw")

	// The operator is scoped to one device, so another device must be refused.
	_, deniedResp, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/files?token="+opToken+"&device=dev-not-granted", nil,
	)
	if err == nil {
		t.Fatal("operator without a grant should not open the file manager")
	}
	if deniedResp == nil || deniedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for an ungranted device, got %+v", deniedResp)
	}

	// The granted device still works.
	grantedWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/files?token="+opToken+"&device="+grantedID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = grantedWS.Close() }()
}

// TestServerRejectsEmptyAdminToken pins the fail-closed behaviour: an empty
// admin token would compare equal to an empty request credential.
// TestDeletedUserSessionDies is a regression test: a session used to survive
// the deletion of its account, because the lookup failure fell through to a
// successful return and the token stayed valid until its 24h expiry.
func TestDeletedUserSessionDies(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.Token
	}
	get := func(token, path string) int {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	adminToken := login("admin", "secret")
	createReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users",
		bytes.NewReader([]byte(`{"username":"victim","password":"pw123456","role":"operator","deviceIds":[]}`)))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create user failed: %d", createResp.StatusCode)
	}

	opToken := login("victim", "pw123456")
	if code := get(opToken, "/api/devices"); code != http.StatusOK {
		t.Fatalf("the operator session should work before deletion, got %d", code)
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/users/victim", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete user failed: %d", deleteResp.StatusCode)
	}

	if code := get(opToken, "/api/devices"); code != http.StatusUnauthorized {
		t.Fatalf("a deleted account's session must be rejected with 401, got %d", code)
	}
	// Deleting the account has to invalidate it as a credential too.
	body, _ := json.Marshal(map[string]string{"username": "victim", "password": "pw123456"})
	loginResp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a deleted account must not log in again, got %d", loginResp.StatusCode)
	}
}

func TestServerRejectsEmptyAdminToken(t *testing.T) {
	if _, err := NewServer(Config{DataDir: t.TempDir()}); err == nil {
		t.Fatal("NewServer must refuse an empty admin token")
	}
	if _, err := NewServer(Config{DataDir: t.TempDir(), AdminToken: "   "}); err == nil {
		t.Fatal("NewServer must refuse a whitespace-only admin token")
	}
	srv, err := NewServer(Config{DataDir: t.TempDir(), AdminToken: "admin-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if srv.validAdmin("") {
		t.Fatal("an empty credential must never validate as admin")
	}
	if !srv.validAdmin("admin-token") {
		t.Fatal("the configured token must validate")
	}
}

// TestUpdateUserExpiryPatchAllowsNull is a regression test for a nil
// dereference: PATCHing {"expiresAt":null} used to crash the handler, and an
// empty string was rejected with a 400, so an expiry could never be cleared.
func TestUpdateUserExpiryPatchAllowsNull(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	patch := func(body string) int {
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/users/operator1", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer admin-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	createReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users",
		strings.NewReader(`{"username":"operator1","password":"pw123456","role":"operator","deviceIds":[],"expiresAt":"2030-01-01T00:00:00Z"}`))
	createReq.Header.Set("Authorization", "Bearer admin-token")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create user failed: %d", createResp.StatusCode)
	}

	if code := patch(`{"expiresAt":null}`); code != http.StatusOK {
		t.Fatalf("clearing the expiry with null should succeed, got %d", code)
	}
	user, err := srv.reg.GetUser("operator1")
	if err != nil {
		t.Fatal(err)
	}
	if !user.ExpiresAt.IsZero() {
		t.Fatalf("expiry should be cleared, got %v", user.ExpiresAt)
	}

	// An omitted field must not clear an existing expiry.
	if code := patch(`{"expiresAt":"2031-05-05T00:00:00Z"}`); code != http.StatusOK {
		t.Fatalf("setting an expiry should succeed, got %d", code)
	}
	if code := patch(`{"role":"operator"}`); code != http.StatusOK {
		t.Fatalf("a patch without expiresAt should succeed, got %d", code)
	}
	user, err = srv.reg.GetUser("operator1")
	if err != nil {
		t.Fatal(err)
	}
	if user.ExpiresAt.IsZero() {
		t.Fatal("a patch that omits expiresAt must leave the existing expiry alone")
	}

	// A malformed value is a client error, not a crash.
	if code := patch(`{"expiresAt":"not-a-date"}`); code != http.StatusBadRequest {
		t.Fatalf("a malformed expiresAt should be a 400, got %d", code)
	}
}

// TestFileManagerRejectsPrivilegedMessageTypes ensures the manager channel is
// not a generic passthrough to the agent: terminal, tunnel and upload frames
// must be refused there even for a fully granted session.
func TestFileManagerRejectsPrivilegedMessageTypes(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	key := newTestKey(t, srv)
	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentWS.Close() }()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "files box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		dev, ok := srv.reg.Get(key.DeviceID)
		return ok && dev.Online
	})

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/files?token=admin-token&device="+key.DeviceID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browserWS.Close() }()

	for _, forbidden := range []string{
		protocol.TypeTermStart,
		protocol.TypeFileUpload,
		protocol.TypeForwardConnect,
	} {
		if err := browserWS.WriteJSON(protocol.Message{Type: forbidden, Path: "/tmp"}); err != nil {
			t.Fatal(err)
		}
		var reply protocol.Message
		if err := browserWS.ReadJSON(&reply); err != nil {
			t.Fatal(err)
		}
		if reply.Type != protocol.TypeFileError {
			t.Fatalf("type %s should be rejected with file:error, got %+v", forbidden, reply)
		}
	}

	// The agent must never have observed any of the rejected frames.
	_ = agentWS.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var leaked protocol.Message
	if err := agentWS.ReadJSON(&leaked); err == nil {
		t.Fatalf("forbidden message reached the agent: %+v", leaked)
	}
}

// TestEntityListNotEmptyContracts pins the JSON contract: list endpoints must
// serialize an empty array, never null, so the console can render them.
func TestEntityListNotEmptyContracts(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken: "admin-token",
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/devices", "/api/users", "/api/device-keys", "/api/invites", "/api/audit", "/api/forward-policy", "/api/forward-grants"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, resp.StatusCode, body)
		}
		if strings.TrimSpace(string(body)) == "null" {
			t.Fatalf("%s returned null instead of an empty array", path)
		}
	}
}

func TestUserDevicePermission(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if _, err := srv.reg.Upsert("dev-a", "a", "a", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.reg.Upsert("dev-b", "b", "b", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}

	adminToken := login("admin", "secret")
	createBody := []byte(`{"username":"operator1","password":"pw","role":"operator","deviceIds":["dev-a"]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user failed: %d", resp.StatusCode)
	}

	opToken := login("operator1", "pw")
	devReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/devices", nil)
	devReq.Header.Set("Authorization", "Bearer "+opToken)
	devResp, err := http.DefaultClient.Do(devReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devResp.Body.Close() }()
	var devices []Device
	if err := json.NewDecoder(devResp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != "dev-a" {
		t.Fatalf("operator should only see dev-a, got %+v", devices)
	}

	usersReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/users", nil)
	usersReq.Header.Set("Authorization", "Bearer "+opToken)
	usersResp, err := http.DefaultClient.Do(usersReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = usersResp.Body.Close()
	if usersResp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator should not manage users, got %d", usersResp.StatusCode)
	}
}

func TestUserListIncludesEmptyDeviceIDs(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:    "admin-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var users []map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	if len(users) == 0 {
		t.Fatal("expected at least the seeded admin user")
	}
	for _, u := range users {
		raw, ok := u["deviceIds"]
		if !ok {
			t.Fatalf("user response is missing deviceIds: %s", u["username"])
		}
		if string(raw) != "[]" {
			t.Fatalf("empty deviceIds should serialize as [], got %s", raw)
		}
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func readAgentMessage(t *testing.T, ws *websocket.Conn) protocol.Message {
	t.Helper()
	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != protocol.TypePing {
			return msg
		}
	}
}
