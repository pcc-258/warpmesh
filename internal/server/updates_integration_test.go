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

	"github.com/gorilla/websocket"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func TestNodeUpdateTaskReachesNodeWithFixedMetadata(t *testing.T) {
	nodeDir := t.TempDir()
	binary := filepath.Join(nodeDir, "warpmesh-node-linux-amd64")
	if err := os.WriteFile(binary, []byte("new node"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'warpmesh-node v2.0.0 test test\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".version", []byte("v2.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{
		AdminToken:    "admin",
		DataDir:       t.TempDir(),
		NodeDir:       nodeDir,
		UpdateService: "warpmesh-node.service",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	key := newTestKey(t, srv)
	nodeWS, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/node?token="+key.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = nodeWS.Close() }()
	if err := nodeWS.WriteJSON(protocol.Message{
		Type:     protocol.TypeHello,
		DeviceID: key.DeviceID,
		Name:     "update-node",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	waitForNode(t, srv, key.DeviceID)

	request, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/devices/"+key.DeviceID+"/update", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer admin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	_ = nodeWS.SetReadDeadline(time.Now().Add(3 * time.Second))
	var task protocol.Message
	if err := nodeWS.ReadJSON(&task); err != nil {
		t.Fatal(err)
	}
	if task.Type != protocol.TypeNodeUpdate || task.UpdateVersion != "v2.0.0" || task.UpdateSHA256 == "" ||
		!strings.Contains(task.UpdateURL, "/api/node-update/download") || task.UpdateService != "warpmesh-node.service" {
		t.Fatalf("unexpected update task: %+v", task)
	}

	if err := nodeWS.WriteJSON(protocol.Message{
		Type:          protocol.TypeNodeUpdateState,
		UpdateID:      task.UpdateID,
		UpdateState:   "staged",
		UpdateVersion: "v2.0.0",
		UpdateError:   "restart required",
		UpdateService: "warpmesh-node.service",
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		device, ok := srv.reg.Get(key.DeviceID)
		if ok && device.UpdateState == "staged" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("update state did not persist: %+v", device)
		}
		time.Sleep(10 * time.Millisecond)
	}

	var devices []Device
	inventory := httptest.NewRecorder()
	inventoryRequest := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	inventoryRequest.Header.Set("Authorization", "Bearer admin")
	srv.Handler().ServeHTTP(inventory, inventoryRequest)
	if err := json.Unmarshal(inventory.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Version != "v1.0.0" || devices[0].UpdateState != "staged" {
		t.Fatalf("unexpected device update metadata: %+v", devices)
	}
}
