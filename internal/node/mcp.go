package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MCPProtocolVersion = "2025-06-18"

// MCPBackend abstracts the local automation API for tests and alternate
// transports. The HTTP implementation is ControlClient.
type MCPBackend interface {
	Call(context.Context, string, string, any) (any, error)
}

type ControlClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewControlClient(baseURL, token string) *ControlClient {
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/api/v1")
	return &ControlClient{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *ControlClient) Call(ctx context.Context, method, requestPath string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+requestPath, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNoContent {
		return map[string]any{"deleted": true}, nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	var result any
	decodeErr := decoder.Decode(&result)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if failure, ok := result.(map[string]any); ok {
			if detail, ok := failure["error"].(map[string]any); ok {
				message, _ := detail["message"].(string)
				if message != "" {
					return nil, fmt.Errorf("automation API: %s", message)
				}
			}
		}
		return nil, fmt.Errorf("automation API: status %d", response.StatusCode)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("decode automation response: %w", decodeErr)
	}
	return result, nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type resourceReadParams struct {
	URI string `json:"uri"`
}

// ServeMCP serves newline-delimited MCP JSON-RPC over stdio or another stream.
// Protocol output is the only stdout content; diagnostics belong on stderr.
func ServeMCP(ctx context.Context, input io.Reader, output io.Writer, backend MCPBackend) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal(line, &request); err != nil {
			if err := encoder.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		response, send := handleMCPRequest(ctx, request, backend)
		if !send {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleMCPRequest(ctx context.Context, request rpcRequest, backend MCPBackend) (rpcResponse, bool) {
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" || request.Method == "" {
		response.Error = &rpcError{Code: -32600, Message: "invalid request"}
		return response, len(request.ID) > 0
	}
	if len(request.ID) == 0 {
		// All supported notifications are acknowledgements with no response.
		return response, false
	}
	switch request.Method {
	case "initialize":
		response.Result = map[string]any{
			"protocolVersion": MCPProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"subscribe": false, "listChanged": false},
			},
			"serverInfo": map[string]any{"name": "warpmesh-node", "version": "0.1.0"},
			"instructions": "Manage the local WarpMesh node through typed tools. " +
				"Read warpmesh://node/guide before mutations. " +
				"Terminal and desktop deep links remain subject to server-side authorization.",
		}
	case "ping":
		response.Result = map[string]any{}
	case "tools/list":
		response.Result = map[string]any{"tools": mcpToolDefinitions()}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			response.Error = &rpcError{Code: -32602, Message: "invalid tool parameters"}
			return response, true
		}
		result, err := callMCPTool(ctx, backend, params.Name, params.Arguments)
		if err != nil {
			response.Result = toolErrorResult(err)
			return response, true
		}
		response.Result = toolSuccessResult(result)
	case "resources/list":
		response.Result = map[string]any{"resources": mcpResources()}
	case "resources/read":
		var params resourceReadParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			response.Error = &rpcError{Code: -32602, Message: "invalid resource parameters"}
			return response, true
		}
		mimeType, text, err := readMCPResource(ctx, backend, params.URI)
		if err != nil {
			response.Error = &rpcError{Code: -32001, Message: err.Error()}
			return response, true
		}
		response.Result = map[string]any{"contents": []map[string]any{{
			"uri":      params.URI,
			"mimeType": mimeType,
			"text":     text,
		}}}
	default:
		response.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return response, true
}

func mcpToolDefinitions() []map[string]any {
	tools := AutomationTools()
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"annotations": tool.Annotations,
		}
		if tool.OutputSchema != nil {
			entry["outputSchema"] = tool.OutputSchema
		}
		out = append(out, entry)
	}
	return out
}

func callMCPTool(ctx context.Context, backend MCPBackend, name string, args map[string]any) (any, error) {
	tool, ok := mcpTool(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	requestPath := tool.Path
	switch name {
	case "node_create_link":
		return backend.Call(ctx, http.MethodPost, requestPath, args)
	case "node_update_link":
		localPort, err := intArgument(args, "localPort")
		if err != nil {
			return nil, err
		}
		requestPath = strings.ReplaceAll(requestPath, "{localPort}", strconv.Itoa(localPort))
		return backend.Call(ctx, http.MethodPut, requestPath, map[string]any{
			"targetDeviceId": args["targetDeviceId"],
			"targetPort":     args["targetPort"],
		})
	case "node_delete_link":
		localPort, err := intArgument(args, "localPort")
		if err != nil {
			return nil, err
		}
		requestPath = strings.ReplaceAll(requestPath, "{localPort}", strconv.Itoa(localPort))
		return backend.Call(ctx, http.MethodDelete, requestPath, nil)
	default:
		return backend.Call(ctx, tool.Method, requestPath, nil)
	}
}

func intArgument(args map[string]any, key string) (int, error) {
	switch value := args[key].(type) {
	case float64:
		if value != float64(int(value)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(value), nil
	case int:
		return value, nil
	case json.Number:
		number, err := value.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(number), nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func mcpTool(name string) (AutomationTool, bool) {
	for _, tool := range AutomationTools() {
		if tool.Name == name {
			return tool, true
		}
	}
	return AutomationTool{}, false
}

func toolSuccessResult(value any) map[string]any {
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": mustCompactJSON(value)}},
		"structuredContent": value,
		"isError":           false,
	}
}

func toolErrorResult(err error) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}

func mustCompactJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"error":"unable to encode result"}`
	}
	return string(raw)
}

func mcpResources() []map[string]any {
	return []map[string]any{
		{"uri": "warpmesh://node/guide", "name": "AI operating guide", "description": "Capabilities, authentication, workflows and operating rules.", "mimeType": "text/markdown"},
		{"uri": "warpmesh://node/status", "name": "Node status", "description": "Connection and service readiness.", "mimeType": "application/json"},
		{"uri": "warpmesh://node/links", "name": "Node links", "description": "TCP links and peer endpoints.", "mimeType": "application/json"},
		{"uri": "warpmesh://node/peers", "name": "Node peers", "description": "Links grouped by remote device.", "mimeType": "application/json"},
		{"uri": "warpmesh://node/diagnostics", "name": "Node diagnostics", "description": "Runtime diagnostics.", "mimeType": "application/json"},
	}
}

func readMCPResource(ctx context.Context, backend MCPBackend, uri string) (string, string, error) {
	if uri == "warpmesh://node/guide" {
		return "text/markdown", AIGuideMD, nil
	}
	paths := map[string]string{
		"warpmesh://node/status":      "/api/v1/status",
		"warpmesh://node/links":       "/api/v1/links",
		"warpmesh://node/peers":       "/api/v1/peers",
		"warpmesh://node/diagnostics": "/api/v1/diagnostics",
	}
	requestPath, ok := paths[uri]
	if !ok {
		return "", "", errors.New("unknown resource")
	}
	result, err := backend.Call(ctx, http.MethodGet, requestPath, nil)
	if err != nil {
		return "", "", err
	}
	return "application/json", mustCompactJSON(result), nil
}
