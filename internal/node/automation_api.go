package node

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const automationMaxBodyBytes = 1 << 20

type automationStatus struct {
	localOverview
	APIVersion   string `json:"apiVersion"`
	APIBaseURL   string `json:"apiBaseUrl"`
	ConsoleURL   string `json:"consoleUrl"`
	TerminalURL  string `json:"terminalUrl"`
	DesktopURL   string `json:"desktopUrl"`
	FilesURL     string `json:"filesUrl"`
	AccessURL    string `json:"accessUrl"`
	DevicesURL   string `json:"devicesUrl"`
	TokenFile    string `json:"tokenFile"`
	MCPTransport string `json:"mcpTransport"`
}

type consoleTargets struct {
	DeviceID    string `json:"deviceId"`
	ConsoleURL  string `json:"consoleUrl"`
	TerminalURL string `json:"terminalUrl"`
	DesktopURL  string `json:"desktopUrl"`
	FilesURL    string `json:"filesUrl"`
	AccessURL   string `json:"accessUrl"`
	DevicesURL  string `json:"devicesUrl"`
}

type automationError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Node) registerAutomationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/capabilities", a.automationCapabilities)
	mux.HandleFunc("/api/v1/ai-guide", a.automationAIGuide)
	mux.HandleFunc("/api/v1/status", a.automationStatusHandler)
	mux.HandleFunc("/api/v1/links", a.automationLinks)
	mux.HandleFunc("/api/v1/links/", a.automationLinkByPort)
	mux.HandleFunc("/api/v1/peers", a.automationPeers)
	mux.HandleFunc("/api/v1/diagnostics", a.automationDiagnostics)
	mux.HandleFunc("/api/v1/console-targets", a.automationConsoleTargets)
}

func (a *Node) automationCapabilities(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, true) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeAutomationJSON(w, http.StatusOK, map[string]any{
		"apiVersion":        AutomationAPIVersion,
		"authentication":    "Bearer token",
		"tokenFile":         UITokenFilename,
		"tokenMode":         "owner-readable data-dir/ui-token",
		"mcpTransport":      "stdio",
		"mcpCommand":        "warpmesh-node mcp -data-dir <data-dir>",
		"aiGuideURL":        "/api/v1/ai-guide",
		"mcpGuideResource":  "warpmesh://node/guide",
		"tools":             AutomationTools(),
		"authorizationNote": "Server-side operator and link authorization remains authoritative.",
	})
}

func (a *Node) automationAIGuide(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, true) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(AIGuideMD))
}

func (a *Node) automationStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	targets := a.consoleTargets()
	overview := a.overviewSnapshot()
	writeAutomationJSON(w, http.StatusOK, automationStatus{
		localOverview: overview,
		APIVersion:    AutomationAPIVersion,
		APIBaseURL:    fmt.Sprintf("http://127.0.0.1:%d/api/v1", a.cfg.UIPort),
		ConsoleURL:    targets.ConsoleURL,
		TerminalURL:   targets.TerminalURL,
		DesktopURL:    targets.DesktopURL,
		FilesURL:      targets.FilesURL,
		AccessURL:     targets.AccessURL,
		DevicesURL:    targets.DevicesURL,
		TokenFile:     UITokenFilename,
		MCPTransport:  "stdio",
	})
}

func (a *Node) automationLinks(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeAutomationJSON(w, http.StatusOK, a.ListForwardDetails())
	case http.MethodPost:
		var spec ForwardSpec
		if !decodeAutomationJSON(w, r, &spec) {
			return
		}
		if err := a.StartForward(spec); err != nil {
			status := http.StatusConflict
			if validateForwardSpec(spec) != nil {
				status = http.StatusBadRequest
			}
			writeAutomationError(w, status, "link_create_failed", err.Error())
			return
		}
		detail, _ := a.forwardDetail(spec.LocalPort)
		writeAutomationJSON(w, http.StatusCreated, detail)
	default:
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
	}
}

