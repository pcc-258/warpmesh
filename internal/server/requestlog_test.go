package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRedactQueryKeepsCredentialsOutOfLogs pins the property that request
// logging never writes a token, password or invite code to the journal.
func TestRedactQueryKeepsCredentialsOutOfLogs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"token", "token=abc123&device=dev-1", "token=REDACTED&device=dev-1"},
		{"password", "password=hunter2", "password=REDACTED"},
		{"invite code", "code=invite-secret", "code=REDACTED"},
		{"device key", "key=dev-key&name=box", "key=REDACTED&name=box"},
		{"nothing sensitive", "device=dev-1&cols=120", "device=dev-1&cols=120"},
		{"valueless token", "token", "token"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactQuery(tc.in); got != tc.want {
				t.Fatalf("redactQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// TestRequestLogRecordsWhatMatters verifies the request log keeps the events an
// operator needs (mutations, denials) and stays quiet for the successful reads
// the console polls every few seconds.
func TestRequestLogRecordsWhatMatters(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("successful GET is not logged", func(t *testing.T) {
		buf := captureLog(t)
		resp, err := http.Get(ts.URL + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if strings.Contains(buf.String(), "http.request") {
			t.Fatalf("a successful GET should not be logged, got %q", buf.String())
		}
	})

	t.Run("a denied request is logged with its status and redacted query", func(t *testing.T) {
		buf := captureLog(t)
		resp, err := http.Get(ts.URL + "/api/users?token=super-secret&device=dev-1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		out := buf.String()
		if !strings.Contains(out, "http.request") {
			t.Fatalf("an unauthenticated request should be logged, got %q", out)
		}
		if !strings.Contains(out, "status=401") {
			t.Fatalf("expected status=401 in %q", out)
		}
		if strings.Contains(out, "super-secret") {
			t.Fatalf("a credential leaked into the request log: %q", out)
		}
		if !strings.Contains(out, "token=REDACTED") {
			t.Fatalf("expected the token to be redacted in %q", out)
		}
	})

	t.Run("a mutation is logged", func(t *testing.T) {
		buf := captureLog(t)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/users", strings.NewReader(`{"username":"x"}`))
		req.Header.Set("Authorization", "Bearer admin-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		out := buf.String()
		if !strings.Contains(out, "http.request") || !strings.Contains(out, "method=POST") {
			t.Fatalf("a mutation should be logged, got %q", out)
		}
	})

	t.Run("websocket paths bypass the request log", func(t *testing.T) {
		buf := captureLog(t)
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ws/terminal", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if strings.Contains(buf.String(), "http.request") {
			t.Fatalf("/ws/ paths must not be logged as ordinary requests, got %q", buf.String())
		}
	})
}
