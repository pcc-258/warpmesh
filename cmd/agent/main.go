package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/pcc-258/pylon/internal/agent"
)

func main() {
	serverURL := flag.String("server", envOr("DEVICE_RELAY_SERVER", "ws://127.0.0.1:8080/ws/agent"), "relay server websocket URL")
	token := flag.String("token", envOr("DEVICE_RELAY_TOKEN", "device"), "device token")
	deviceID := flag.String("device-id", os.Getenv("DEVICE_RELAY_DEVICE_ID"), "stable device id (auto-generated if empty)")
	name := flag.String("name", os.Getenv("DEVICE_RELAY_NAME"), "display name, defaults to hostname")
	shell := flag.String("shell", os.Getenv("DEVICE_RELAY_SHELL"), "shell used for terminal sessions")
	dataDir := flag.String("data-dir", envOr("DEVICE_RELAY_DATA_DIR", "./data"), "directory for local agent state")
	insecure := flag.Bool("insecure", os.Getenv("DEVICE_RELAY_INSECURE") == "1", "skip TLS verification for self-signed servers")
	allowPaths := flag.String("allow-paths", os.Getenv("DEVICE_RELAY_ALLOW_PATHS"), "comma-separated download path roots, defaults to home")
	forwardList := flag.String("forward", os.Getenv("DEVICE_RELAY_FORWARD"), "comma-separated localPort:targetDeviceId:targetPort forwards")
	stunList := flag.String("stun", os.Getenv("DEVICE_RELAY_STUN"), "comma-separated STUN server URLs for direct connections")
	vncPort := flag.Int("vnc-port", envInt("DEVICE_RELAY_VNC_PORT", 5900), "local VNC server port used for remote desktop")
	flag.Parse()

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
	a, err := agent.New(agent.Config{
		ServerURL:   *serverURL,
		Token:       *token,
		DeviceID:    *deviceID,
		Name:        *name,
		Shell:       *shell,
		DataDir:     *dataDir,
		Insecure:    *insecure,
		AllowPaths:  paths,
		Forwards:    forwards,
		STUNServers: stunServers,
		VNCPort:     *vncPort,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	a.Run()
}

func parseForwards(spec string) ([]agent.ForwardSpec, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	var out []agent.ForwardSpec
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
		out = append(out, agent.ForwardSpec{
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
