package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
