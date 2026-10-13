package agent

import (
	"errors"
	"io"
	"net"
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

func testAgent(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Config{Token: "test", DeviceID: "test", DataDir: dir, AllowPaths: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.cleanupSessions)
	return a
}

func testWebSockets(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- ws
		<-release
		_ = ws.Close()
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	peer := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close(); close(release); server.Close() })
	return client, peer
}

func readAgentMessage(t *testing.T, ws *websocket.Conn) protocol.Message {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var msg protocol.Message
	if err := ws.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not unblock")
	}
}

func TestForwardConnCloseInterruptsIO(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			conn, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			fc := newForwardConn(conn, nil)
			started, done := make(chan struct{}), make(chan struct{})
			go func() {
				close(started)
				if operation == "read" {
					_, _ = fc.Read(make([]byte, 1))
				} else {
					_, _ = fc.Write([]byte("blocked"))
				}
				close(done)
			}()
			<-started
			closed := make(chan struct{})
			go func() { _ = fc.Close(); close(closed) }()
			waitDone(t, closed)
			waitDone(t, done)
		})
	}
}

func TestForwardTargetControlLoopAndReconnect(t *testing.T) {
	a := testAgent(t)
	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- ws
		<-release
		_ = ws.Close()
	}))
	t.Cleanup(func() { close(release); server.Close() })
	a.cfg.ServerURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	services := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			services <- conn
		}
	}()

	for i := 0; i < 2; i++ {
		finished := make(chan struct{})
		go func() { _ = a.connectOnce(); close(finished) }()
		var control *websocket.Conn
		select {
		case control = <-accepted:
		case <-time.After(3 * time.Second):
			t.Fatal("control connection timeout")
		}
		if got := readAgentMessage(t, control); got.Type != protocol.TypeHello {
			t.Fatalf("hello: %+v", got)
		}
		if err := control.WriteJSON(protocol.Message{Type: protocol.TypeForwardConnect, SessionID: "relay", Port: ln.Addr().(*net.TCPAddr).Port}); err != nil {
			t.Fatal(err)
		}
		if got := readAgentMessage(t, control); got.Type != protocol.TypeForwardOpen {
			t.Fatalf("open: %+v", got)
		}
		service := <-services
		if err := control.WriteJSON(protocol.Message{Type: protocol.TypePing}); err != nil {
			t.Fatal(err)
		}
		if got := readAgentMessage(t, control); got.Type != protocol.TypePong {
			t.Fatalf("ping blocked by TCP read: %+v", got)
		}
		if err := control.WriteJSON(protocol.Message{Type: protocol.TypeForwardData, SessionID: "relay", Data: protocol.EncodeData([]byte("request"))}); err != nil {
			t.Fatal(err)
		}
		_ = service.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, len("request"))
		if _, err := io.ReadFull(service, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "request" {
			t.Fatalf("data = %q", buf)
		}
		if _, err := service.Write([]byte("response")); err != nil {
			t.Fatal(err)
		}
		if got := readAgentMessage(t, control); got.Type != protocol.TypeForwardData || got.Data != protocol.EncodeData([]byte("response")) {
			t.Fatalf("response: %+v", got)
		}
		if i == 0 {
			if err := control.WriteJSON(protocol.Message{Type: protocol.TypeForwardClose, SessionID: "relay"}); err != nil {
				t.Fatal(err)
			}
			if err := control.WriteJSON(protocol.Message{Type: protocol.TypePing}); err != nil {
				t.Fatal(err)
			}
			if got := readAgentMessage(t, control); got.Type != protocol.TypePong {
				t.Fatalf("close blocked: %+v", got)
			}
		}
		for _, msg := range []protocol.Message{
			{Type: protocol.TypeFileUpload, SessionID: "interrupted-upload", Name: "partial"},
			{Type: protocol.TypeFileChunk, SessionID: "interrupted-upload", Data: protocol.EncodeData([]byte("partial"))},
			{Type: protocol.TypePing},
		} {
			if err := control.WriteJSON(msg); err != nil {
				t.Fatal(err)
			}
		}
		if got := readAgentMessage(t, control); got.Type != protocol.TypePong {
			t.Fatalf("upload/ping: %+v", got)
		}
		a.mu.Lock()
		upload := a.uploads["interrupted-upload"]
		a.mu.Unlock()
		if upload == nil {
			t.Fatal("upload was not registered")
		}
		_ = control.Close()
		waitDone(t, finished)
		if _, err := upload.Write([]byte("late")); err == nil {
			t.Fatal("upload descriptor survived reconnect cleanup")
		}
		_ = service.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := service.Read(buf); err == nil {
			t.Fatal("TCP session survived disconnect/close")
		}
		_ = service.Close()
		a.mu.Lock()
		count := len(a.forwards)
		a.mu.Unlock()
		if count != 0 {
			t.Fatal("forward sessions survived cleanup")
		}
	}
}

