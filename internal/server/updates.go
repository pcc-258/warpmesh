package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

type nodeUpdateBuild struct {
	Platform string `json:"platform"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func (s *Server) handleNodeUpdates(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	builds := make([]nodeUpdateBuild, 0, len(nodePlatforms))
	for _, platform := range nodePlatforms {
		if build, err := s.updateBuild(platform); err == nil {
			builds = append(builds, build)
		}
	}
	writeJSON(w, http.StatusOK, builds)
}

func (s *Server) handleNodeUpdateStart(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deviceID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/update")
	if deviceID == "" || strings.Contains(deviceID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid device id"})
		return
	}
	device, found := s.reg.Get(deviceID)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "device not found"})
		return
	}
	if device.UpdateState == "succeeded" {
		_ = s.reg.SetUpdateState(deviceID, "idle", device.UpdateVersion, "", s.updateService())
		device.UpdateState = "idle"
	}
	conn := s.getNode(deviceID)
	if conn == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device is offline"})
		return
	}
	platform := device.OS + "-" + device.Arch
	build, err := s.updateBuild(platform)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid update request"})
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request body must contain one JSON object"})
			return
		}
	}
	if request.Version != "" && request.Version != build.Version {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "requested version is not installed on the server"})
		return
	}
	if len(build.Version) > 128 || len(build.SHA256) != 64 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node release metadata is invalid"})
		return
	}
	if device.UpdateState == "downloading" || device.UpdateState == "verifying" || device.UpdateState == "installing" || device.UpdateState == "restarting" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device already has an update in progress"})
		return
	}
	if device.Version == build.Version {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "device already runs the target version"})
		return
	}
	updateID := newID()
	platformQuery := url.QueryEscape(platform)
	downloadURL := "/api/node-update/download?device=" + url.QueryEscape(deviceID) + "&platform=" + platformQuery
	if err := conn.write(protocol.Message{
		Type:          protocol.TypeNodeUpdate,
		UpdateID:      updateID,
		UpdateVersion: build.Version,
		UpdateSHA256:  build.SHA256,
		UpdateURL:     downloadURL,
		UpdateService: s.updateService(),
	}); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "send update task: " + err.Error()})
		return
	}
	_ = s.reg.SetUpdateState(deviceID, "queued", build.Version, "", s.updateService())
	_ = s.reg.RecordAudit(actor, "node.update.start", deviceID, build.Version)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"updateId": updateID,
		"deviceId": deviceID,
		"version":  build.Version,
		"state":    "queued",
	})
}

func (s *Server) handleNodeUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deviceID := r.URL.Query().Get("device")
	platform := r.URL.Query().Get("platform")
	if deviceID == "" || !s.authorizeNode(deviceID, bearerToken(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid node credential"})
		return
	}
	path, ok := s.nodeBinary(platform)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node build not found"})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (s *Server) updateBuild(platform string) (nodeUpdateBuild, error) {
	path, ok := s.nodeBinary(platform)
	if !ok {
		return nodeUpdateBuild{}, fmt.Errorf("node build is not available for %s", platform)
	}
	versionBytes, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.Base(path)+".version"))
	if err != nil {
		output, execErr := exec.Command(path, "version").Output()
		if execErr != nil {
			return nodeUpdateBuild{}, fmt.Errorf("node version metadata is missing for %s", platform)
		}
		versionBytes = output
	}
	version := strings.TrimSpace(string(versionBytes))
	if fields := strings.Fields(version); len(fields) >= 2 && fields[0] == "warpmesh-node" {
		version = fields[1]
	}
	if version == "" {
		return nodeUpdateBuild{}, fmt.Errorf("node version metadata is empty for %s", platform)
	}
	file, err := os.Open(path)
	if err != nil {
		return nodeUpdateBuild{}, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return nodeUpdateBuild{}, err
	}
	return nodeUpdateBuild{Platform: platform, Version: version, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}
