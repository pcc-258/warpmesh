package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalUIForwardFieldContract(t *testing.T) {
	a := testAgent(t)
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
	if strings.Contains(uiPage, "innerHTML") || !strings.Contains(uiPage, "target.textContent=") {
		t.Fatal("untrusted forward target rendered as HTML")
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
				a := testAgent(t)
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
