package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pcc-258/warpmesh/internal/node"
)

func runAutomationCLI(args []string) {
	fs := flag.NewFlagSet("automation", flag.ExitOnError)
	dataDir := fs.String("data-dir", envOr("WARPMESH_NODE_DATA_DIR", "./data"), "node data directory")
	uiPort := fs.Int("ui-port", envInt("WARPMESH_NODE_UI_PORT", 9876), "local node UI port")
	_ = fs.Parse(args)

	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "automation:", err)
		os.Exit(1)
	}
	executable, err := os.Executable()
	if err != nil {
		executable = "warpmesh-node"
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", *uiPort)
	config := map[string]any{
		"apiVersion":      node.AutomationAPIVersion,
		"apiBaseURL":      baseURL + "/api/v1",
		"capabilitiesURL": baseURL + "/api/v1/capabilities",
		"aiGuideURL":      baseURL + "/api/v1/ai-guide",
		"authentication":  "Bearer token",
		"tokenFile":       filepath.Join(absoluteDataDir, node.UITokenFilename),
		"mcp": map[string]any{
			"transport":     "stdio",
			"command":       executable,
			"guideResource": "warpmesh://node/guide",
			"args": []string{
				"mcp",
				"-api", baseURL + "/api/v1",
				"-data-dir", absoluteDataDir,
			},
		},
		"note": "Run `warpmesh-node api-token -data-dir <dir>` only when a client needs the bearer token directly.",
	}
	raw, _ := json.MarshalIndent(config, "", "  ")
	fmt.Println(string(raw))
}

func runAIGuideCLI(args []string) {
	fs := flag.NewFlagSet("ai-guide", flag.ExitOnError)
	_ = fs.Parse(args)
	fmt.Print(node.AIGuideMD)
}

func runAPITokenCLI(args []string) {
	fs := flag.NewFlagSet("api-token", flag.ExitOnError)
	dataDir := fs.String("data-dir", envOr("WARPMESH_NODE_DATA_DIR", "./data"), "node data directory")
	_ = fs.Parse(args)
	token, err := node.LoadOrCreateUIToken(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "api-token:", err)
		os.Exit(1)
	}
	fmt.Println(token)
}

func runMCPCLI(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	api := fs.String("api", envOr("WARPMESH_NODE_API_URL", "http://127.0.0.1:9876/api/v1"), "local automation API base URL")
	dataDir := fs.String("data-dir", envOr("WARPMESH_NODE_DATA_DIR", "./data"), "node data directory")
	tokenFile := fs.String("token-file", "", "automation bearer token file (defaults to <data-dir>/ui-token)")
	token := fs.String("token", os.Getenv("WARPMESH_NODE_API_TOKEN"), "automation bearer token (prefer -token-file)")
	_ = fs.Parse(args)

	bearer := *token
	if bearer == "" {
		file := *tokenFile
		if file == "" {
			file = node.UITokenPath(*dataDir)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mcp: read automation token:", err)
			os.Exit(1)
		}
		bearer = strings.TrimSpace(string(raw))
	}
	if bearer == "" {
		fmt.Fprintln(os.Stderr, "mcp: automation token is required")
		os.Exit(2)
	}
	client := node.NewControlClient(*api, bearer)
	if err := node.ServeMCP(context.Background(), os.Stdin, os.Stdout, client); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}
