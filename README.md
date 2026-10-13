# WarpMesh

Self-hosted control plane for devices behind NAT. One console, every machine, anywhere.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml/badge.svg)](https://github.com/pcc-258/warpmesh/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/pcc-258/warpmesh)](go.mod)

## Features

- **Web console** - dashboard, searchable device inventory, status/system/group filters, device details, batch grouping, audit log and account management
- **Guided enrollment** - platform-specific agent downloads, one-time invites, commands using the current server address, and live online confirmation
- **Service readiness** - agents report terminal/file support and local VNC reachability, with OS-specific desktop setup guidance
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

# Agent
./bin/warpmesh-agent \
  -server https://warpmesh.ddns.net \
  -token <device-key> -device-id <device-id> -name my-machine
```

Agents discover the control-plane port from the server automatically. By default,
the console and agents share one listener. Set `-alt-https-listen :18443` to
separate the agent plane when HTTPS is enabled; open that port on the server too.

## Add devices from the console

Open **Devices → Add device**, choose the device platform, download the agent,
and run the generated commands in its download folder. Keep `warpmesh-data`:
it stores the enrolled identity. The wizard commands run in the foreground;
configure your operating system's service manager for unattended startup.

The server reads downloadable builds from `-agent-dir` (environment variable
`DEVICE_RELAY_AGENT_DIR`, default `/opt/warpmesh/agents`). Build filenames are
`warpmesh-agent-{linux,darwin}-{amd64,arm64}` and
`warpmesh-agent-windows-amd64.exe`. Downloads require a console login. For a
local build, use `-agent-dir ./bin`.

New agents report service readiness at connection time and every 15 seconds.
Older agents remain usable but display unknown readiness. VNC reachability is
not proof of screen capture permission or an available desktop session. The
console shows setup guidance when VNC is unavailable; Windows needs TightVNC,
macOS needs Screen Sharing, and Linux needs an X11 VNC session or the desktop
container. Wayland and login screens require separate platform validation.

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

## Cross-platform CI acceptance

GitHub Actions runs native builds, full Go tests and race checks on Linux AMD64,
Windows AMD64, macOS Intel and macOS Apple Silicon. The integration smoke test
launches the actual server and agent binaries, enrolls a device, executes a
PTY/ConPTY shell command, round-trips a multi-chunk binary file and restarts the
agent to verify identity and inventory edits are preserved. Test reports and
native binaries are available as workflow artifacts for seven days.

Every branch push, pull request and merge queue runs lint/type checks, runtime
dependency vulnerability checks and the native matrix. `Required checks` is the
aggregate branch-protection check: it fails if any required job fails or is
skipped. Configure it as a required status check in GitHub repository settings.
Only after all checks pass does CI package server and agent binaries for Linux
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
