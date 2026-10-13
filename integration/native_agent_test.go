// Package integration runs native server and agent processes against loopback.
// It deliberately does not claim that a desktop GUI or screen permissions were
// exercised: VNC availability and graphical sessions need separate acceptance.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pcc-258/warpmesh/internal/protocol"
	"github.com/pcc-258/warpmesh/internal/server"
)

const adminToken = "native-smoke-local-admin"

type nativeProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, args...)
	// Local developer configuration must never leak into the isolated test.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DEVICE_RELAY_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	return cmd
}

func startProcess(t *testing.T, executable string, args ...string) *nativeProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logPath := filepath.Join(t.TempDir(), "process.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd := command(ctx, executable, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatal(err)
	}
	p := &nativeProcess{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.stop(t)
		cancel()
		_ = logFile.Close()
		if t.Failed() {
			if raw, err := os.ReadFile(logPath); err == nil {
				t.Logf("native process log:\n%s", raw)
			}
		}
	})
	return p
}

func (p *nativeProcess) stop(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Error("native process failed to stop")
	}
}

func eventually(t *testing.T, reason string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", reason)
}

func build(t *testing.T, root, pkg, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := command(ctx, "go", "build", "-o", binary, pkg)
	cmd.Dir = root
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, raw)
	}
	return binary
}

func api(t *testing.T, base, method, path string, input, result any) {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, raw)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	}
}

