package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutomationCapabilitiesRequireAuthentication(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/automation/capabilities", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestAutomationCapabilitiesExposeServerOperations(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	request := httptest.NewRequest(http.MethodGet, "/api/automation/capabilities", nil)
	request.Header.Set("Authorization", "Bearer admin-token")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		APIVersion string           `json:"apiVersion"`
		Tools      []automationTool `json:"tools"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.APIVersion != "v1" || len(response.Tools) < 22 {
		t.Fatalf("unexpected capability response: %+v", response)
	}
	if !strings.Contains(recorder.Body.String(), `"/api/automation/ai-guide"`) {
		t.Fatal("server capabilities do not advertise the AI guide")
	}
	seen := make(map[string]bool)
	for _, tool := range response.Tools {
		seen[tool.Name] = true
		if tool.Method == "" || tool.Path == "" || tool.InputSchema == nil || tool.Access == "" {
			t.Fatalf("incomplete tool: %+v", tool)
		}
	}
	for _, name := range []string{
		"server_update_device",
		"server_create_forward_grant",
		"server_create_user",
		"server_rotate_device_key",
		"server_create_invite",
		"server_start_node_update",
	} {
		if !seen[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestServerAIGuideRequiresAuthentication(t *testing.T) {
	srv, err := NewServer(Config{AdminToken: "admin-token", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	unauthorized := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/automation/ai-guide", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/automation/ai-guide", nil)
	request.Header.Set("Authorization", "Bearer admin-token")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "# WarpMesh Server AI Guide") {
		t.Fatalf("guide status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