func TestForwardBlockedWriterDoesNotBlockPingOrCleanup(t *testing.T) {
	a := testAgent(t)
	client, peer := testWebSockets(t)
	a.ws = client
	conn, tcpPeer := net.Pipe()
	defer func() { _ = tcpPeer.Close() }()
	fc := newForwardConn(conn, client)
	a.forwards["blocked"] = fc
	go fc.writeLoop()
	for i := 0; i < 3; i++ {
		if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardData, SessionID: "blocked", Data: protocol.EncodeData([]byte("write"))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypePing}); err != nil {
		t.Fatal(err)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypePong {
		t.Fatalf("pong: %+v", got)
	}
	done := make(chan struct{})
	go func() { a.cleanupSessions(); close(done) }()
	waitDone(t, done)
	if fc.ctx.Err() == nil {
		t.Fatal("writer not cancelled")
	}
}

func TestForwardBacklogOverflowClosesAndNotifiesRelay(t *testing.T) {
	a := testAgent(t)
	client, peer := testWebSockets(t)
	a.ws = client
	conn, tcpPeer := net.Pipe()
	defer func() { _ = tcpPeer.Close() }()
	fc := newForwardConn(conn, client)
	a.forwards["overflow"] = fc
	done := make(chan struct{})
	go func() { a.readForward("overflow", fc); a.finishForward("overflow", fc); close(done) }()
	// No writer drains the queue, representing a completely stalled TCP peer.
	for i := 0; i <= cap(fc.writes); i++ {
		if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardData, SessionID: "overflow", Data: protocol.EncodeData([]byte("data"))}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypeForwardClose || got.SessionID != "overflow" {
		t.Fatalf("overflow notification: %+v", got)
	}
	waitDone(t, done)
	if fc.ctx.Err() == nil {
		t.Fatal("overflow not cancelled")
	}
}

type readEOFConn struct{ net.Conn }

func (readEOFConn) Read([]byte) (int, error) { return 0, io.EOF }

func TestForwardDataThenCloseDrainsAllQueuedBytes(t *testing.T) {
	for _, readHalfEOF := range []bool{false, true} {
		name := "idle read"
		if readHalfEOF {
			name = "read-half EOF"
		}
		t.Run(name, func(t *testing.T) {
			a := testAgent(t)
			conn, tcpPeer := net.Pipe()
			defer func() { _ = tcpPeer.Close() }()
			transport := conn
			if readHalfEOF {
				transport = readEOFConn{conn}
			}
			fc := newForwardConn(transport, nil)
			a.forwards["ordered"] = fc
			fc.startWriter()
			payload := strings.Repeat("tail-data", 1024)
			// Fill the data backlog while the TCP peer deliberately does not read.
			// EOF still has a reserved slot and must not discard any prior frame.
			for i := 0; i < cap(fc.writes)-1; i++ {
				if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardData, SessionID: "ordered", Data: protocol.EncodeData([]byte(payload))}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardClose, SessionID: "ordered"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { a.readForward("ordered", fc); a.finishForward("ordered", fc); close(done) }()
			_ = tcpPeer.SetReadDeadline(time.Now().Add(3 * time.Second))
			raw, err := io.ReadAll(tcpPeer)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Repeat(payload, cap(fc.writes)-1); string(raw) != want {
				t.Fatalf("data+close truncated: got %d want %d bytes", len(raw), len(want))
			}
			waitDone(t, done)
		})
	}
}

