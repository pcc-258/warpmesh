package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var agentPlatforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}

func (s *Server) handleAgentDownloads(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	platform := strings.TrimPrefix(r.URL.Path, "/api/agent-downloads")
	if platform == "" || platform == "/" {
		type build struct {
			Platform  string `json:"platform"`
			Available bool   `json:"available"`
		}
		builds := make([]build, 0, len(agentPlatforms))
		for _, name := range agentPlatforms {
			_, ok := s.agentBinary(name)
			builds = append(builds, build{name, ok})
		}
		writeJSON(w, http.StatusOK, builds)
		return
	}
	platform = strings.TrimPrefix(platform, "/")
	path, ok := s.agentBinary(platform)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Agent build is not available for this platform."})
		return
	}
	filename := "warpmesh-agent"
	if strings.HasPrefix(platform, "windows-") {
		filename += ".exe"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeFile(w, r, path)
}

func (s *Server) agentBinary(platform string) (string, bool) {
	if s.cfg.AgentDir == "" {
		return "", false
	}
	for _, allowed := range agentPlatforms {
		if platform != allowed {
			continue
		}
		name := "warpmesh-agent-" + allowed
		if strings.HasPrefix(platform, "windows-") {
			name += ".exe"
		}
		path := filepath.Join(s.cfg.AgentDir, name)
		info, err := os.Stat(path)
		return path, err == nil && info.Mode().IsRegular()
	}
	return "", false
}
