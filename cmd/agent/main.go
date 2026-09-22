package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/pcc-258/device-relay/internal/agent"
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
	flag.Parse()

	var paths []string
	for _, p := range splitComma(*allowPaths) {
		if p != "" {
			paths = append(paths, p)
		}
	}
	a, err := agent.New(agent.Config{
		ServerURL: *serverURL,
		Token:     *token,
		DeviceID:  *deviceID,
		Name:      *name,
		Shell:     *shell,
		DataDir:   *dataDir,
		Insecure:  *insecure,
		AllowPaths: paths,
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	a.Run()
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
