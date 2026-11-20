package node

const AutomationAPIVersion = "v1"

// AutomationTool describes one stable, machine-callable operation. The same
// definition powers API discovery and MCP tool listings.
type AutomationTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Method       string         `json:"method,omitempty"`
	Path         string         `json:"path,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations"`
}

// AutomationTools is the complete control surface exposed by the Windows
// client's local node service. UI-only presentation actions are represented by
// stable URLs rather than stateful browser automation.
func AutomationTools() []AutomationTool {
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true}
	create := map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false}
	update := map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}
	remove := map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true}
	empty := map[string]any{"type": "object", "additionalProperties": false}
	linkInput := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"localPort":      map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"targetDeviceId": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"targetPort":     map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
		},
		"required":             []string{"localPort", "targetDeviceId", "targetPort"},
		"additionalProperties": false,
	}
	updateInput := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"localPort":      map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"targetDeviceId": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"targetPort":     map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
		},
		"required":             []string{"localPort", "targetDeviceId", "targetPort"},
		"additionalProperties": false,
	}
	portInput := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"localPort": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
		},
		"required":             []string{"localPort"},
		"additionalProperties": false,
	}

	return []AutomationTool{
		{
			Name:        "node_get_status",
			Description: "Read node connection, runtime, service readiness, and console routing status.",
			Method:      "GET",
			Path:        "/api/v1/status",
			InputSchema: empty,
			Annotations: readOnly,
		},
		{
			Name:        "node_list_links",
			Description: "List local TCP links, their remote peers, active sessions, and usage counters.",
			Method:      "GET",
			Path:        "/api/v1/links",
			InputSchema: empty,
			Annotations: readOnly,
		},
		{
			Name:        "node_create_link",
			Description: "Create a loopback TCP link from this node to a target device and port.",
			Method:      "POST",
			Path:        "/api/v1/links",
			InputSchema: linkInput,
			Annotations: create,
		},
		{
			Name:        "node_update_link",
			Description: "Change the target device and target port of an existing local link.",
			Method:      "PUT",
			Path:        "/api/v1/links/{localPort}",
			InputSchema: updateInput,
			Annotations: update,
		},
		{
			Name:        "node_delete_link",
			Description: "Stop and remove one local TCP link.",
			Method:      "DELETE",
			Path:        "/api/v1/links/{localPort}",
			InputSchema: portInput,
			Annotations: remove,
		},
		{
			Name:        "node_list_peers",
			Description: "Group current links by remote device, including mapped local and target ports.",
			Method:      "GET",
			Path:        "/api/v1/peers",
			InputSchema: empty,
			Annotations: readOnly,
		},
		{
			Name:        "node_get_diagnostics",
			Description: "Read runtime diagnostics for connection, platform, paths, VNC, and automation.",
			Method:      "GET",
			Path:        "/api/v1/diagnostics",
			InputSchema: empty,
			Annotations: readOnly,
		},
		{
			Name:        "node_get_console_targets",
			Description: "Get deep links for this node's terminal, desktop, files, access policy, and control-plane pages.",
			Method:      "GET",
			Path:        "/api/v1/console-targets",
			InputSchema: empty,
			Annotations: readOnly,
		},
	}
}