func openSocket(t *testing.T, base, path, deviceID string, values url.Values) *websocket.Conn {
	t.Helper()
	if values == nil {
		values = make(url.Values)
	}
	values.Set("device", deviceID)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+path+"?"+values.Encode(), http.Header{"Authorization": {"Bearer " + adminToken}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	return ws
}

func readReply(t *testing.T, ws *websocket.Conn) protocol.Message {
	t.Helper()
	var msg protocol.Message
	if err := ws.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type == protocol.TypeFileError || msg.Type == protocol.TypeTermError {
		t.Fatalf("native operation failed: %s", msg.Error)
	}
	return msg
}

func TestNativeAgentSmoke(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	serverBinary := build(t, root, "./cmd/server", "warpmesh-server")
	agentBinary := build(t, root, "./cmd/agent", "warpmesh-agent")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	base := "http://" + address
	serverProcess := startProcess(t, serverBinary, "-listen", address, "-admin-token", adminToken, "-admin-password", "native-smoke-local-password", "-data-dir", t.TempDir())
	eventually(t, "server health", func() bool {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get(base + "/api/health")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	// Keep this reference so a prematurely exited server is explicitly detected.
	select {
	case <-serverProcess.done:
		t.Fatal("server exited before enrollment")
	default:
	}
	var invite server.Invite
	api(t, base, http.MethodPost, "/api/invites", map[string]string{"name": "Native CI device"}, &invite)
	stateDir, shareDir := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	enroll := command(ctx, agentBinary, "enroll", "-server", base, "-code", invite.Code, "-name", "Native CI device", "-data-dir", stateDir)
	raw, err := enroll.Output()
	cancel()
	if err != nil {
		t.Fatalf("native enrollment failed: %v", err)
	}
	var identity server.DeviceKey
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ name, value string }{{"device-id", identity.DeviceID}, {"token", identity.Token}} {
		saved, err := os.ReadFile(filepath.Join(stateDir, item.name))
		if err != nil || string(saved) != item.value || item.value == "" {
			t.Fatalf("enrollment did not persist %s", item.name)
		}
	}
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	}
	startAgent := func() *nativeProcess {
		return startProcess(t, agentBinary, "-server", base, "-data-dir", stateDir, "-name", "Native CI device", "-allow-paths", shareDir, "-auto-vnc=false", "-ui-port", "0", "-shell", shell)
	}
	agentProcess := startAgent()
	getDevice := func(t *testing.T) server.Device {
		var devices []server.Device
		api(t, base, http.MethodGet, "/api/devices", nil, &devices)
		for _, device := range devices {
			if device.ID == identity.DeviceID {
				return device
			}
		}
		return server.Device{}
	}
	eventually(t, "native agent online", func() bool { return getDevice(t).Online })
	device := getDevice(t)
	if device.OS != runtime.GOOS || device.Arch != runtime.GOARCH || device.Capabilities == nil || !device.Capabilities.Terminal || !device.Capabilities.Files {
		t.Fatalf("unexpected native capabilities: %+v", device)
	}
	t.Logf("native server and agent: %s/%s", runtime.GOOS, runtime.GOARCH)

	t.Run("terminal", func(t *testing.T) {
		ws := openSocket(t, base, "/ws/terminal", identity.DeviceID, nil)
		if err := ws.WriteJSON(protocol.Message{Type: protocol.TypeRTCFallback}); err != nil {
			t.Fatal(err)
		}
		for readReply(t, ws).Type != protocol.TypeRTCRelay {
		}
		input := "printf '%s%s\\n' warpmesh- native-ok\r"
		if runtime.GOOS == "windows" {
			input = "echo warpmesh-^native-ok\r\n"
		}
		if err := ws.WriteJSON(protocol.Message{Type: protocol.TypeTermInput, Data: input}); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for !strings.Contains(output.String(), "warpmesh-native-ok") {
			msg := readReply(t, ws)
			if msg.Type == protocol.TypeTermExit {
				t.Fatal("native shell exited without the expected output")
			}
			if msg.Type == protocol.TypeTermOutput {
				data, err := protocol.DecodeData(msg.Data)
				if err != nil {
					t.Fatal(err)
				}
				output.Write(data)
			}
		}
		if err := ws.WriteJSON(protocol.Message{Type: protocol.TypeTermStop}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("files", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0, 1, 2, 255, '\n', 'x'}, 30000)
		ws := openSocket(t, base, "/ws/file", identity.DeviceID, url.Values{"op": {"upload"}, "path": {shareDir}, "name": {"native.bin"}, "size": {fmt.Sprint(len(payload))}})
		for offset := 0; offset < len(payload); offset += 64 * 1024 {
			end := offset + 64*1024
			if end > len(payload) {
				end = len(payload)
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, payload[offset:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := ws.WriteJSON(protocol.Message{Type: protocol.TypeFileUploadEnd}); err != nil {
			t.Fatal(err)
		}
		if msg := readReply(t, ws); msg.Type != protocol.TypeFileDone {
			t.Fatalf("missing native upload acknowledgment: %s", msg.Type)
		}
		_ = ws.Close()
		path := filepath.Join(shareDir, "native.bin")
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, payload) {
			t.Fatalf("native file differs: %v", err)
		}
		download := openSocket(t, base, "/ws/file", identity.DeviceID, url.Values{"op": {"download"}, "path": {path}})
		var downloaded bytes.Buffer
		for {
			kind, data, err := download.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if kind == websocket.BinaryMessage {
				downloaded.Write(data)
				continue
			}
			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Type == protocol.TypeFileError {
				t.Fatalf("native download failed: %s", msg.Error)
			}
			if msg.Type == protocol.TypeFileDone {
				break
			}
		}
		if !bytes.Equal(downloaded.Bytes(), payload) {
			t.Fatal("native download bytes differ")
		}
		manager := openSocket(t, base, "/ws/files", identity.DeviceID, nil)
		if err := manager.WriteJSON(protocol.Message{Type: protocol.TypeFileList, Path: shareDir}); err != nil {
			t.Fatal(err)
		}
		listing := readReply(t, manager)
		if listing.Type != protocol.TypeFileListResult || len(listing.Entries) != 1 || listing.Entries[0].Name != "native.bin" {
			t.Fatalf("native listing failed: %+v", listing.Entries)
		}
	})

	t.Run("restart_identity_and_inventory", func(t *testing.T) {
		api(t, base, http.MethodPatch, "/api/devices/"+identity.DeviceID, map[string]string{"name": "Managed CI name", "group": "CI"}, nil)
		agentProcess.stop(t)
		eventually(t, "agent offline", func() bool { return !getDevice(t).Online })
		_ = startAgent()
		eventually(t, "agent reconnected", func() bool { return getDevice(t).Online })
		device := getDevice(t)
		if device.ID != identity.DeviceID || device.Name != "Managed CI name" || device.Group != "CI" {
			t.Fatalf("restart lost identity or inventory edits: %+v", device)
		}
	})
}
