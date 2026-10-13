package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pcc-258/warpmesh/internal/protocol"
	"github.com/pcc-258/warpmesh/internal/server"
)

// Exercise the same handler selection used by run, not just Server.Handler.
// The former omitted the agent routes on default single-port deployments.
func TestPrimaryHandlerAcceptsAgentsOnSingleListener(t *testing.T) {
	for _, tlsEnabled := range []bool{false, true} {
		name := "HTTP"
		if tlsEnabled {
			name = "HTTPS"
		}
		t.Run(name, func(t *testing.T) {
			srv, err := server.NewServer(server.Config{AdminToken: "static-admin"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			handler := primaryHandler(srv, false)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/device-keys", strings.NewReader(`{"name":"test-agent"}`))
			req.Header.Set("Authorization", "Bearer static-admin")
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create key: %d %s", rec.Code, rec.Body.String())
			}
			var key server.DeviceKey
			if err := json.Unmarshal(rec.Body.Bytes(), &key); err != nil {
				t.Fatal(err)
			}
			var ts *httptest.Server
			dialer := *websocket.DefaultDialer
			if tlsEnabled {
				ts = httptest.NewTLSServer(handler)
				dialer.TLSClientConfig = ts.Client().Transport.(*http.Transport).TLSClientConfig
			} else {
				ts = httptest.NewServer(handler)
			}
			defer ts.Close()
			resp, err := ts.Client().Get(ts.URL + "/api/agent-config")
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				AgentWSS string `json:"agentWSS"`
			}
			err = json.NewDecoder(resp.Body).Decode(&cfg)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/agent"
			if cfg.AgentWSS != want {
				t.Fatalf("discovery=%q, want %q", cfg.AgentWSS, want)
			}
			headers := http.Header{"Authorization": []string{"Bearer " + key.Token}}
			ws, handshake, err := dialer.Dial(cfg.AgentWSS, headers)
			if err != nil {
				t.Fatalf("agent upgrade: %v (%v)", err, handshake)
			}
			defer func() { _ = ws.Close() }()
			if err := ws.WriteJSON(protocol.Message{Type: protocol.TypeHello, DeviceID: key.DeviceID, Name: "test-agent"}); err != nil {
				t.Fatal(err)
			}
			// Verify an authenticated hello registered the device through the
			// HTTP inventory; an upgrade alone could still reject credentials.
			deadline := time.Now().Add(3 * time.Second)
			for {
				inventory := httptest.NewRecorder()
				get := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
				get.Header.Set("Authorization", "Bearer static-admin")
				handler.ServeHTTP(inventory, get)
				var devices []server.Device
				if err := json.Unmarshal(inventory.Body.Bytes(), &devices); err != nil {
					t.Fatal(err)
				}
				if len(devices) == 1 && devices[0].ID == key.DeviceID && devices[0].Online {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("agent did not register: %s", inventory.Body.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			link := httptest.NewRecorder()
			handler.ServeHTTP(link, httptest.NewRequest(http.MethodGet, "/ws/screen-link", nil))
			if link.Code != http.StatusUnauthorized {
				t.Fatalf("screen-link route missing: %d", link.Code)
			}
		})
	}
}

func TestSeparateListenerKeepsAgentRoutesOffWebPlane(t *testing.T) {
	srv, err := server.NewServer(server.Config{AdminToken: "static-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	for _, path := range []string{"/ws/agent", "/ws/screen-link"} {
		rec := httptest.NewRecorder()
		primaryHandler(srv, true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("web plane unexpectedly serves %s: %d", path, rec.Code)
		}
	}
	link := httptest.NewRecorder()
	srv.AgentHandler().ServeHTTP(link, httptest.NewRequest(http.MethodGet, "/ws/screen-link", nil))
	if link.Code != http.StatusUnauthorized {
		t.Fatalf("separate agent plane missing screen-link: %d", link.Code)
	}
}
