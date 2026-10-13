package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestLogoutRevokesPresentedCredential(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "static-admin", AdminUser: "admin", AdminPassword: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	for _, credential := range []string{"cookie", "bearer", "query"} {
		t.Run(credential, func(t *testing.T) {
			token := "session-" + credential
			srv.sessionsMu.Lock()
			srv.sessions[token] = sessionEntry{username: "admin", expiresAt: time.Now().Add(time.Hour)}
			srv.sessionsMu.Unlock()
			target := "http://console.test/api/logout"
			if credential == "query" {
				target += "?token=" + token
			}
			req := httptest.NewRequest(http.MethodPost, target, nil)
			switch credential {
			case "cookie":
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
				req.Header.Set("Origin", "http://console.test")
				// Cookie precedence must match authenticate, even if a stale
				// legacy header is also present in the request.
				req.Header.Set("Authorization", "Bearer stale-token")
			case "bearer":
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("logout: %d %s", rec.Code, rec.Body.String())
			}
			for _, replayKind := range []string{"cookie", "bearer", "query"} {
				replayURL := "http://console.test/api/devices"
				if replayKind == "query" {
					replayURL += "?token=" + token
				}
				replay := httptest.NewRequest(http.MethodGet, replayURL, nil)
				switch replayKind {
				case "cookie":
					replay.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
				case "bearer":
					replay.Header.Set("Authorization", "Bearer "+token)
				}
				got := httptest.NewRecorder()
				srv.Handler().ServeHTTP(got, replay)
				if got.Code != http.StatusUnauthorized {
					t.Fatalf("revoked %s token replayed via %s: %d", credential, replayKind, got.Code)
				}
			}
		})
	}
}

func TestLogoutRejectsGetAndCrossOriginWithoutRevoking(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "static-admin", AdminUser: "admin", AdminPassword: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	const token = "active-session"
	srv.sessions[token] = sessionEntry{username: "admin", expiresAt: time.Now().Add(time.Hour)}
	for _, tc := range []struct {
		method string
		origin string
		status int
	}{
		{http.MethodGet, "http://console.test", http.StatusMethodNotAllowed},
		{http.MethodPost, "https://evil.example", http.StatusForbidden},
	} {
		req := httptest.NewRequest(tc.method, "http://console.test/api/logout", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("logout %s origin=%s: %d, want %d", tc.method, tc.origin, rec.Code, tc.status)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("rejected logout must not clear the browser cookie")
		}
		if _, ok := srv.validSession(token); !ok {
			t.Fatal("rejected logout revoked the session")
		}
	}
}

func TestStatsRespectsDeviceAndAuditVisibility(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "static-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	for _, id := range []string{"visible", "hidden"} {
		if _, err := srv.reg.Upsert(id, id, id, "linux", "amd64", nil); err != nil {
			t.Fatal(err)
		}
		if err := srv.reg.SetGroup(id, id+"-group"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"operator", "unassigned"} {
		devices := []string{}
		if name == "operator" {
			devices = append(devices, "visible")
		}
		if err := srv.reg.CreateUser(name, "secret", "operator", devices, time.Time{}); err != nil {
			t.Fatal(err)
		}
		srv.sessions[name] = sessionEntry{username: name, expiresAt: time.Now().Add(time.Hour)}
	}
	if err := srv.reg.RecordAudit("admin", "device.rename", "hidden", "private-detail"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token  string
		total  int
		groups []string
		audits int
	}{
		{"static-admin", 2, []string{"hidden-group", "visible-group"}, 1},
		{"operator", 1, []string{"visible-group"}, 0},
		{"unassigned", 0, []string{}, 0},
	} {
		t.Run(tc.token, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
			}
			var stats struct {
				Total  int          `json:"total"`
				Groups []string     `json:"groups"`
				Recent []AuditEntry `json:"recentActivity"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
				t.Fatal(err)
			}
			if stats.Total != tc.total || !reflect.DeepEqual(stats.Groups, tc.groups) || len(stats.Recent) != tc.audits {
				t.Fatalf("unexpected stats: %+v", stats)
			}
			if stats.Groups == nil || stats.Recent == nil {
				t.Fatal("list fields must remain arrays, never null")
			}
		})
	}
}

func TestAgentConfigAdvertisesEffectiveEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		requestURL string
		listen     string
		want       string
	}{
		{"plain request port", "http://example.test:8080/api/agent-config", "", "ws://example.test:8080/ws/agent"},
		{"plain standard port", "http://example.test/api/agent-config", "", "ws://example.test:80/ws/agent"},
		{"TLS primary port", "https://example.test:8443/api/agent-config", ":8443", "wss://example.test:8443/ws/agent"},
		{"TLS standard port", "https://example.test/api/agent-config", "", "wss://example.test:443/ws/agent"},
		{"separate full address", "https://example.test/api/agent-config", "0.0.0.0:18443", "wss://example.test:18443/ws/agent"},
		{"IPv6", "https://[::1]:8443/api/agent-config", "[::]:18443", "wss://[::1]:18443/ws/agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{cfg: Config{AgentListen: tc.listen}}
			rec := httptest.NewRecorder()
			srv.handleAgentConfig(rec, httptest.NewRequest(http.MethodGet, tc.requestURL, nil))
			var cfg struct {
				AgentWSS string `json:"agentWSS"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.AgentWSS != tc.want {
				t.Fatalf("endpoint = %q, want %q", cfg.AgentWSS, tc.want)
			}
		})
	}
}