func (a *Node) automationLinkByPort(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/v1/links/")
	if raw == "" || strings.Contains(raw, "/") {
		writeAutomationError(w, http.StatusBadRequest, "invalid_local_port", "localPort is required")
		return
	}
	localPort, err := strconv.Atoi(raw)
	if err != nil || localPort < 1 || localPort > 65535 {
		writeAutomationError(w, http.StatusBadRequest, "invalid_local_port", "localPort must be between 1 and 65535")
		return
	}
	switch r.Method {
	case http.MethodGet:
		detail, ok := a.forwardDetail(localPort)
		if !ok {
			writeAutomationError(w, http.StatusNotFound, "link_not_found", "link not found")
			return
		}
		writeAutomationJSON(w, http.StatusOK, detail)
	case http.MethodPut:
		var spec ForwardSpec
		if !decodeAutomationJSON(w, r, &spec) {
			return
		}
		spec.LocalPort = localPort
		if err := validateForwardSpec(spec); err != nil {
			writeAutomationError(w, http.StatusBadRequest, "invalid_link", err.Error())
			return
		}
		if err := a.UpdateForward(spec); err != nil {
			status := http.StatusConflict
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeAutomationError(w, status, "link_update_failed", err.Error())
			return
		}
		detail, _ := a.forwardDetail(localPort)
		writeAutomationJSON(w, http.StatusOK, detail)
	case http.MethodDelete:
		if err := a.StopForward(localPort); err != nil {
			writeAutomationError(w, http.StatusNotFound, "link_not_found", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET, PUT, or DELETE")
	}
}

func (a *Node) automationPeers(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeAutomationJSON(w, http.StatusOK, a.peerSnapshot())
}

func (a *Node) automationDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeAutomationJSON(w, http.StatusOK, a.diagnosticsSnapshot())
}

func (a *Node) automationConsoleTargets(w http.ResponseWriter, r *http.Request) {
	if !a.automationRequest(w, r, false) {
		return
	}
	if r.Method != http.MethodGet {
		writeAutomationError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeAutomationJSON(w, http.StatusOK, a.consoleTargets())
}

func (a *Node) automationRequest(w http.ResponseWriter, r *http.Request, public bool) bool {
	if !loopbackRequestHost(r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeAutomationError(w, http.StatusForbidden, "local_only", "local loopback requests only")
		return false
	}
	if public {
		return true
	}
	if len(a.uiToken) < 32 {
		writeAutomationError(w, http.StatusServiceUnavailable, "automation_unavailable", "local automation token is not configured")
		return false
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") {
		writeAutomationError(w, http.StatusUnauthorized, "unauthorized", "Bearer token required")
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if len(provided) != len(a.uiToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(a.uiToken)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="WarpMesh Node Automation"`)
		writeAutomationError(w, http.StatusUnauthorized, "unauthorized", "invalid automation token")
		return false
	}
	return true
}

func loopbackRequestHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func decodeAutomationJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, automationMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAutomationError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAutomationError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeAutomationJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAutomationError(w http.ResponseWriter, status int, code, message string) {
	var response automationError
	response.Error.Code = code
	response.Error.Message = message
	writeAutomationJSON(w, status, response)
}

func (a *Node) forwardDetail(localPort int) (ForwardDetail, bool) {
	for _, detail := range a.ListForwardDetails() {
		if detail.LocalPort == localPort {
			return detail, true
		}
	}
	return ForwardDetail{}, false
}

func (a *Node) peerSnapshot() []localPeer {
	byDevice := make(map[string]*localPeer)
	for _, detail := range a.ListForwardDetails() {
		peer := byDevice[detail.TargetDeviceID]
		if peer == nil {
			peer = &localPeer{DeviceID: detail.TargetDeviceID, LocalPorts: []int{}, TargetPorts: []int{}}
			byDevice[detail.TargetDeviceID] = peer
		}
		peer.ForwardCount++
		peer.Active += detail.Active
		peer.LocalPorts = append(peer.LocalPorts, detail.LocalPort)
		peer.TargetPorts = append(peer.TargetPorts, detail.TargetPort)
	}
	peers := make([]localPeer, 0, len(byDevice))
	for _, peer := range byDevice {
		sort.Ints(peer.LocalPorts)
		sort.Ints(peer.TargetPorts)
		peers = append(peers, *peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].DeviceID < peers[j].DeviceID })
	return peers
}

func (a *Node) diagnosticsSnapshot() map[string]any {
	overview := a.overviewSnapshot()
	return map[string]any{
		"apiVersion":     AutomationAPIVersion,
		"deviceId":       overview.DeviceID,
		"deviceName":     overview.DeviceName,
		"serverUrl":      overview.ServerURL,
		"consoleUrl":     overview.ConsoleURL,
		"connected":      overview.Connected,
		"uptimeSeconds":  overview.UptimeSeconds,
		"os":             overview.OS,
		"arch":           overview.Arch,
		"uiPort":         a.cfg.UIPort,
		"vncPort":        overview.VNCPort,
		"desktopReady":   overview.Desktop,
		"terminalReady":  overview.Terminal,
		"fileReady":      overview.Files,
		"allowedPaths":   overview.AllowedPaths,
		"forwardCount":   overview.ForwardCount,
		"activeForwards": overview.ActiveForwards,
		"securityModel":  "Server-managed operator and forwarding authorization",
		"tokenFile":      UITokenFilename,
		"mcpTransport":   "stdio",
	}
}

func (a *Node) consoleTargets() consoleTargets {
	overview := a.overviewSnapshot()
	base := strings.TrimRight(overview.ConsoleURL, "/")
	device := url.PathEscape(overview.DeviceID)
	target := func(fragment string) string {
		if base == "" {
			return ""
		}
		return base + fragment
	}
	return consoleTargets{
		DeviceID:    overview.DeviceID,
		ConsoleURL:  target("/"),
		TerminalURL: target("/#/terminal/" + device),
		DesktopURL:  target("/#/desktop/" + device),
		FilesURL:    target("/#/files/" + device),
		AccessURL:   target("/access"),
		DevicesURL:  target("/devices"),
	}
}
