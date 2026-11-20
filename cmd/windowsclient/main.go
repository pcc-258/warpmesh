package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type clientOptions struct {
	nodeURL string
	debug   bool
	wait    time.Duration
}

func main() {
	runClient(parseOptions(os.Args[1:]))
}

func parseOptions(args []string) clientOptions {
	fs := flag.NewFlagSet("warpmesh-client", flag.ExitOnError)
	nodeURL := fs.String("node-url", envOr("WARPMESH_NODE_UI_URL", "http://127.0.0.1:9876"), "local node UI URL (loopback only)")
	debug := fs.Bool("debug", os.Getenv("WARPMESH_CLIENT_DEBUG") == "1", "enable WebView2 developer tools")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for the local node UI")
	_ = fs.Parse(args)

	normalized, err := normalizeLoopbackURL(*nodeURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warpmesh-client:", err)
		os.Exit(2)
	}
	return clientOptions{nodeURL: normalized, debug: *debug, wait: *wait}
}

func normalizeLoopbackURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid node URL: %w", err)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("node URL must use http")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("node URL must not contain credentials, a query, or a fragment")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("node URL host must be loopback")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func waitForNode(options clientOptions) bool {
	deadline := time.Now().Add(options.wait)
	client := &http.Client{Timeout: 900 * time.Millisecond}
	for {
		response, err := client.Get(options.nodeURL + "/api/overview")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
