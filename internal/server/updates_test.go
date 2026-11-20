package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func TestNodeUpdateBuildMetadataAndTask(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "warpmesh-node-linux-amd64")
	payload := []byte("test node binary")
	if err := os.WriteFile(binary, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".version", []byte("v1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{AdminToken: "admin", DataDir: t.TempDir(), NodeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	key := newTestKey(t, srv)
	if _, err := srv.reg.Upsert(key.DeviceID, "node", "host", "test", "arch", nil, "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/node-updates", nil)
	request.Header.Set("Authorization", "Bearer admin")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("builds status=%d body=%s", response.Code, response.Body.String())
	}
	var builds []nodeUpdateBuild
	if err := json.Unmarshal(response.Body.Bytes(), &builds); err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 || builds[0].Version != "v1.2.3" {
		t.Fatalf("unexpected builds: %+v", builds)
	}
	sum := sha256.Sum256(payload)
	if builds[0].SHA256 != hex.EncodeToString(sum[:]) || builds[0].Size != int64(len(payload)) {
		t.Fatalf("unexpected metadata: %+v", builds[0])
	}
}

func TestNodeUpdateStateIsPersisted(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	key := newTestKey(t, srv)
	if _, err := srv.reg.Upsert(key.DeviceID, "node", "host", "linux", "amd64", nil, "v1"); err != nil {
		t.Fatal(err)
	}
	srv.routeNodeMessage(key.DeviceID, protocol.Message{
		Type:          protocol.TypeNodeUpdateState,
		UpdateState:   "staged",
		UpdateVersion: "v2",
		UpdateError:   "restart required",
		UpdateService: "warpmesh-node.service",
	})
	device, ok := srv.reg.Get(key.DeviceID)
	if !ok || device.UpdateState != "staged" || device.UpdateVersion != "v2" || device.UpdateService != "warpmesh-node.service" {
		t.Fatalf("update state was not persisted: %+v", device)
	}
}

func TestNodeUpdateDownloadRequiresNodeCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "warpmesh-node-linux-amd64"), []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{AdminToken: "admin", DataDir: t.TempDir(), NodeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	key := newTestKey(t, srv)
	request := httptest.NewRequest(http.MethodGet, "/api/node-update/download?device="+key.DeviceID+"&platform=linux-amd64", nil)
	request.Header.Set("Authorization", "Bearer "+key.Token)
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "binary" {
		t.Fatalf("download status=%d body=%q", response.Code, response.Body.String())
	}
}
