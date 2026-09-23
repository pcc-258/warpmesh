package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.reg.ListUsers())
	case http.MethodPost:
		var req struct {
			Username  string    `json:"username"`
			Password  string    `json:"password"`
			Role      string    `json:"role"`
			DeviceIDs []string  `json:"deviceIds"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Role == "" {
			req.Role = "operator"
		}
		if err := s.reg.CreateUser(req.Username, req.Password, req.Role, req.DeviceIDs, req.ExpiresAt); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "user.create", req.Username, req.Role)
		writeJSON(w, http.StatusCreated, map[string]any{"username": req.Username})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUserByUsername(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	username := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if username == "" || strings.Contains(username, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing username"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		user, err := s.reg.GetUser(username)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodPatch:
		var req struct {
			Password  *string    `json:"password"`
			Role      *string    `json:"role"`
			DeviceIDs *[]string  `json:"deviceIds"`
			ExpiresAt *time.Time `json:"expiresAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.reg.UpdateUser(username, req.Password, req.Role, req.DeviceIDs, req.ExpiresAt); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "user.update", username, "")
		writeJSON(w, http.StatusOK, map[string]any{"username": username})
	case http.MethodDelete:
		user, err := s.reg.GetUser(username)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
			return
		}
		if user.Role == "admin" {
			admins := 0
			for _, u := range s.reg.ListUsers() {
				if u.Role == "admin" {
					admins++
				}
			}
			if admins <= 1 {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "cannot delete the last admin"})
				return
			}
		}
		if err := s.reg.DeleteUser(username); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "user.delete", username, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