func TestForwardDrainDeadlineAndReconnectInterruptBlockedWriter(t *testing.T) {
	for _, mode := range []string{"deadline", "reconnect", "error"} {
		t.Run(mode, func(t *testing.T) {
			a := testAgent(t)
			conn, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			fc := newForwardConn(conn, nil)
			a.forwards["blocked-drain"] = fc
			fc.startWriter()
			fc.enqueue([]byte("blocked because peer never reads"))
			if mode == "deadline" {
				fc.gracefulClose(25 * time.Millisecond)
			} else {
				if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardClose, SessionID: "blocked-drain"}); err != nil {
					t.Fatal(err)
				}
				a.mu.Lock()
				registered := a.forwards["blocked-drain"] == fc
				a.mu.Unlock()
				if !registered {
					t.Fatal("draining session lost reconnect cleanup registration")
				}
				if mode == "error" {
					if err := a.handleMessage(protocol.Message{Type: protocol.TypeForwardError, SessionID: "blocked-drain", Error: "cancelled"}); err != nil {
						t.Fatal(err)
					}
				} else {
					a.cleanupSessions()
				}
			}
			done := make(chan struct{})
			go func() { a.readForward("blocked-drain", fc); a.finishForward("blocked-drain", fc); close(done) }()
			waitDone(t, done)
			if fc.ctx.Err() == nil {
				t.Fatal("blocked drain not force-cancelled")
			}
		})
	}
}

func TestForwardCancellationBeforeAttachAndSessionReuse(t *testing.T) {
	a := testAgent(t)
	old := newForwardConn(nil, nil)
	a.forwards["reuse"] = old
	_ = old.Close()
	conn, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	if old.attach(conn) {
		t.Fatal("cancelled session accepted late dial")
	}
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("late dial was not closed")
	}
	current := newForwardConn(nil, nil)
	a.forwards["reuse"] = current
	a.finishForward("reuse", old)
	if a.forwards["reuse"] != current {
		t.Fatal("old goroutine deleted reused session")
	}
}

func TestUploadDownloadAndAcknowledgement(t *testing.T) {
	a := testAgent(t)
	client, peer := testWebSockets(t)
	a.ws = client
	dir := filepath.Join(a.cfg.DataDir, "nested", "new")
	for _, msg := range []protocol.Message{
		{Type: protocol.TypeFileUpload, SessionID: "file", Path: dir, Name: "sample.txt"},
		{Type: protocol.TypeFileChunk, SessionID: "file", Data: protocol.EncodeData([]byte("payload"))},
		{Type: protocol.TypeFileUploadEnd, SessionID: "file"},
	} {
		if err := a.handleMessage(msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileDone || got.SessionID != "file" {
		t.Fatalf("upload ACK: %+v", got)
	}
	path := filepath.Join(dir, "sample.txt")
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "payload" {
		t.Fatalf("upload: %q %v", raw, err)
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileDownload, SessionID: "download", Path: path}); err != nil {
		t.Fatal(err)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileChunk || got.Data != protocol.EncodeData([]byte("payload")) {
		t.Fatalf("download: %+v", got)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileDone {
		t.Fatalf("download completion: %+v", got)
	}
}

func TestUploadRejectsOutsideSymlinks(t *testing.T) {
	for _, kind := range []string{"existing", "dangling", "parent"} {
		t.Run(kind, func(t *testing.T) {
			a := testAgent(t)
			client, peer := testWebSockets(t)
			a.ws = client
			outside := t.TempDir()
			victim := filepath.Join(outside, "victim")
			if kind != "dangling" {
				if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			dir := a.cfg.DataDir
			link := filepath.Join(dir, "victim")
			target := victim
			if kind == "parent" {
				link = filepath.Join(dir, "parent")
				target = outside
				dir = link
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "escape", Path: dir, Name: "victim"}); err != nil {
				t.Fatal(err)
			}
			if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileError {
				t.Fatalf("escape allowed: %+v", got)
			}
			raw, err := os.ReadFile(victim)
			if kind == "dangling" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("outside file created: %q %v", raw, err)
				}
			} else if err != nil || string(raw) != "untouched" {
				t.Fatalf("outside file changed: %q %v", raw, err)
			}
		})
	}
}

func TestUploadAllowsInternalRelativeSymlinkAndDefaultDirectory(t *testing.T) {
	a := testAgent(t)
	target := filepath.Join(a.cfg.DataDir, "target")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(a.cfg.DataDir, "link")); err != nil {
		t.Fatal(err)
	}
	f, err := a.openUpload(a.cfg.DataDir, "link")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "new" {
		t.Fatalf("internal symlink upload: %q %v", raw, err)
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "default", Name: "sample"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "uploads", "sample")); err != nil {
		t.Fatal(err)
	}
}

func TestUploadAllowsAbsoluteInternalSymlink(t *testing.T) {
	a := testAgent(t)
	target := filepath.Join(a.cfg.DataDir, "target")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(a.cfg.DataDir, "link")); err != nil {
		t.Fatal(err)
	}
	f, err := a.openUpload(a.cfg.DataDir, "link")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "new" {
		t.Fatalf("internal absolute link: %q %v", raw, err)
	}
}

