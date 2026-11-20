package node

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func automationTestNode(t *testing.T) *Node {
	t.Helper()
	a := testNode(t)
	a.uiToken = strings.Repeat("a", 64)
	return a
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func automationRequest(method, target, body, token string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestAutomationAPIRegistrationLifecycle(t *testing.T) {
	a := automationTestNode(t)
	token := a.uiToken
	port := freeLocalPort(t)

	unauthorized := httptest.NewRecorder()
	a.automationLinks(unauthorized, automationRequest(http.MethodGet, "http://127.0.0.1:8090/api/v1/links", "", ""))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	create := httptest.NewRecorder()
	body := `{"localPort":` + itoa(port) + `,"targetDeviceId":"dev-peer","targetPort":22}`
	a.automationLinks(create, automationRequest(http.MethodPost, "http://127.0.0.1:8090/api/v1/links", body, token))
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	t.Cleanup(func() { _ = a.StopForward(port) })

	update := httptest.NewRecorder()
	updateBody := `{"targetDeviceId":"dev-next","targetPort":443}`
	a.automationLinkByPort(update, automationRequest(http.MethodPut, "http://127.0.0.1:8090/api/v1/links/"+itoa(port), updateBody, token))
	if update.Code != http.StatusOK {
		t.Fatalf("update=%d %s", update.Code, update.Body.String())
	}
	var detail ForwardDetail
	if err := json.Unmarshal(update.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.TargetDeviceID != "dev-next" || detail.TargetPort != 443 {
		t.Fatalf("unexpected updated link: %+v", detail)
	}

	list := httptest.NewRecorder()
	a.automationLinks(list, automationRequest(http.MethodGet, "http://127.0.0.1:8090/api/v1/links", "", token))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"targetDeviceId":"dev-next"`) {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}

	remove := httptest.NewRecorder()
	a.automationLinkByPort(remove, automationRequest(http.MethodDelete, "http://127.0.0.1:8090/api/v1/links/"+itoa(port), "", token))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", remove.Code, remove.Body.String())
	}
}

func TestAutomationCapabilitiesAreDiscoverable(t *testing.T) {
	a := automationTestNode(t)
	recorder := httptest.NewRecorder()
	a.automationCapabilities(recorder, automationRequest(http.MethodGet, "http://127.0.0.1:8090/api/v1/capabilities", "", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("capabilities=%d %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		APIVersion string           `json:"apiVersion"`
		Tools      []AutomationTool `json:"tools"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.APIVersion != AutomationAPIVersion || len(result.Tools) < 8 {
		t.Fatalf("unexpected capabilities: %+v", result)
	}
	if !strings.Contains(recorder.Body.String(), `"/api/v1/ai-guide"`) {
		t.Fatal("capabilities do not advertise the AI guide")
	}
	for _, tool := range result.Tools {
		if tool.Name == "" || tool.Method == "" || tool.Path == "" || tool.InputSchema == nil || tool.Annotations == nil {
			t.Fatalf("incomplete tool: %+v", tool)
		}
	}
}

func TestAIGuideIsPublicButLoopbackOnly(t *testing.T) {
	a := automationTestNode(t)
	recorder := httptest.NewRecorder()
	a.automationAIGuide(recorder, automationRequest(http.MethodGet, "http://127.0.0.1:8090/api/v1/ai-guide", "", ""))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "# WarpMesh AI Guide") {
		t.Fatalf("guide=%d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("content type=%q", recorder.Header().Get("Content-Type"))
	}
	rebinding := httptest.NewRecorder()
	a.automationAIGuide(rebinding, automationRequest(http.MethodGet, "http://attacker.example:8090/api/v1/ai-guide", "", ""))
	if rebinding.Code != http.StatusForbidden {
		t.Fatalf("guide rebinding status=%d", rebinding.Code)
	}
}

func TestAutomationAPIRejectsBadTokensAndRebinding(t *testing.T) {
	a := automationTestNode(t)
	wrong := httptest.NewRecorder()
	a.automationLinks(wrong, automationRequest(http.MethodGet, "http://127.0.0.1:8090/api/v1/links", "", strings.Repeat("b", 64)))
	if wrong.Code != http.StatusUnauthorized || wrong.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("wrong token status=%d headers=%v", wrong.Code, wrong.Header())
	}
	rebindingRequest := automationRequest(http.MethodGet, "http://attacker.example:8090/api/v1/links", "", a.uiToken)
	rebinding := httptest.NewRecorder()
	a.automationLinks(rebinding, rebindingRequest)
	if rebinding.Code != http.StatusForbidden {
		t.Fatalf("rebinding status=%d", rebinding.Code)
	}
}

func TestAutomationTokenFileIsStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateUIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateUIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || second != first {
		t.Fatalf("token is not stable: %q %q", first, second)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(UITokenPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("token mode=%o want 600", info.Mode().Perm())
		}
	}
}

func TestControlClientMCPBackendUsesHTTPAutomationAPI(t *testing.T) {
	a := automationTestNode(t)
	server := httptest.NewServer(a.localUIHandler())
	defer server.Close()
	client := NewControlClient(server.URL+"/api/v1", a.uiToken)

	status, err := client.Call(t.Context(), http.MethodGet, "/api/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.(map[string]any)["deviceId"] != a.cfg.DeviceID {
		t.Fatalf("unexpected status: %+v", status)
	}

	port := freeLocalPort(t)
	created, err := client.Call(t.Context(), http.MethodPost, "/api/v1/links", map[string]any{
		"localPort":      port,
		"targetDeviceId": "dev-node-client",
		"targetPort":     22,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.StopForward(port) })
	if created.(map[string]any)["targetDeviceId"] != "dev-node-client" {
		t.Fatalf("unexpected created link: %+v", created)
	}
	if _, err := client.Call(t.Context(), http.MethodDelete, "/api/v1/links/"+itoa(port), nil); err != nil {
		t.Fatal(err)
	}
}
