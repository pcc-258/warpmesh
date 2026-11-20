package node

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type fakeMCPBackend struct {
	method string
	path   string
	body   any
	result any
	err    error
}

func (f *fakeMCPBackend) Call(_ context.Context, method, path string, body any) (any, error) {
	f.method, f.path, f.body = method, path, body
	return f.result, f.err
}

func mcpMessages(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	messages := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func TestMCPInitializeAndToolDiscovery(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n",
	)
	var output bytes.Buffer
	if err := ServeMCP(context.Background(), input, &output, &fakeMCPBackend{}); err != nil {
		t.Fatal(err)
	}
	messages := mcpMessages(t, output.Bytes())
	if len(messages) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(messages))
	}
	initResult := messages[0]["result"].(map[string]any)
	if initResult["protocolVersion"] != MCPProtocolVersion {
		t.Fatalf("unexpected initialize: %+v", initResult)
	}
	toolsResult := messages[1]["result"].(map[string]any)
	tools := toolsResult["tools"].([]any)
	if len(tools) != len(AutomationTools()) {
		t.Fatalf("tool count=%d", len(tools))
	}
}

func TestControlClientAcceptsDocumentedAPIPrefix(t *testing.T) {
	client := NewControlClient("http://127.0.0.1:9876/api/v1/", "token")
	if client.BaseURL != "http://127.0.0.1:9876" {
		t.Fatalf("unexpected base URL: %s", client.BaseURL)
	}
}

func TestMCPCreateLinkCallsStableAPIContract(t *testing.T) {
	backend := &fakeMCPBackend{result: map[string]any{"localPort": 2222, "targetDeviceId": "dev-a", "targetPort": 22}}
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"node_create_link","arguments":{"localPort":2222,"targetDeviceId":"dev-a","targetPort":22}}}` + "\n",
	)
	var output bytes.Buffer
	if err := ServeMCP(context.Background(), input, &output, backend); err != nil {
		t.Fatal(err)
	}
	if backend.method != http.MethodPost || backend.path != "/api/v1/links" {
		t.Fatalf("unexpected call: %s %s", backend.method, backend.path)
	}
	body := backend.body.(map[string]any)
	if body["targetDeviceId"] != "dev-a" {
		t.Fatalf("unexpected body: %+v", body)
	}
	messages := mcpMessages(t, output.Bytes())
	result := messages[0]["result"].(map[string]any)
	if result["isError"] != false || result["structuredContent"] == nil {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestMCPResourcesExposeReadOnlySnapshots(t *testing.T) {
	backend := &fakeMCPBackend{result: []any{}}
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":8,"method":"resources/list","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"warpmesh://node/links"}}` + "\n",
	)
	var output bytes.Buffer
	if err := ServeMCP(context.Background(), input, &output, backend); err != nil {
		t.Fatal(err)
	}
	messages := mcpMessages(t, output.Bytes())
	resources := messages[0]["result"].(map[string]any)["resources"].([]any)
	if len(resources) != 5 {
		t.Fatalf("unexpected resources: %d", len(resources))
	}
	if backend.method != http.MethodGet || backend.path != "/api/v1/links" {
		t.Fatalf("resource read used %s %s", backend.method, backend.path)
	}
}

func TestMCPGuideResourceDoesNotRequireLocalAPI(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":10,"method":"resources/read","params":{"uri":"warpmesh://node/guide"}}` + "\n",
	)
	var output bytes.Buffer
	if err := ServeMCP(context.Background(), input, &output, &fakeMCPBackend{}); err != nil {
		t.Fatal(err)
	}
	messages := mcpMessages(t, output.Bytes())
	contents := messages[0]["result"].(map[string]any)["contents"].([]any)
	content := contents[0].(map[string]any)
	if content["mimeType"] != "text/markdown" || !strings.Contains(content["text"].(string), "# WarpMesh AI Guide") {
		t.Fatalf("unexpected guide resource: %+v", content)
	}
}
