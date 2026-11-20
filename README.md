# WarpMesh

Self-hosted control plane for devices behind NAT. One console, every machine, anywhere.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml/badge.svg)](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/pcc-258/warpmesh)](go.mod)

## Terminology

- **Server** is the Linux control plane: web console, API, authorization and relay.
- **Client** is the optional Windows desktop shell at `warpmesh-client.exe`.
- **Node** is the managed-device service at `warpmesh-node`.

The Client does not connect directly to peers. It opens the local Node console
and the Server console. TCP links are owned by Node; fleet access is authorized
by Server.

## Features

- **Web console** - dashboard, searchable device inventory, status/system/group filters, device details, batch grouping, audit log and account management
- **Guided enrollment** - platform-specific node downloads, one-time invites, commands using the current server address, and live online confirmation
- **Service readiness** - nodes report terminal/file support and local VNC reachability, with OS-specific desktop setup guidance
- **Account editor** - searchable device selection, role/password/expiry forms, clear expiry action and protection against demoting the last administrator
- **Remote terminal** - PTY / ConPTY shell from any browser
- **Remote desktop** - VNC-backed screen control, noVNC in the browser
- **File manager** - browse, drag-drop upload, batch download, rename, delete
- **Device mesh** - WebRTC direct paths with automatic server relay fallback
- **Secure by default** - per-device keys, invite codes, role-based access
- **HTTPS closed loop** - certmagic issues and renews certificates automatically

## Quickstart

```bash
make build

# Server
DEVICE_RELAY_ADMIN_TOKEN="$(openssl rand -hex 24)" \
DEVICE_RELAY_ADMIN_PASSWORD='<strong-password>' ./bin/warpmesh-server \
  -domain warpmesh.ddns.net -data-dir ./data

# Node
./bin/warpmesh-node \
  -server https://warpmesh.ddns.net \
  -token <device-key> -device-id <device-id> -name my-machine
```

Nodes discover the control-plane port from the server automatically. By default,
the console and nodes share one listener. Set `-alt-https-listen :18443` to
separate the node plane when HTTPS is enabled; open that port on the server too.

## Add devices from the console

Open **Devices → Add device**, choose the device platform, download the node,
and run the generated commands in its download folder. Keep `warpmesh-data`:
it stores the enrolled identity. The wizard commands run in the foreground;
configure your operating system's service manager for unattended startup.

The server reads downloadable builds from `-node-dir` (environment variable
`WARPMESH_NODE_DIR`; `DEVICE_RELAY_NODE_DIR` is also accepted). The default is
`/opt/warpmesh/nodes`. Build filenames are
`warpmesh-node-{linux,darwin}-{amd64,arm64}` and
`warpmesh-node-windows-amd64.exe`. Downloads require a console login. For a
local build, use `-node-dir ./bin`.

New nodes report service readiness at connection time and every 15 seconds.
Older nodes remain usable but display unknown readiness. VNC reachability is
not proof of screen capture permission or an available desktop session. The
console shows setup guidance when VNC is unavailable; Windows needs TightVNC,
macOS needs Screen Sharing, and Linux needs an X11 VNC session or the desktop
container. Wayland and login screens require separate platform validation.

## Windows client

Windows packages include `warpmesh-client.exe`, a WebView2 desktop window for
the local node UI. It provides:

- node connection health and runtime details
- TCP link creation, endpoint copying, active-session counts and peer mappings
- terminal, desktop and authorization entry points into the control plane
- access-policy and link-grant management through the server console
- local diagnostics and a copyable JSON snapshot

The client waits for the node service at `http://127.0.0.1:9876` and falls back
to the default browser if WebView2 is unavailable. Cross-build it with:

```bash
make client-windows
```

## Node management

Each node ships a small local web UI:

```text
http://127.0.0.1:9876
```

It manages links, shows their peers and provides direct entry points for
terminal, desktop and authorization workflows.

## AI automation

The local Node UI is a client of a versioned automation API, not the only
control surface. Actions available in the UI are also exposed as typed tools:

- status, links, peers, diagnostics and console target discovery
- create, update and delete TCP links
- terminal, desktop, files and access-policy deep links

The API lives under `http://127.0.0.1:9876/api/v1`. Mutating and read-only
operations use a Bearer token stored in the node data directory:

```bash
warpmesh-node ai-guide
warpmesh-node automation -data-dir ./data
warpmesh-node api-token -data-dir ./data
```

For AI clients, the node also exposes the same tool surface through MCP over
stdio:

```bash
warpmesh-node mcp -data-dir ./data
```

`automation` prints a ready-to-use MCP command and endpoint configuration. The
automation token is never logged. Operator access and forwarding grants remain
server-side policies; a local automation token cannot bypass them.

The AI operating contract is available in
[AI_GUIDE.md](AI_GUIDE.md), at `GET /api/v1/ai-guide`, and as the MCP
resource `warpmesh://node/guide`.

The control plane publishes its own machine-readable REST capability manifest:

```text
GET /api/automation/capabilities
Authorization: Bearer <admin-or-session-token>
```

