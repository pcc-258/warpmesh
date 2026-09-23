package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"golang.org/x/term"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func runTerminalCLI(args []string) {
	fs := flag.NewFlagSet("terminal", flag.ExitOnError)
	server := fs.String("server", "https://warpmesh.ddns.net", "web base URL")
	token := fs.String("token", os.Getenv("WARPMESH_ADMIN_TOKEN"), "admin or session token")
	device := fs.String("device", "", "target device id")
	insecure := fs.Bool("insecure", false, "skip TLS verification")
	cols := fs.Int("cols", 120, "terminal columns")
	rows := fs.Int("rows", 30, "terminal rows")
	_ = fs.Parse(args)

	if *device == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "usage: warpmesh-agent terminal -server https://host -token <admin-token> -device <device-id>")
		os.Exit(2)
	}
	wss := webWSSURL(*server, "/ws/terminal", map[string]string{
		"token":  *token,
		"device": *device,
		"cols":   strconv.Itoa(*cols),
		"rows":   strconv.Itoa(*rows),
	})

	dialer := websocket.DefaultDialer
	if *insecure {
		dialer.TLSClientConfig = insecureTLSConfig()
	}
	ws, _, err := dialer.Dial(wss, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect terminal: %v\n", err)
		os.Exit(1)
	}
	defer ws.Close()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err == nil {
		defer func() { _ = term.Restore(fd, oldState) }()
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				_ = ws.WriteJSON(protocol.Message{Type: protocol.TypeTermInput, Data: string(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		var msg protocol.Message
		if err := ws.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case protocol.TypeTermOutput:
			raw, err := protocol.DecodeData(msg.Data)
			if err == nil {
				_, _ = os.Stdout.Write(raw)
			}
		case protocol.TypeTermExit, protocol.TypeTermError:
			return
		}
	}
}

func runDesktopCLI(args []string) {
	fs := flag.NewFlagSet("desktop", flag.ExitOnError)
	server := fs.String("server", "https://warpmesh.ddns.net", "web base URL")
	device := fs.String("device", "", "target device id")
	noOpen := fs.Bool("no-open", false, "only print the desktop URL")
	_ = fs.Parse(args)

	if *device == "" {
		fmt.Fprintln(os.Stderr, "usage: warpmesh-agent desktop -server https://host -device <device-id>")
		os.Exit(2)
	}
	u := strings.TrimRight(*server, "/") + "/#/desktop/" + url.PathEscape(*device)
	fmt.Println(u)
	if !*noOpen {
		openURL(u)
	}
}

func runEnrollCLI(args []string) {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	server := fs.String("server", "https://warpmesh.ddns.net", "web base URL")
	code := fs.String("code", "", "one-time invite code")
	name := fs.String("name", "", "device name")
	dataDir := fs.String("data-dir", "data", "directory to persist device id and token")
	_ = fs.Parse(args)

	if *code == "" {
		fmt.Fprintln(os.Stderr, "usage: warpmesh-agent enroll -server https://host -code <invite> -name <device-name>")
		os.Exit(2)
	}
	body := strings.NewReader(fmt.Sprintf(`{"code":%q,"name":%q}`, *code, *name))
	resp, err := http.Post(strings.TrimRight(*server, "/")+"/api/enroll", "application/json", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enroll: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var key struct {
		DeviceID  string `json:"deviceId"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&key); err != nil {
		fmt.Fprintf(os.Stderr, "enroll: %v\n", err)
		os.Exit(1)
	}
	if key.Token == "" {
		fmt.Fprintln(os.Stderr, "enroll failed: invalid invite")
		os.Exit(1)
	}
	if *dataDir != "" {
		if err := os.MkdirAll(*dataDir, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "enroll: %v\n", err)
			os.Exit(1)
		}
		_ = os.WriteFile(filepath.Join(*dataDir, "device-id"), []byte(key.DeviceID), 0o600)
		_ = os.WriteFile(filepath.Join(*dataDir, "token"), []byte(key.Token), 0o600)
	}
	raw, _ := json.MarshalIndent(key, "", "  ")
	fmt.Println(string(raw))
}

func openURL(u string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", u).Start()
	case "linux":
		_ = exec.Command("xdg-open", u).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
}

func webWSSURL(base, path string, query map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	u.Path = path
	q := u.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
