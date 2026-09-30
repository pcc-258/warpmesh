package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	devices := s.reg.List()
	if !s.isAdmin(actor) {
		user, _ := s.actorInfo(actor)
		allowed := make(map[string]bool)
		for _, id := range user.DeviceIDs {
			allowed[id] = true
		}
		filtered := make([]Device, 0)
		for _, d := range devices {
			if allowed[d.ID] {
				filtered = append(filtered, d)
			}
		}
		devices = filtered
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) handleDeviceByID(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		if !s.isAdmin(actor) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
			return
		}
		var req struct {
			Name          string `json:"name"`
			Group         string `json:"group"`
			ForwardPolicy string `json:"forwardPolicy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Name != "" {
			if err := s.reg.Rename(id, req.Name); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			_ = s.reg.RecordAudit(actor, "device.rename", id, req.Name)
		}
		if req.Group != "" {
			if err := s.reg.SetGroup(id, req.Group); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			_ = s.reg.RecordAudit(actor, "device.group", id, req.Group)
		}
		if req.ForwardPolicy != "" {
			if !ValidForwardPolicy(req.ForwardPolicy) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "forwardPolicy must be 'any' or 'restricted'"})
				return
			}
			if err := s.reg.SetForwardPolicy(id, req.ForwardPolicy); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			_ = s.reg.RecordAudit(actor, "device.forward-policy", id, req.ForwardPolicy)
		}
		d, _ := s.reg.Get(id)
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if !s.isAdmin(actor) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
			return
		}
		if err := s.reg.Delete(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "device.delete", id, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// forwardGrantRequest is the payload for creating or deleting a forward grant.
// The DELETE form may pass the same fields as query parameters so callers that
// cannot send a request body still address one exact grant.
type forwardGrantRequest struct {
	SourceDevice string `json:"sourceDevice"`
	TargetDevice string `json:"targetDevice"`
	PortMin      int    `json:"portMin"`
	PortMax      int    `json:"portMax"`
}

func (g forwardGrantRequest) valid() bool {
	return g.SourceDevice != "" && g.TargetDevice != "" && g.PortMin > 0 && g.PortMax > 0
}

func (g forwardGrantRequest) detail() string {
	return fmt.Sprintf("%s:%d-%d", g.TargetDevice, g.PortMin, g.PortMax)
}

// handleForwardPolicy reports every device with its forwarding policy and the
// number of grants that apply to it.
func (s *Server) handleForwardPolicy(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, s.reg.ListForwardPolicies())
}

// handleForwardGrants manages the grants that restricted devices rely on.
func (s *Server) handleForwardGrants(w http.ResponseWriter, r *http.Request) {
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
		if source := r.URL.Query().Get("sourceDevice"); source != "" {
			writeJSON(w, http.StatusOK, s.reg.ListForwardGrantsFor(source))
			return
		}
		writeJSON(w, http.StatusOK, s.reg.ListForwardGrants())
	case http.MethodPost:
		var req forwardGrantRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.reg.AddForwardGrant(req.SourceDevice, req.TargetDevice, req.PortMin, req.PortMax); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "forward-grant.create", req.SourceDevice, req.detail())
		grant, _ := s.reg.GetForwardGrant(req.SourceDevice, req.TargetDevice, req.PortMin, req.PortMax)
		writeJSON(w, http.StatusCreated, grant)
	case http.MethodDelete:
		req, ok := forwardGrantTarget(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "sourceDevice, targetDevice, portMin and portMax are required"})
			return
		}
		if err := s.reg.DeleteForwardGrant(req.SourceDevice, req.TargetDevice, req.PortMin, req.PortMax); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "forward-grant.delete", req.SourceDevice, req.detail())
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// forwardGrantTarget reads one grant identity from the query string, falling
// back to a JSON body.
func forwardGrantTarget(r *http.Request) (forwardGrantRequest, bool) {
	query := r.URL.Query()
	req := forwardGrantRequest{
		SourceDevice: query.Get("sourceDevice"),
		TargetDevice: query.Get("targetDevice"),
	}
	req.PortMin, _ = strconv.Atoi(query.Get("portMin"))
	req.PortMax, _ = strconv.Atoi(query.Get("portMax"))
	if req.valid() {
		return req, true
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.valid() {
		return req, false
	}
	return req, true
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	devices := s.reg.List()
	if !s.isAdmin(actor) {
		user, _ := s.actorInfo(actor)
		allowed := make(map[string]bool)
		for _, id := range user.DeviceIDs {
			allowed[id] = true
		}
		filtered := make([]Device, 0)
		for _, d := range devices {
			if allowed[d.ID] {
				filtered = append(filtered, d)
			}
		}
		devices = filtered
	}
	byOS := make(map[string]int)
	online := 0
	for _, d := range devices {
		byOS[d.OS]++
		if d.Online {
			online++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":          len(devices),
		"online":         online,
		"groups":         s.reg.Groups(),
		"byOS":           byOS,
		"recentActivity": s.reg.ListAudit(10),
	})
}

func (s *Server) handleDeviceKeys(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, s.reg.ListDeviceKeys())
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
			return
		}
		key, err := s.reg.CreateDeviceKey(req.Name, s.cfg.KeyTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "device-key.create", key.DeviceID, req.Name)
		writeJSON(w, http.StatusCreated, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeviceKeyByID(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	// The console addresses a specific key, and rotation appends a sub-path:
	// /api/device-keys/{id} and /api/device-keys/{id}/rotate.
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/device-keys/"), "/")
	segments := strings.Split(rest, "/")
	id := segments[0]
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	if len(segments) > 1 {
		if len(segments) != 2 || segments[1] != "rotate" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown device key operation"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "rotate requires POST"})
			return
		}
		key, err := s.reg.RotateDeviceKey(id, s.cfg.KeyTTL)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "device-key.rotate", id, "")
		writeJSON(w, http.StatusOK, key)
		return
	}
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if err := s.reg.RevokeDeviceKey(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	_ = s.reg.RecordAudit(actor, "device-key.revoke", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInvites(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, s.reg.ListInvites())
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		inv, err := s.reg.CreateInvite(req.Name, s.cfg.KeyTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		_ = s.reg.RecordAudit(actor, "invite.create", inv.Code, req.Name)
		writeJSON(w, http.StatusCreated, inv)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInviteByCode(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/api/invites/")
	if r.Method != http.MethodDelete || code == "" || strings.Contains(code, "/") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.reg.UseInvite(code); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	_ = s.reg.RecordAudit(actor, "invite.delete", code, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleEnroll exchanges a one-time invite for a long-lived device key.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	inv, err := s.reg.ValidateInvite(req.Code)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	name := req.Name
	if name == "" {
		name = inv.Name
	}
	key, err := s.reg.CreateDeviceKey(name, s.cfg.KeyTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := s.reg.UseInvite(req.Code); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.reg.RecordAudit("enroll:"+req.Code, "device.enroll", key.DeviceID, name)
	writeJSON(w, http.StatusCreated, key)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !s.isAdmin(actor) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin only"})
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, s.reg.ListAudit(limit))
}
