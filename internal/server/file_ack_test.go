package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pcc-258/warpmesh/internal/protocol"
)

func TestUploadWaitsForDeviceAcknowledgement(t *testing.T) {
	for _, ackType := range []string{protocol.TypeFileDone, protocol.TypeFileError} {
		t.Run(ackType, func(t *testing.T) {
			srv, err := NewServer(Config{AdminToken: "admin-token"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			base := "ws" + strings.TrimPrefix(ts.URL, "http")
			key := newTestKey(t, srv)
			agent, _, err := websocket.DefaultDialer.Dial(base+"/ws/agent?token="+key.Token, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = agent.Close() }()
			if err := agent.WriteJSON(protocol.Message{Type: protocol.TypeHello, DeviceID: key.DeviceID}); err != nil {
				t.Fatal(err)
			}
			waitForAgent(t, srv, key.DeviceID)
			browser, _, err := websocket.DefaultDialer.Dial(base+"/ws/file?token=admin-token&device="+key.DeviceID+"&op=upload&name=test.txt", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = browser.Close() }()
			_ = agent.SetReadDeadline(time.Now().Add(3 * time.Second))
			start := readAgentMessage(t, agent)
			if start.Type != protocol.TypeFileUpload {
				t.Fatalf("expected upload, got %+v", start)
			}
			if err := browser.WriteMessage(websocket.BinaryMessage, []byte("payload")); err != nil {
				t.Fatal(err)
			}
			chunk := readAgentMessage(t, agent)
			data, err := protocol.DecodeData(chunk.Data)
			if err != nil || chunk.Type != protocol.TypeFileChunk || string(data) != "payload" {
				t.Fatalf("chunk=%+v error=%v", chunk, err)
			}
			if err := browser.WriteJSON(protocol.Message{Type: protocol.TypeFileUploadEnd}); err != nil {
				t.Fatal(err)
			}
			end := readAgentMessage(t, agent)
			if end.Type != protocol.TypeFileUploadEnd {
				t.Fatalf("expected completion, got %+v", end)
			}
			// Prove the handler continues reading after completion but before ACK.
			// ReadJSON processes the protocol pong while waiting for the ACK.
			pong := make(chan struct{}, 1)
			browser.SetPongHandler(func(string) error {
				select {
				case pong <- struct{}{}:
				default:
				}
				return nil
			})
			_ = browser.SetReadDeadline(time.Now().Add(3 * time.Second))
			type result struct {
				msg protocol.Message
				err error
			}
			read := make(chan result, 1)
			go func() { var msg protocol.Message; err := browser.ReadJSON(&msg); read <- result{msg, err} }()
			if err := browser.WriteControl(websocket.PingMessage, []byte("await-ack"), time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-pong:
			case got := <-read:
				t.Fatalf("socket ended before acknowledgement: %+v", got)
			case <-time.After(2 * time.Second):
				t.Fatal("upload stopped reading before acknowledgement")
			}
			ack := protocol.Message{Type: ackType, SessionID: start.SessionID}
			if ackType == protocol.TypeFileError {
				ack.Error = "disk full"
			}
			if err := agent.WriteJSON(ack); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-read:
				if got.err != nil || got.msg.Type != ackType || got.msg.Error != ack.Error {
					t.Fatalf("ack=%+v error=%v", got.msg, got.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("device acknowledgement was not relayed")
			}
			want := "closed"
			if ackType == protocol.TypeFileError {
				want = "failed"
			}
			waitFor(t, func() bool {
				connections := srv.reg.ListConnections(time.Now().Add(-time.Hour), 10)
				return len(connections) == 1 && connections[0].State == want
			})
		})
	}
}

func TestTrafficCloseIsIdempotent(t *testing.T) {
	reg, err := NewRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()
	id, err := reg.StartConnection("actor", "device", "", "files-upload", "relay", "")
	if err != nil {
		t.Fatal(err)
	}
	traffic := newTrafficRecorder(reg, id)
	traffic.Add(7, 0)
	traffic.Close("failed")
	traffic.Add(100, 0)
	traffic.Close("closed")
	connections := reg.ListConnections(time.Now().Add(-time.Hour), 10)
	if len(connections) != 1 || connections[0].State != "failed" || connections[0].BytesToDevice != 7 {
		t.Fatalf("late cleanup changed completed traffic: %+v", connections)
	}
}
