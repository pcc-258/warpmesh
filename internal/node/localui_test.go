package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalUIForwardFieldContract(t *testing.T) {
	a := testNode(t)
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/forwards", nil)
	w := httptest.NewRecorder()
	a.uiForwards(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"forwards":[]`) {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}
	raw, err := json.Marshal(ForwardSpec{LocalPort: 2222, TargetDeviceID: "device", TargetPort: 22})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"localPort", "targetDeviceId", "targetPort"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("missing field %s: %s", field, raw)
		}
	}
	var decoded ForwardSpec
	if err := json.Unmarshal([]byte(`{"localPort":2222,"targetDeviceId":"device","targetPort":22}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LocalPort != 2222 || decoded.TargetDeviceID != "device" || decoded.TargetPort != 22 {
		t.Fatalf("decode: %+v", decoded)
	}
	if strings.Contains(uiPage, "innerHTML") || !strings.Contains(uiPage, "textContent") {
		t.Fatal("untrusted forward target rendered as HTML")
	}
}

func TestLocalUIOverviewAndPeersContracts(t *testing.T) {
	a := testNode(t)
	a.cfg.ServerURL = "wss://relay.example.test/ws/node"
	a.cfg.Name = "Studio PC"
	if err := a.StartForward(ForwardSpec{LocalPort: 2222, TargetDeviceID: "dev-target", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.StopForward(2222) })

	overviewReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/overview", nil)
	overview := httptest.NewRecorder()
	a.uiOverview(overview, overviewReq)
	if overview.Code != http.StatusOK {
		t.Fatalf("overview status %d: %s", overview.Code, overview.Body.String())
	}
	var result struct {
		DeviceID     string   `json:"deviceId"`
		ConsoleURL   string   `json:"consoleUrl"`
		ForwardCount int      `json:"forwardCount"`
		AllowedPaths []string `json:"allowedPaths"`
		Connected    bool     `json:"connected"`
		DesktopHint  string   `json:"desktopHint"`
	}
	if err := json.Unmarshal(overview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.DeviceID == "" || result.ConsoleURL != "https://relay.example.test" {
		t.Fatalf("unexpected overview: %+v", result)
	}
	if result.ForwardCount != 1 || result.AllowedPaths == nil {
		t.Fatalf("overview arrays must be present and non-null: %+v", result)
	}

	peersReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/peers", nil)
	peers := httptest.NewRecorder()
	a.uiPeers(peers, peersReq)
	if peers.Code != http.StatusOK {
		t.Fatalf("peers status %d: %s", peers.Code, peers.Body.String())
	}
	var peerList []localPeer
	if err := json.Unmarshal(peers.Body.Bytes(), &peerList); err != nil {
		t.Fatal(err)
	}
	if len(peerList) != 1 || peerList[0].DeviceID != "dev-target" {
		t.Fatalf("unexpected peers: %+v", peerList)
	}
	if peerList[0].LocalPorts == nil || peerList[0].TargetPorts == nil {
		t.Fatalf("peer arrays must be non-null: %+v", peerList[0])
	}
}

func TestConsoleURLFromNodeURL(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"wss://relay.example/ws/node", "https://relay.example"},
		{"ws://127.0.0.1:8080/ws/node?x=1", "http://127.0.0.1:8080"},
		{"https://relay.example/custom", "https://relay.example"},
		{"", ""},
	} {
		if got := consoleURL(test.input); got != test.want {
			t.Errorf("consoleURL(%q)=%q want %q", test.input, got, test.want)
		}
	}
}

func TestLocalUIRejectsCrossOriginAndRebinding(t *testing.T) {
	for _, test := range []struct {
		name, host, origin, fetchSite string
		allowed                       bool
	}{
		{"same origin", "127.0.0.1:8090", "http://127.0.0.1:8090", "same-origin", true},
		{"localhost", "localhost:8090", "http://localhost:8090", "", true},
		{"CLI", "127.0.0.1:8090", "", "", true},
		{"foreign origin", "127.0.0.1:8090", "https://attacker.example", "", false},
		{"different port", "127.0.0.1:8090", "http://127.0.0.1:8091", "", false},
		{"null", "127.0.0.1:8090", "null", "", false},
		{"rebinding", "attacker.example:8090", "http://attacker.example:8090", "", false},
		{"cross-site", "127.0.0.1:8090", "", "cross-site", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/api/forwards", strings.NewReader(`{}`))
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", test.fetchSite)
			w := httptest.NewRecorder()
			if got := localUIRequest(w, r); got != test.allowed {
				t.Fatalf("allowed=%v expected=%v", got, test.allowed)
			}
			if !test.allowed && w.Code != http.StatusForbidden {
				t.Fatalf("status=%d", w.Code)
			}
			if !test.allowed {
				a := testNode(t)
				for _, handler := range []http.HandlerFunc{a.uiForwards, a.uiForwardByPort} {
					w := httptest.NewRecorder()
					handler(w, r)
					if w.Code != http.StatusForbidden {
						t.Fatalf("handler allowed hostile request: %d", w.Code)
					}
				}
			}
		})
	}
}
