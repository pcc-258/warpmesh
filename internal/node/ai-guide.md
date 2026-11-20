# WarpMesh AI Guide

WarpMesh gives an AI client a typed control surface for a self-hosted device
mesh. It does not replace server authorization. The AI client must distinguish the
local Node API from the control-plane API.

## What You Can Control

The local Node service runs on the same machine as `warpmesh-node`. Its API is
loopback-only and manages that Node:

- inspect connection, platform, uptime and service readiness
- list, create, update and delete loopback TCP links
- inspect peer devices referenced by those links
- read diagnostics
- obtain terminal, desktop, file-manager and access-policy URLs

The server control plane manages the fleet:

- devices, groups and forwarding policy
- operator accounts and per-device grants
- device keys and one-time enrollment invites
- explicit device-to-device forwarding grants
- analytics, connection history and audit entries

The local Node token is not a server operator credential. It cannot grant a
user access, bypass a forwarding grant, or open a terminal or desktop session.

## Discover Before Acting

Read the local capability manifest before selecting a tool:

```text
GET http://127.0.0.1:9876/api/v1/capabilities
```

The response contains the API version, authentication mode and a JSON Schema
for every local tool. Prefer these runtime definitions over hard-coded
assumptions.

The full guide is also available as:

```text
GET http://127.0.0.1:9876/api/v1/ai-guide
```

## Authentication

Create or inspect the local automation configuration:

```bash
warpmesh-node automation -data-dir ./data
```

The Node creates `ui-token` in its data directory on first start. Send it as:

```text
Authorization: Bearer <ui-token>
```

Do not log, include in URLs, commit, or transmit this token. Use `api-token`
only when a client cannot read the token file directly.

On PowerShell:

```powershell
$Token = (Get-Content .\data\ui-token -Raw).Trim()
Invoke-RestMethod `
  -Uri http://127.0.0.1:9876/api/v1/status `
  -Headers @{ Authorization = "Bearer $Token" }
```

On a POSIX shell:

```bash
token=$(tr -d '\r\n' < ./data/ui-token)
curl -fsS \
  -H "Authorization: Bearer $token" \
  http://127.0.0.1:9876/api/v1/status
```

## MCP

Register the Node as a stdio MCP server:

```json
{
  "mcpServers": {
    "warpmesh-node": {
      "command": "C:\\Program Files\\WarpMesh\\warpmesh-node.exe",
      "args": [
        "mcp",
        "-data-dir",
        "C:\\ProgramData\\WarpMesh\\data"
      ]
    }
  }
}
```

Use the actual Node executable and data directory from the deployment. The MCP
client first calls `initialize`, then `tools/list`, then `tools/call`. Read
`warpmesh://node/guide` before issuing mutation tools.

Available MCP tools:

| Tool | Access | Purpose |
| --- | --- | --- |
| `node_get_status` | read | Connection, runtime and service readiness |
| `node_list_links` | read | Link endpoints, peers and traffic counters |
| `node_create_link` | create | Bind a loopback port to a peer TCP port |
| `node_update_link` | update | Change the peer or target port of a link |
| `node_delete_link` | delete | Stop and remove a link |
| `node_list_peers` | read | Group links by peer device |
| `node_get_diagnostics` | read | Runtime and automation diagnostics |
| `node_get_console_targets` | read | Terminal, desktop, files and access URLs |

Available MCP resources:

| Resource | Purpose |
| --- | --- |
| `warpmesh://node/guide` | This operating guide |
| `warpmesh://node/status` | Current status snapshot |
| `warpmesh://node/links` | Current links |
| `warpmesh://node/peers` | Current peer grouping |
| `warpmesh://node/diagnostics` | Current diagnostics |

## HTTP Operations

All paths below are relative to `http://127.0.0.1:9876`. Operations other than
capabilities and the guide require the Bearer token.

| Method | Path | Meaning |
| --- | --- | --- |
| `GET` | `/api/v1/capabilities` | Tool schemas and authentication metadata |
| `GET` | `/api/v1/ai-guide` | This document |
| `GET` | `/api/v1/status` | Node status and console URLs |
| `GET` | `/api/v1/links` | List links |
| `POST` | `/api/v1/links` | Create a link |
| `GET` | `/api/v1/links/{localPort}` | Read one link |
| `PUT` | `/api/v1/links/{localPort}` | Update the link target |
| `DELETE` | `/api/v1/links/{localPort}` | Delete the link |
| `GET` | `/api/v1/peers` | List linked peers |
| `GET` | `/api/v1/diagnostics` | Read diagnostics |
| `GET` | `/api/v1/console-targets` | Get console deep links |

Create a link:

```json
{
  "localPort": 2222,
  "targetDeviceId": "dev-example",
  "targetPort": 22
}
```

All ports are integers from 1 to 65535. A local port can have only one link.
Treat `409 Conflict` as a bind or ownership conflict, `404 Not Found` as a
missing link, and `401 Unauthorized` as an authentication problem.

## Server Operations

The control plane publishes a REST capability manifest:

```text
GET https://your-warpmesh-host/api/automation/capabilities
Authorization: Bearer <admin-or-session-token>
```

The manifest describes device, analytics, audit, account, device-key, invite
and forwarding-grant operations. Server authorization still applies. An
operator token cannot invoke admin-only tools.

Node releases are visible to administrators through `server_list_node_updates`.
Use `server_start_node_update` only for an online device and only after checking
its current `version`, `updateState`, and the target release SHA256. The Node
must verify the download, and `staged` means installation is waiting for the
service manager; it does not mean the update succeeded.

For an interactive remote terminal from a trusted automation host:

```bash
warpmesh-node terminal \
  -server https://your-warpmesh-host \
  -token <operator-or-admin-token> \
  -device dev-example
```

Desktop and file access are browser workflows. Use
`node_get_console_targets` to obtain their URLs, then open them with a browser
session that has the required server login and device grant.

## Operating Rules

1. Read status and existing links before creating or changing a link.
2. Do not delete a link unless the user explicitly requested that endpoint be
   removed or the operation is part of a clearly stated rollback.
3. Do not assume that a reachable VNC port means desktop capture permission is
   valid.
4. Do not infer authorization from URL visibility. The server enforces account
   and device grants.
5. Prefer stable tool names and JSON Schemas from capabilities over scraping the
   HTML UI.
6. Report structured errors and preserve the distinction between local
   automation failure and server authorization failure.
