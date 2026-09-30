package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLoginSetsSessionCookieAndCookieAuthenticates pins the browser session
// path: login must set an HttpOnly cookie, and that cookie alone must
// authenticate subsequent API calls so the frontend never needs the token.
func TestLoginSetsSessionCookieAndCookieAuthenticates(t *testing.T) {
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

	body := strings.NewReader(`{"username":"admin","password":"secret"}`)
	resp, err := http.Post(ts.URL+"/api/login", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}

	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	if !sessionCookie.HttpOnly {
		t.Error("the session cookie must be HttpOnly so scripts cannot read it")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Error("the session cookie must be SameSite=Strict to blunt CSRF")
	}
	if sessionCookie.Value == "" {
		t.Fatal("session cookie carries no value")
	}

	// The cookie alone (no Authorization header, no ?token=) must authenticate.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/devices", nil)
	req.AddCookie(sessionCookie)
	got, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("cookie did not authenticate: %d", got.StatusCode)
	}

	// Logout must clear it.
	lo, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/logout", nil)
	lo.AddCookie(sessionCookie)
	loResp, err := http.DefaultClient.Do(lo)
	if err != nil {
		t.Fatal(err)
	}
	_ = loResp.Body.Close()
	cleared := false
	for _, c := range loResp.Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout must expire the session cookie")
	}
}

// TestCookieSessionRejectsCrossOriginMutations covers the CSRF guard: a
// state-changing request carrying the session cookie from another origin must
// be refused, while a header-authenticated caller is unaffected.
func TestCookieSessionRejectsCrossOriginMutations(t *testing.T) {
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

	loginBody := strings.NewReader(`{"username":"admin","password":"secret"}`)
	resp, err := http.Post(ts.URL+"/api/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}

	post := func(origin string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users",
			strings.NewReader(`{"username":"x","password":"pw123456","role":"operator","deviceIds":[]}`))
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r.StatusCode
	}

	if code := post("https://evil.example"); code != http.StatusUnauthorized {
		t.Fatalf("a cross-origin cookie request must be rejected, got %d", code)
	}
	if code := post(ts.URL); code != http.StatusCreated {
		t.Fatalf("a same-origin cookie request must succeed, got %d", code)
	}
}

// TestWSTicketIsSingleUseAndBoundToResource covers the ticket mechanism that
// header-authenticated clients can use to authorize an upgrade without
// embedding a long-lived credential.
func TestWSTicketIsSingleUseAndBoundToResource(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	issue := func(device, kind string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket?device="+device+"&kind="+kind, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec := httptest.NewRecorder()
		srv.handleWSTicket(rec, req)
		return rec
	}

	rec := issue("dev-a", "terminal")
	if rec.Code != http.StatusOK {
		t.Fatalf("ticket issue failed: %d %s", rec.Code, rec.Body.String())
	}
	var issued struct {
		Ticket   string `json:"ticket"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Ticket == "" || !strings.HasPrefix(issued.Protocol, wsProtocolPrefix) {
		t.Fatalf("unexpected ticket payload: %+v", issued)
	}

	// A ticket is consumed exactly once.
	if _, ok := srv.tickets.consume(issued.Ticket, "dev-a", "terminal"); !ok {
		t.Fatal("a freshly issued ticket must be accepted")
	}
	if _, ok := srv.tickets.consume(issued.Ticket, "dev-a", "terminal"); ok {
		t.Fatal("a ticket must not be reusable")
	}

	// It is bound to the device and kind it was issued for.
	other := issue("dev-b", "terminal")
	var second struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(other.Body.Bytes(), &second)
	if _, ok := srv.tickets.consume(second.Ticket, "dev-c", "terminal"); ok {
		t.Fatal("a ticket must not authorize a different device")
	}
	third := issue("dev-d", "terminal")
	var t3 struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(third.Body.Bytes(), &t3)
	if _, ok := srv.tickets.consume(t3.Ticket, "dev-d", "desktop"); ok {
		t.Fatal("a ticket must not authorize a different session kind")
	}
}

// TestWSTicketRejectsBadRequests covers the ticket endpoint's validation.
func TestWSTicketRejectsBadRequests(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	cases := []struct {
		name   string
		method string
		target string
		token  string
		want   int
	}{
		{"missing token", http.MethodPost, "/api/ws-ticket?device=d&kind=terminal", "", http.StatusUnauthorized},
		{"missing device", http.MethodPost, "/api/ws-ticket?kind=terminal", "admin-token", http.StatusBadRequest},
		{"unknown kind", http.MethodPost, "/api/ws-ticket?device=d&kind=shell", "admin-token", http.StatusBadRequest},
		{"wrong method", http.MethodGet, "/api/ws-ticket?device=d&kind=terminal", "admin-token", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			srv.handleWSTicket(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