func TestFileRootRejectsLinkReplacementAfterResolution(t *testing.T) {
	a := testAgent(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.cfg.DataDir, "new-file")
	root, rel, err := a.fileRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	// Plant the symlink after the path has been resolved and the root chosen.
	// This is the vulnerable check/open interval in the previous implementation.
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666); err == nil {
		_ = f.Close()
		t.Fatal("replacement symlink escaped the root")
	}
	if raw, err := os.ReadFile(victim); err != nil || string(raw) != "untouched" {
		t.Fatalf("outside file changed: %q %v", raw, err)
	}
}

func TestUploadWriteAndCloseFailuresReportFileError(t *testing.T) {
	for _, messageType := range []string{protocol.TypeFileChunk, protocol.TypeFileUploadEnd} {
		t.Run(messageType, func(t *testing.T) {
			a := testAgent(t)
			client, peer := testWebSockets(t)
			a.ws = client
			if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "broken", Name: "file"}); err != nil {
				t.Fatal(err)
			}
			_ = a.uploads["broken"].Close()
			if err := a.handleMessage(protocol.Message{Type: messageType, SessionID: "broken", Data: protocol.EncodeData([]byte("fail"))}); err != nil {
				t.Fatal(err)
			}
			if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileError || got.Error == "" {
				t.Fatalf("failure: %+v", got)
			}
			if len(a.uploads) != 0 {
				t.Fatal("failed upload retained")
			}
		})
	}
}

func TestUploadCancellationDoesNotAcknowledgeSuccess(t *testing.T) {
	a := testAgent(t)
	client, peer := testWebSockets(t)
	a.ws = client
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "cancel", Name: "partial"}); err != nil {
		t.Fatal(err)
	}
	f := a.uploads["cancel"]
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileError, SessionID: "cancel", Error: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("late")); err == nil {
		t.Fatal("cancelled upload remains open")
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypePing}); err != nil {
		t.Fatal(err)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypePong {
		t.Fatalf("cancellation sent unexpected ACK: %+v", got)
	}
}

func TestUploadMalformedChunkReportsError(t *testing.T) {
	a := testAgent(t)
	client, peer := testWebSockets(t)
	a.ws = client
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "bad", Name: "bad"}); err != nil {
		t.Fatal(err)
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileChunk, SessionID: "bad", Data: "!not-base64!"}); err != nil {
		t.Fatal(err)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypeFileError {
		t.Fatalf("decode failure: %+v", got)
	}
	if len(a.uploads) != 0 {
		t.Fatal("malformed upload retained")
	}
}

func TestUploadDisconnectCleanupAndOldDownloadCannotUseReconnect(t *testing.T) {
	a := testAgent(t)
	oldWS, _ := testWebSockets(t)
	a.ws = oldWS
	if err := a.handleMessage(protocol.Message{Type: protocol.TypeFileUpload, SessionID: "interrupted", Name: "partial"}); err != nil {
		t.Fatal(err)
	}
	f := a.uploads["interrupted"]
	a.cleanupSessions()
	if _, err := f.Write([]byte("late")); err == nil {
		t.Fatal("upload file survived disconnect")
	}
	if len(a.uploads) != 0 {
		t.Fatal("upload retained")
	}
	newWS, peer := testWebSockets(t)
	a.ws = newWS
	if err := a.streamFileOn(protocol.Message{Type: protocol.TypeFileDownload, SessionID: "old", Path: filepath.Join(a.cfg.DataDir, "uploads", "partial")}, oldWS); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("stale download: %v", err)
	}
	if err := a.handleMessage(protocol.Message{Type: protocol.TypePing}); err != nil {
		t.Fatal(err)
	}
	if got := readAgentMessage(t, peer); got.Type != protocol.TypePong {
		t.Fatalf("old download contaminated new connection: %+v", got)
	}
}

func TestScreenBridgeEitherSideUnblocksOther(t *testing.T) {
	for _, side := range []string{"vnc", "websocket"} {
		t.Run(side, func(t *testing.T) {
			ws, wsPeer := testWebSockets(t)
			conn, vncPeer := net.Pipe()
			defer func() { _ = vncPeer.Close() }()
			done := make(chan struct{})
			go func() { bridgeScreen(ws, conn); close(done) }()
			if side == "vnc" {
				_ = vncPeer.Close()
			} else {
				_ = wsPeer.Close()
			}
			waitDone(t, done)
		})
	}
}
