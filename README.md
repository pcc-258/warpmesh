# WarpMesh

Self-hosted control plane for devices behind NAT. One console, every machine, anywhere.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml/badge.svg)](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/pcc-258/warpmesh)](go.mod)

## Features

- **Web console** - dashboard, device inventory, audit log, account management
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
DEVICE_RELAY_ADMIN_PASSWORD=secret ./bin/warpmesh-server \
  -domain warpmesh.ddns.net -data-dir ./data

# Agent
./bin/warpmesh-agent \
  -server https://warpmesh.ddns.net \
  -token <device-key> -device-id <device-id> -name my-machine
```

Agents discover the control-plane port from the server automatically.

## Client

Each agent ships a small local web UI:

```text
http://127.0.0.1:9876
```

It manages port forwards and links to the console.

```bash
# Open a terminal into another device
warpmesh-agent terminal -server https://warpmesh.ddns.net -token <admin-token> -device <id>

# Open the desktop of another device
warpmesh-agent desktop -server https://warpmesh.ddns.net -device <id>

# Enroll a new device with a one-time invite
warpmesh-agent enroll -server https://warpmesh.ddns.net -code <invite> -name <device>
```

## Containers

```bash
podman build --build-arg AGENT_BINARY=bin/warpmesh-agent-linux-arm64 -t warpmesh-agent .
podman build -f Containerfile.desktop --build-arg AGENT_BINARY=bin/warpmesh-agent-linux-arm64 -t warpmesh-desktop .
```

## Logs

Both binaries log to stderr, so `journalctl -u warpmesh.service -f` and
`podman logs -f warpmesh-desktop` are enough to follow them.

The relay prints one line per notable event, as `key=value` pairs:

```text
[relay] agent online: dev-0e3ed6d8548fed8b (linux@arm64)
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
  on both the server and the agent. They are useful when a direct path fails to
  establish and very noisy otherwise.

## Documentation

Architecture, protocol and deployment details: [docs/architecture.md](docs/architecture.md)

## License

[Apache 2.0](LICENSE)
