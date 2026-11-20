package node

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *Node) startLocalUI() {
	handler := a.localUIHandler()
	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", a.cfg.UIPort)
		log.Printf("local ui: http://%s", addr)
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Printf("local ui stopped: %v", err)
		}
	}()
}

func (a *Node) localUIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.uiIndex)
	mux.HandleFunc("/api/overview", a.uiOverview)
	mux.HandleFunc("/api/forwards", a.uiForwards)
	mux.HandleFunc("/api/forwards/", a.uiForwardByPort)
	mux.HandleFunc("/api/peers", a.uiPeers)
	mux.HandleFunc("/api/diagnostics", a.uiDiagnostics)
	a.registerAutomationRoutes(mux)
	return mux
}

func (a *Node) uiIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(uiPage))
}

func (a *Node) uiForwards(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeLocalJSON(w, http.StatusOK, map[string]any{
			"device":   a.cfg.DeviceID,
			"forwards": a.ListForwardDetails(),
		})
	case http.MethodPost:
		var req ForwardSpec
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.StartForward(req); err != nil {
			writeLocalJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeLocalJSON(w, http.StatusCreated, req)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *Node) uiForwardByPort(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/forwards/")
	port, err := strconv.Atoi(raw)
	if err != nil {
		writeLocalJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid port"})
		return
	}
	if err := a.StopForward(port); err != nil {
		writeLocalJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type localOverview struct {
	DeviceID       string   `json:"deviceId"`
	DeviceName     string   `json:"deviceName"`
	Hostname       string   `json:"hostname"`
	OS             string   `json:"os"`
	Arch           string   `json:"arch"`
	ServerURL      string   `json:"serverUrl"`
	ConsoleURL     string   `json:"consoleUrl"`
	Connected      bool     `json:"connected"`
	UptimeSeconds  int64    `json:"uptimeSeconds"`
	Terminal       bool     `json:"terminal"`
	Files          bool     `json:"files"`
	Desktop        bool     `json:"desktop"`
	DesktopHint    string   `json:"desktopHint"`
	VNCPort        int      `json:"vncPort"`
	ForwardCount   int      `json:"forwardCount"`
	ActiveForwards int      `json:"activeForwards"`
	AllowedPaths   []string `json:"allowedPaths"`
}

func (a *Node) uiOverview(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeLocalJSON(w, http.StatusOK, a.overviewSnapshot())
}

func (a *Node) overviewSnapshot() localOverview {
	caps := a.capabilities()
	forwards := a.ListForwardDetails()
	active := 0
	for _, forward := range forwards {
		active += forward.Active
	}
	paths := append([]string(nil), a.cfg.AllowPaths...)
	if paths == nil {
		paths = []string{}
	}
	a.mu.Lock()
	connected := a.ws != nil
	a.mu.Unlock()
	return localOverview{
		DeviceID:       a.cfg.DeviceID,
		DeviceName:     a.cfg.Name,
		Hostname:       a.cfg.Name,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		ServerURL:      a.cfg.ServerURL,
		ConsoleURL:     consoleURL(a.cfg.ServerURL),
		Connected:      connected,
		UptimeSeconds:  int64(time.Since(a.startedAt).Seconds()),
		Terminal:       caps.Terminal,
		Files:          caps.Files,
		Desktop:        caps.Desktop,
		DesktopHint:    caps.DesktopHint,
		VNCPort:        a.cfg.VNCPort,
		ForwardCount:   len(forwards),
		ActiveForwards: active,
		AllowedPaths:   paths,
	}
}

type localPeer struct {
	DeviceID     string `json:"deviceId"`
	ForwardCount int    `json:"forwardCount"`
	Active       int    `json:"active"`
	LocalPorts   []int  `json:"localPorts"`
	TargetPorts  []int  `json:"targetPorts"`
}

func (a *Node) uiPeers(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	byDevice := make(map[string]*localPeer)
	for _, detail := range a.ListForwardDetails() {
		peer := byDevice[detail.TargetDeviceID]
		if peer == nil {
			peer = &localPeer{
				DeviceID:    detail.TargetDeviceID,
				LocalPorts:  []int{},
				TargetPorts: []int{},
			}
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
	writeLocalJSON(w, http.StatusOK, peers)
}

func (a *Node) uiDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.mu.Lock()
	connected := a.ws != nil
	a.mu.Unlock()
	caps := a.capabilities()
	paths := append([]string(nil), a.cfg.AllowPaths...)
	if paths == nil {
		paths = []string{}
	}
	writeLocalJSON(w, http.StatusOK, map[string]any{
		"deviceId":       a.cfg.DeviceID,
		"deviceName":     a.cfg.Name,
		"serverUrl":      a.cfg.ServerURL,
		"consoleUrl":     consoleURL(a.cfg.ServerURL),
		"connected":      connected,
		"uptimeSeconds":  int64(time.Since(a.startedAt).Seconds()),
		"goVersion":      runtime.Version(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"uiPort":         a.cfg.UIPort,
		"vncPort":        a.cfg.VNCPort,
		"desktopReady":   caps.Desktop,
		"terminalReady":  caps.Terminal,
		"fileReady":      caps.Files,
		"allowedPaths":   paths,
		"forwardCount":   len(a.ListForwardDetails()),
		"securityModel":  "Server-managed operator and forwarding authorization",
		"websocketState": connectedState(connected),
	})
}

func consoleURL(nodeURL string) string {
	u, err := url.Parse(nodeURL)
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func connectedState(connected bool) string {
	if connected {
		return "connected"
	}
	return "reconnecting"
}

// Loopback binding alone does not prevent browser CSRF or DNS rebinding.
// Origin-less clients remain supported, but browser origins must match exactly.
func localUIRequest(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	allowed := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		allowed = allowed && err == nil && u.Scheme == "http" && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		allowed = false
	}
	if !allowed {
		writeLocalJSON(w, http.StatusForbidden, map[string]any{"error": "local same-origin requests only"})
	}
	return allowed
}

func writeLocalJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