It lists device, analytics, audit, account, device-key, invite and forwarding
grant operations with method, path, JSON Schema and required access level.
The server guide is at `GET /api/automation/ai-guide`.

## Node updates

Server can ask an online Node to install the release already published under
`-node-dir`. It sends a fixed authenticated download path, version and SHA256;
it never sends arbitrary commands or arbitrary download URLs.

```text
GET  /api/node-updates
POST /api/devices/{deviceId}/update
GET  /api/node-update/download?device=...&platform=...
```

The Node downloads to `data/updates`, verifies SHA256 and runs the staged
binary's `version` command before installation. On Windows and other
unsupervised deployments it reports `staged` and waits for the service manager
to restart it. Unix deployments configured with `-update-service` perform an
atomic replacement, restart the service and restore the previous binary if the
new service does not become active.

Configure the Server with `WARPMESH_NODE_UPDATE_SERVICE` or `-update-service`
and run Nodes with the same service name if discovery is unavailable.

```bash
# Open a terminal into another device
warpmesh-node terminal -server https://warpmesh.ddns.net -token <admin-token> -device <id>

# Open the desktop of another device
warpmesh-node desktop -server https://warpmesh.ddns.net -device <id>

# Enroll a new device with a one-time invite
warpmesh-node enroll -server https://warpmesh.ddns.net -code <invite> -name <device>
```

## Containers

```bash
podman build --build-arg NODE_BINARY=bin/warpmesh-node-linux-arm64 -t warpmesh-node .
podman build -f Containerfile.desktop --build-arg NODE_BINARY=bin/warpmesh-node-linux-arm64 -t warpmesh-desktop .
```

## Logs

The server and node binaries log to stderr, so `journalctl -u warpmesh.service -f` and
`podman logs -f warpmesh-desktop` are enough to follow them.

The relay prints one line per notable event, as `key=value` pairs:

```text
[relay] node online: dev-0e3ed6d8548fed8b (linux@arm64)
[relay] auth.login.failed user=admin ip=203.0.113.7 reason=bad-credentials
[relay] session.start kind=terminal session=c88c0e8e actor=admin device=dev-0e3ed6d8 ip=203.0.113.7
[relay] session.end kind=terminal session=c88c0e8e actor=admin device=dev-0e3ed6d8 dur=987ms reason=browser-disconnected
[relay] http.request ip=203.0.113.7 method=POST path=/api/login status=401 bytes=32 dur=77ms
[relay] filemanager.rejected session=710884ed actor=admin device=dev-0e3ed6d8 type=forward:connect
```

Notes:

- Request lines cover mutations, and any authentication or authorization
  outcome. Successful reads (which the console polls) are not logged.
  Credentials in query strings are redacted before they reach the log.
- TLS handshakes for hostnames this server does not serve are dropped: an
  internet-facing IP is probed constantly and those lines bury real events.
  A handshake failure for your own domain is always reported.
- `DEVICE_RELAY_DEBUG_RTC=1` enables the per-candidate WebRTC/ICE diagnostics
  on both the server and the node. They are useful when a direct path fails to
  establish and very noisy otherwise.

## Documentation

Architecture, protocol and deployment details: [docs/architecture.md](docs/architecture.md)

Server, Client and Node tutorials: [docs/server-client-node.md](docs/server-client-node.md)

AI operating contract: [AI_GUIDE.md](AI_GUIDE.md)

Windows verification strategy: [docs/windows-client-testing.md](docs/windows-client-testing.md)

## Cross-platform CI acceptance

GitHub Actions runs native builds, full Go tests and race checks on Linux AMD64,
Windows AMD64, macOS Intel and macOS Apple Silicon. The integration smoke test
launches the actual server and node binaries, enrolls a device, executes a
PTY/ConPTY shell command, round-trips a multi-chunk binary file and restarts the
node to verify identity and inventory edits are preserved. Test reports and
native binaries are available as workflow artifacts for seven days.

Every branch push, pull request and merge queue runs lint/type checks, runtime
dependency vulnerability checks and the native matrix. `Required checks` is the
aggregate branch-protection check: it fails if any required job fails or is
skipped. Configure it as a required status check in GitHub repository settings.
Only after all checks pass does CI package server and node binaries, plus the
Windows desktop client, for Linux
AMD64/ARM64, Windows AMD64 and macOS AMD64/ARM64, with SHA256 checksums and the
source commit. Linux ARM64 packaging is a cross-build, not a native runtime test.

The Release workflow reuses the complete CI gate. Pushing a `v*` tag creates a
draft GitHub release with tested packages; publish the draft after reviewing it.
Manual dispatch validates and packages the selected branch without creating a
release. Dependabot proposes weekly Go, npm and Actions updates, which run the
same PR checks. Workflows use read-only permissions except for release upload;
concurrent branch runs cancel obsolete runs, and test logs survive failures.

This validates native runtime behavior, not graphical desktop acceptance:
screen capture/input, macOS screen permissions, Windows UAC/login screens and
multiple monitors still require dedicated graphical test environments.

## License

[Apache 2.0](LICENSE)
