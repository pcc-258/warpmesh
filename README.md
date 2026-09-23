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

## Documentation

Architecture, protocol and deployment details: [docs/architecture.md](docs/architecture.md)

## License

[Apache 2.0](LICENSE)
