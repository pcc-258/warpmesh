package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pcc-258/warpmesh/internal/node"
	"github.com/pcc-258/warpmesh/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "terminal":
			runTerminalCLI(os.Args[2:])
			return
		case "desktop":
			runDesktopCLI(os.Args[2:])
			return
		case "enroll":
			runEnrollCLI(os.Args[2:])
			return
		case "automation":
			runAutomationCLI(os.Args[2:])
			return
		case "ai-guide":
			runAIGuideCLI(os.Args[2:])
			return
		case "api-token":
			runAPITokenCLI(os.Args[2:])
			return
		case "mcp":
			runMCPCLI(os.Args[2:])
			return
		case "version":
			info := version.Current()
			fmt.Printf("warpmesh-node %s %s %s\n", info.Version, info.Commit, info.BuildTime)
			return
		case "vnc-install":
			runVNCInstallCLI(os.Args[2:])
			return
		}
	}

	serverURL := flag.String("server", envOr("WARPMESH_NODE_SERVER", "ws://127.0.0.1:8080/ws/node"), "relay server websocket URL")
	token := flag.String("token", envOr("WARPMESH_NODE_TOKEN", ""), "device token, or read from data dir after enroll")
	deviceID := flag.String("device-id", envOr("WARPMESH_NODE_ID", ""), "stable device id (auto-generated if empty)")
	name := flag.String("name", envOr("WARPMESH_NODE_NAME", ""), "display name, defaults to hostname")
	shell := flag.String("shell", envOr("WARPMESH_NODE_SHELL", ""), "shell used for terminal sessions")
	dataDir := flag.String("data-dir", envOr("WARPMESH_NODE_DATA_DIR", "./data"), "directory for local node state")
	insecure := flag.Bool("insecure", os.Getenv("WARPMESH_NODE_INSECURE") == "1", "skip TLS verification for self-signed servers")
	allowPaths := flag.String("allow-paths", envOr("WARPMESH_NODE_ALLOW_PATHS", ""), "comma-separated download path roots, defaults to home")
	forwardList := flag.String("forward", envOr("WARPMESH_NODE_FORWARD", ""), "comma-separated localPort:targetDeviceId:targetPort forwards")
	stunList := flag.String("stun", envOr("WARPMESH_NODE_STUN", ""), "comma-separated STUN server URLs for direct connections")
	vncPort := flag.Int("vnc-port", envInt("WARPMESH_NODE_VNC_PORT", 5900), "local VNC server port used for remote desktop")
	uiPort := flag.Int("ui-port", envInt("WARPMESH_NODE_UI_PORT", 9876), "local web UI port, 0 to disable")
	autoVNC := flag.Bool("auto-vnc", envOr("WARPMESH_NODE_AUTO_VNC", "1") != "0", "auto start a local VNC server for remote desktop")
	updateService := flag.String("update-service", envOr("WARPMESH_NODE_UPDATE_SERVICE", ""), "systemd service to restart after a staged update")
	flag.Parse()

	serverEndpoint := *serverURL
	if strings.HasPrefix(serverEndpoint, "http://") || strings.HasPrefix(serverEndpoint, "https://") {
		resolved, discoveredService, err := resolveNodeEndpoint(serverEndpoint, *insecure)
		if err != nil {
			log.Fatalf("resolve node endpoint: %v", err)
		}
		serverEndpoint = resolved
		if *updateService == "" {
			*updateService = discoveredService
		}
	}

	var paths []string
	for _, p := range splitComma(*allowPaths) {
		if p != "" {
			paths = append(paths, p)
		}
	}
	forwards, err := parseForwards(*forwardList)
	if err != nil {
		log.Fatalf("parse -forward: %v", err)
	}
	var stunServers []string
	for _, s := range splitComma(*stunList) {
		if s != "" {
			stunServers = append(stunServers, s)
		}
	}
	a, err := node.New(node.Config{
		ServerURL:     serverEndpoint,
		Token:         *token,
		DeviceID:      *deviceID,
		Name:          *name,
		Shell:         *shell,
		DataDir:       *dataDir,
		Insecure:      *insecure,
		AllowPaths:    paths,
		Forwards:      forwards,
		STUNServers:   stunServers,
		VNCPort:       *vncPort,
		UIPort:        *uiPort,
		UpdateService: *updateService,
		AutoVNC:       *autoVNC,
	})
	if err != nil {
		log.Fatalf("create node: %v", err)
	}
	a.Run()
}

func resolveNodeEndpoint(base string, insecure bool) (string, string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	if insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	resp, err := client.Get(strings.TrimRight(base, "/") + "/api/node-config")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var cfg struct {
		NodeWSS    string `json:"nodeWSS"`
		NodeUpdate struct {
			Service string `json:"service"`
		} `json:"nodeUpdate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", "", err
	}
	if cfg.NodeWSS == "" {
		return "", "", fmt.Errorf("server did not publish a node endpoint")
	}
	return cfg.NodeWSS, cfg.NodeUpdate.Service, nil
}

func parseForwards(spec string) ([]node.ForwardSpec, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	var out []node.ForwardSpec
	for _, item := range splitComma(spec) {
		parts := strings.Split(item, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid forward %q, want localPort:targetDeviceId:targetPort", item)
		}
		localPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid local port in %q", item)
		}
		targetPort, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid target port in %q", item)
		}
		if parts[1] == "" || localPort <= 0 || targetPort <= 0 {
			return nil, fmt.Errorf("invalid forward %q", item)
		}
		out = append(out, node.ForwardSpec{
			LocalPort:      localPort,
			TargetDeviceID: parts[1],
			TargetPort:     targetPort,
		})
	}
	return out, nil
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func envInt(key string, def int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return def
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
