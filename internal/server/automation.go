package server

import "net/http"

type automationTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	InputSchema map[string]any `json:"inputSchema"`
	Access      string         `json:"access"`
}

func (s *Server) handleAutomationCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":     "v1",
		"authentication": "operator session or Bearer admin/session token",
		"authorization":  "Admin-only tools return 403 for operators.",
		"aiGuideURL":     "/api/automation/ai-guide",
		"tools":          serverAutomationTools(),
	})
}

func (s *Server) handleAutomationAIGuide(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("# WarpMesh Server AI Guide\n\n" +
		"The server capability manifest is available at `/api/automation/capabilities`.\n\n" +
		"Use the normal operator session or Bearer credential. Admin-only operations return 403 for operators.\n\n" +
		"Read devices and current policy before mutations. Terminal, desktop and file workflows remain server-authorized WebSocket/browser flows. Use `warpmesh-node terminal` or the console deep links for interactive access.\n\n" +
		"Never place credentials in URLs or logs. Forwarding grants constrain restricted source devices; an unrestricted source uses `forwardPolicy=any`.\n"))
}

func serverAutomationTools() []automationTool {
	empty := objectSchema(nil, nil)
	devicePatch := objectSchema(map[string]any{
		"deviceId":      stringSchema(),
		"name":          stringSchema(),
		"group":         stringSchema(),
		"forwardPolicy": enumSchema("any", "restricted"),
	}, nil)
	deviceID := objectSchema(map[string]any{"deviceId": stringSchema()}, []string{"deviceId"})
	forwardGrant := objectSchema(map[string]any{
		"sourceDevice": stringSchema(),
		"targetDevice": stringSchema(),
		"portMin":      integerSchema(1, 65535),
		"portMax":      integerSchema(1, 65535),
	}, []string{"sourceDevice", "targetDevice", "portMin", "portMax"})
	userCreate := objectSchema(map[string]any{
		"username":  stringSchema(),
		"password":  stringSchema(),
		"role":      enumSchema("admin", "operator"),
		"deviceIds": arraySchema(stringSchema()),
		"expiresAt": stringSchema(),
	}, []string{"username", "password", "role", "deviceIds"})
	userPatch := objectSchema(map[string]any{
		"username":  stringSchema(),
		"password":  stringSchema(),
		"role":      enumSchema("admin", "operator"),
		"deviceIds": arraySchema(stringSchema()),
		"expiresAt": map[string]any{"type": []string{"string", "null"}},
	}, []string{"username"})
	itemName := objectSchema(map[string]any{"name": stringSchema()}, []string{"name"})
	key := objectSchema(map[string]any{"deviceId": stringSchema()}, []string{"deviceId"})
	user := objectSchema(map[string]any{"username": stringSchema()}, []string{"username"})
	invite := objectSchema(map[string]any{"code": stringSchema()}, []string{"code"})

	return []automationTool{
		{"server_list_devices", "List devices visible to the authenticated operator.", http.MethodGet, "/api/devices", empty, "operator/admin"},
		{"server_get_device", "Read one device with live capabilities.", http.MethodGet, "/api/devices/{deviceId}", deviceID, "operator/admin"},
		{"server_update_device", "Rename or regroup a device, or change its forwarding policy.", http.MethodPatch, "/api/devices/{deviceId}", devicePatch, "admin"},
		{"server_delete_device", "Remove a device from the inventory.", http.MethodDelete, "/api/devices/{deviceId}", deviceID, "admin"},
		{"server_get_stats", "Read fleet totals and recent audit activity.", http.MethodGet, "/api/stats", empty, "operator/admin"},
		{"server_get_analytics", "Read connection and relay analytics for 24h or 7d.", http.MethodGet, "/api/analytics", objectSchema(map[string]any{"range": enumSchema("24h", "7d")}, []string{"range"}), "operator/admin"},
		{"server_list_audit", "Read security-relevant audit entries.", http.MethodGet, "/api/audit", objectSchema(map[string]any{"limit": integerSchema(1, 500)}, nil), "admin"},
		{"server_list_forward_policies", "List per-device forwarding policies and grant counts.", http.MethodGet, "/api/forward-policy", empty, "admin"},
		{"server_list_forward_grants", "List explicit device-to-device forwarding grants.", http.MethodGet, "/api/forward-grants", objectSchema(map[string]any{"sourceDevice": stringSchema()}, nil), "admin"},
		{"server_create_forward_grant", "Authorize a restricted source device to reach a target and port range.", http.MethodPost, "/api/forward-grants", forwardGrant, "admin"},
		{"server_delete_forward_grant", "Delete one explicit forwarding grant.", http.MethodDelete, "/api/forward-grants", forwardGrant, "admin"},
		{"server_list_users", "List console accounts.", http.MethodGet, "/api/users", empty, "admin"},
		{"server_create_user", "Create an admin or operator account with device grants and optional expiry.", http.MethodPost, "/api/users", userCreate, "admin"},
		{"server_update_user", "Update role, password, expiry, or device grants for an account.", http.MethodPatch, "/api/users/{username}", userPatch, "admin"},
		{"server_delete_user", "Delete a console account.", http.MethodDelete, "/api/users/{username}", user, "admin"},
		{"server_list_device_keys", "List node device credentials without plaintext tokens.", http.MethodGet, "/api/device-keys", empty, "admin"},
		{"server_create_device_key", "Create a one-time device key and token.", http.MethodPost, "/api/device-keys", itemName, "admin"},
		{"server_revoke_device_key", "Revoke a device key.", http.MethodDelete, "/api/device-keys/{deviceId}", key, "admin"},
		{"server_rotate_device_key", "Rotate a device key and return the new token once.", http.MethodPost, "/api/device-keys/{deviceId}/rotate", key, "admin"},
		{"server_list_invites", "List unused one-time enrollment invites.", http.MethodGet, "/api/invites", empty, "admin"},
		{"server_create_invite", "Create a one-time device enrollment invite.", http.MethodPost, "/api/invites", itemName, "admin"},
		{"server_delete_invite", "Revoke an enrollment invite.", http.MethodDelete, "/api/invites/{code}", invite, "admin"},
		{"server_list_node_updates", "List deployable Node releases with version, size and SHA256.", http.MethodGet, "/api/node-updates", empty, "admin"},
		{"server_start_node_update", "Ask an online Node to download, verify and install its current platform release.", http.MethodPost, "/api/devices/{deviceId}/update", deviceID, "admin"},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func integerSchema(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}

func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func arraySchema(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
