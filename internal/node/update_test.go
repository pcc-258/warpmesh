package node

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func updateTestNode(t *testing.T) *Node {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Config{
		ServerURL: "https://relay.example/ws/node",
		Token:     "node-token",
		DeviceID:  "dev-test",
		DataDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func updateServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(payload)
	}))
}

func TestNodeUpdateRejectsSHA256Mismatch(t *testing.T) {
	a := updateTestNode(t)
	server := updateServer(t, []byte("wrong payload"))
	defer server.Close()
	task := protocol.Message{
		Type:          protocol.TypeNodeUpdate,
		UpdateID:      "update-1",
		UpdateVersion: "v9.9.9",
		UpdateSHA256:  strings.Repeat("0", 64),
		UpdateURL:     server.URL,
	}
	a.handleUpdate(task)
	assertUpdateStagedFile(t, a, false)
}

func TestNodeUpdateRejectsInvalidTaskAndURL(t *testing.T) {
	a := updateTestNode(t)
	for _, task := range []protocol.Message{
		{Type: protocol.TypeNodeUpdate, UpdateID: "missing-fields"},
		{Type: protocol.TypeNodeUpdate, UpdateID: "bad-sha", UpdateVersion: "v2", UpdateURL: "https://example.test/a", UpdateSHA256: strings.Repeat("x", 64)},
		{Type: protocol.TypeNodeUpdate, UpdateID: "bad-scheme", UpdateVersion: "v2", UpdateURL: "file:///tmp/node", UpdateSHA256: strings.Repeat("a", 64)},
	} {
		a.handleUpdate(task)
	}
	assertUpdateStagedFile(t, a, false)
}

func TestNodeUpdateURLResolution(t *testing.T) {
	a := updateTestNode(t)
	got, err := a.updateURL("/api/node-update/download?device=dev-test&platform=linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://relay.example/api/node-update/download?device=dev-test&platform=linux-amd64" {
		t.Fatalf("resolved URL=%q", got)
	}
	if _, err := a.updateURL("http://example.com/node"); err == nil {
		t.Fatal("expected remote plain HTTP URL to be rejected")
	}
	loopback, err := a.updateURL("http://127.0.0.1:8080/api/node")
	if err != nil || loopback != "http://127.0.0.1:8080/api/node" {
		t.Fatalf("loopback URL=%q err=%v", loopback, err)
	}
}

func TestNodeUpdateSHA256Validation(t *testing.T) {
	payload := []byte("node")
	sum := sha256.Sum256(payload)
	if !isSHA256Hex(hex.EncodeToString(sum[:])) {
		t.Fatal("valid SHA256 rejected")
	}
	for _, value := range []string{"", "abc", strings.Repeat("x", 64)} {
		if isSHA256Hex(value) {
			t.Fatalf("invalid SHA256 accepted: %q", value)
		}
	}
}

func assertUpdateStagedFile(t *testing.T, a *Node, want bool) {
	t.Helper()
	path := filepath.Join(a.cfg.DataDir, "updates", "warpmesh-node.next")
	_, err := os.Stat(path)
	if got := err == nil; got != want {
		t.Fatalf("staged file exists=%v want=%v err=%v", got, want, err)
	}
}
