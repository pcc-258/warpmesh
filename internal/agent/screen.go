package agent

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// startScreenLink opens a dedicated data WebSocket to the server and bridges
// it with the local VNC server, so the browser can drive the desktop.
func (a *Agent) startScreenLink(sessionID string) {
	u, err := url.Parse(a.cfg.ServerURL)
	if err != nil {
		return
	}
	u.Path = "/ws/screen-link"
	q := u.Query()
	q.Set("device", a.cfg.DeviceID)
	q.Set("session", sessionID)
	u.RawQuery = q.Encode()

	dialer := websocket.DefaultDialer
	if a.cfg.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Token)
	ws, _, err := dialer.Dial(u.String(), header)
	if err != nil {
		log.Printf("screen link failed: %v", err)
		return
	}
	defer func() { _ = ws.Close() }()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", a.cfg.VNCPort), 5*time.Second)
	if err != nil {
		log.Printf("screen vnc dial failed: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	bridgeScreen(ws, conn)
}

// The first direction to stop tears down both transports, unblocking the
// other direction even when its peer stays connected but sends no more data.
func bridgeScreen(ws *websocket.Conn, conn net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				break
			}
			if _, err := conn.Write(data); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	_ = ws.Close()
	_ = conn.Close()
	<-done
}
