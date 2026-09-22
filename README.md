# Device Relay

Device Relay is a self-hosted control plane for devices behind NAT. Agents
connect outbound to one relay server, the server keeps a device catalog, and a
web console provides remote terminals, file transfer and device management.
The server and agent are single Go binaries; the web UI is a React single-page
app embedded into the server binary.

The server persists its device catalog in SQLite (`data/devices.db`) using the
pure-Go `modernc.org/sqlite` driver, so the release binary has no CGO
dependency.

The console includes a dashboard with online/offline stats, OS breakdown,
device groups, a per-device key panel and an audit log. Agents can use their
own device key instead of the shared device token, and keys are shown once at
creation. Agent file downloads are restricted to an allowed path root (home by
default) instead of allowing arbitrary paths.

The design intentionally borrows ideas from mature open-source projects:
MeshCentral-style web remote management, RustDesk-style lightweight outbound
agents, and Tailscale-style "device identity plus relay" thinking. The code is
original and Apache-2.0 licensed.

## Architecture

```text
Browser ──HTTPS/WSS──> Relay Server <──WSS── Agent (home PC, NAS, Linux box...)
```

See [docs/architecture.md](docs/architecture.md) for the full design,
including Mermaid diagrams for deployment, registration, terminal sessions,
file transfer and the SQLite data model.

- Agents never need a public IP or inbound firewall rules.
- The server authenticates browsers with an admin token and agents with a
  shared device token.
- Terminal sessions are PTY-backed on the agent and streamed through the
  server as JSON over WebSocket.
- Files can be uploaded to the agent's `uploads/` directory or downloaded from
  an arbitrary path readable by the agent process.

## Platform support

- Server: Linux (amd64/arm64). The SQLite driver is pure Go, so the server
  builds with `CGO_ENABLED=0` and has no native dependencies.
- Agent: Windows (ConPTY), macOS and Linux (PTY).

Build all release binaries with:

```bash
make cross
```

Outputs:

```text
bin/devicerelay-server-linux-amd64
bin/devicerelay-agent-linux-amd64
bin/devicerelay-agent-windows-amd64.exe
bin/devicerelay-agent-darwin-arm64
```

Windows terminals are backed by Windows ConPTY, so interactive `cmd.exe` and
PowerShell sessions work the same as Unix PTY sessions.

## Quickstart

```bash
make build

# Terminal 1: server
ADMIN_TOKEN=admin DEVICE_TOKEN=device ./bin/devicerelay-server -listen :8080

# Terminal 2: agent on a device
./bin/devicerelay-agent -server ws://127.0.0.1:8080/ws/agent \
  -token device -name home-pc

# Open http://127.0.0.1:8080 and sign in with the admin token.
```

The console itself requires an account login. The initial account is created
from `DEVICE_RELAY_ADMIN_USER` / `DEVICE_RELAY_ADMIN_PASSWORD` (defaults:
`admin` / `admin`). Change both before exposing the server publicly. Failed
logins are rate limited per account and IP, and sessions can be ended with the
Log out button.

For a public server, put a TLS terminator in front (Caddy, Nginx) or pass
`-tls-cert` and `-tls-key` to the server, then agents use `wss://`.

## HTTPS

Three options, from easiest to most manual:

1. Automatic Let's Encrypt. Point a domain at the server, open TCP 80 and 443,
   then run:

   ```bash
   ./bin/devicerelay-server \
     -domain relay.example.com \
     -acme-email you@example.com \
     -data-dir ./data
   ```

   The server obtains and renews certificates automatically, the web UI is
   `https://relay.example.com`, and agents connect with
   `wss://relay.example.com/ws/agent`.

2. Bring your own certificate:

   ```bash
   ./bin/devicerelay-server \
     -tls-cert fullchain.pem -tls-key privkey.pem \
     -https-listen :443 -http-listen :80
   ```

3. Reverse proxy (Caddy or Nginx) in front of the server on `:8080`. The server
   itself stays plain HTTP on localhost and the proxy terminates TLS.

Use `DEVICE_RELAY_DOMAIN` / `DEVICE_RELAY_ACME_EMAIL` environment variables for
the same automatic mode.

## Protocol

All control messages are JSON over WebSocket:

- `hello` registers an agent with device metadata.
- `terminal:start`, `terminal:input`, `terminal:output`, `terminal:resize`,
  `terminal:stop`, `terminal:exit` carry PTY sessions.
- `file:upload:start`, `file:chunk`, `file:upload:done`, `file:download`,
  `file:done`, `file:error` carry file transfer.

Terminal output bytes are base64 encoded; browser input is UTF-8 JSON text.

## Roadmap

- Per-device tokens and user accounts.
- Device-to-device TCP port forwarding through the relay.
- Remote desktop via an agent-side screen streaming endpoint.
- Device groups, power actions and agent updates.
- Persistent session audit log.

## License

Apache-2.0. This project is inspired by the feature sets of MeshCentral
(Apache-2.0), RustDesk (AGPL-3.0), and Tailscale/Headscale (BSD-3-Clause), but
contains no copied source code from those projects.
