package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func TestManagementDeviceContract(t *testing.T) {
	s, err := NewServer(Config{AdminToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	key, err := s.reg.CreateDeviceKey("workstation", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.reg.Upsert(key.DeviceID, "workstation", "host", "linux", "amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, req)
		return out
	}
	// Group edits must distinguish omission from explicitly clearing the group.
	for _, body := range []string{`{"group":"Home"}`, `{"name":"renamed"}`, `{"group":""}`} {
		out := request(http.MethodPatch, "/api/devices/"+key.DeviceID, body, "test-token")
		if out.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", out.Code, out.Body.String())
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["capabilities"]; !ok {
			t.Fatal("capabilities must be explicitly null for agents without status")
		}
		d, _ := s.reg.Get(key.DeviceID)
		if body == `{"name":"renamed"}` && d.Group != "Home" {
			t.Fatal("omitted group changed existing value")
		}
		if body == `{"group":""}` && d.Group != "" {
			t.Fatal("empty group did not clear")
		}
	}
	out := request(http.MethodGet, "/api/devices/"+key.DeviceID, "", "test-token")
	if out.Code != http.StatusOK {
		t.Fatalf("get: %d", out.Code)
	}
	if err := s.reg.CreateUser("operator", "testing-password", "operator", []string{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	s.sessions["operator-session"] = sessionEntry{username: "operator", expiresAt: time.Now().Add(time.Hour)}
	if out := request(http.MethodGet, "/api/devices/"+key.DeviceID, "", "operator-session"); out.Code != http.StatusForbidden {
		t.Fatalf("unauthorized device exposed: %d", out.Code)
	}
}

func TestCapabilitySnapshotAndLegacyAgents(t *testing.T) {
	s, err := NewServer(Config{AdminToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.reg.Close(); close(s.stop) }()
	s.agents["device"] = &agentConn{}
	s.routeAgentMessage("device", protocol.Message{Type: protocol.TypeStatus, Capabilities: &protocol.DeviceCapabilities{Terminal: true, Files: true, Desktop: false, DesktopHint: "Set up VNC"}})
	d := Device{ID: "device", Online: true}
	s.attachCapabilities(&d)
	if d.Capabilities == nil || !d.Capabilities.Terminal || d.Capabilities.Desktop {
		t.Fatalf("unexpected capabilities: %+v", d.Capabilities)
	}
	s.routeAgentMessage("device", protocol.Message{Type: protocol.TypeStatus, Capabilities: &protocol.DeviceCapabilities{Terminal: true, Files: true, Desktop: true}})
	if d.Capabilities.Desktop {
		t.Fatal("snapshot changed after later report")
	}
	s.attachCapabilities(&d)
	if !d.Capabilities.Desktop {
		t.Fatal("new capability report was not applied")
	}
	legacy := Device{ID: "legacy"}
	s.attachCapabilities(&legacy)
	if legacy.Capabilities != nil {
		t.Fatal("must not invent capabilities for legacy agent")
	}
}

func TestAgentDownloadsAuthorizationAndPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "warpmesh-agent-linux-amd64"), []byte("test-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{AdminToken: "test-token", AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/api/agent-downloads", "", 401}, {"/api/agent-downloads/linux-amd64", "", 401},
		{"/api/agent-downloads", "test-token", 200}, {"/api/agent-downloads/linux-amd64", "test-token", 200},
		{"/api/agent-downloads/darwin-arm64", "test-token", 404}, {"/api/agent-downloads/not-a-platform", "test-token", 404},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, req)
		if out.Code != tc.status {
			t.Fatalf("%s: want %d got %d", tc.path, tc.status, out.Code)
		}
		if tc.path == "/api/agent-downloads/linux-amd64" && tc.status == 200 && (out.Body.String() != "test-binary" || out.Header().Get("Content-Disposition") == "") {
			t.Fatal("download body or attachment header missing")
		}
	}
}

func TestAccountEditProtectsLastAdminAndClearsExpiry(t *testing.T) {
	s, err := NewServer(Config{AdminToken: "test-token", AdminUser: "admin", AdminPassword: "qa-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/users/admin", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, req)
		return out
	}
	if out := patch(`{"role":"operator"}`); out.Code != http.StatusConflict {
		t.Fatalf("last admin demotion: %d", out.Code)
	}
	if out := patch(`{"role":"unknown"}`); out.Code != http.StatusBadRequest {
		t.Fatalf("invalid role: %d", out.Code)
	}
	for _, body := range []string{`{"expiresAt":"2030-12-31T00:00:00Z"}`, `{"expiresAt":null}`} {
		if out := patch(body); out.Code != http.StatusOK {
			t.Fatalf("expiry update: %d %s", out.Code, out.Body.String())
		}
	}
	user, err := s.reg.GetUser("admin")
	if err != nil || !user.ExpiresAt.IsZero() || user.Role != "admin" || user.DeviceIDs == nil {
		t.Fatalf("unexpected user after expiry clear: %+v %v", user, err)
	}
	if out := patch(`{"expiresAt":""}`); out.Code != http.StatusBadRequest {
		t.Fatalf("empty string timestamp should reject: %d", out.Code)
	}
}

func TestDeviceRenameAndGroupSurviveAgentReconnect(t *testing.T) {
	reg, err := NewRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()
	if _, err := reg.Upsert("device", "agent-name", "old-host", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Rename("device", "Managed name"); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetGroup("device", "Home"); err != nil {
		t.Fatal(err)
	}
	device, err := reg.Upsert("device", "agent-name", "new-host", "linux", "amd64", []string{"10.0.0.3"})
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "Managed name" || device.Group != "Home" || device.Hostname != "new-host" || !device.Online {
		t.Fatalf("reconnect lost inventory edits: %+v", device)
	}
}
