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

	"github.com/pcc-258/device-relay/internal/protocol"
)

func TestTerminalRelay(t *testing.T) {
	srv, err := NewServer(Config{
		AdminToken:  "admin-token",
		DeviceToken: "device-token",
		DataDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	baseWS := "ws" + strings.TrimPrefix(ts.URL, "http")

	agentWS, _, err := websocket.DefaultDialer.Dial(baseWS+"/ws/agent?token=device-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agentWS.Close()
	if err := agentWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: "dev-1",
		Name:     "test box",
		Hostname: "test-box",
		OS:       "linux",
		Arch:     "amd64",
	}); err != nil {
		t.Fatal(err)
	}

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
	defer resp.Body.Close()
	var devices []Device
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || !devices[0].Online {
		t.Fatalf("expected one online device, got %+v", devices)
	}

	browserWS, _, err := websocket.DefaultDialer.Dial(
		baseWS+"/ws/terminal?token=admin-token&device=dev-1&cols=80&rows=24", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer browserWS.Close()

	var start protocol.Message
	if err := agentWS.ReadJSON(&start); err != nil {
		t.Fatal(err)
	}
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
	var input protocol.Message
	if err := agentWS.ReadJSON(&input); err != nil {
		t.Fatal(err)
	}
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
		DeviceToken:   "device-token",
		DataDir:       t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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
	defer keyResp.Body.Close()
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
	defer agentWS.Close()
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
	defer statsResp.Body.Close()
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
	defer auditResp.Body.Close()
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
