package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if !s.adminFromRequest(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.reg.List())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeviceByID(w http.ResponseWriter, r *http.Request) {
	if !s.adminFromRequest(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.reg.Rename(id, req.Name); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		d, _ := s.reg.Get(id)
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if err := s.reg.Delete(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
