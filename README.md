# Device Relay

Device Relay is a self-hosted control plane for devices behind NAT. Agents
connect outbound to one relay server, the server keeps a device catalog, and a
web console provides remote terminals, file transfer and device management.
The server and agent are single Go binaries; the web UI is a React single-page
app embedded into the server binary.

The design intentionally borrows ideas from mature open-source projects:
MeshCentral-style web remote management, RustDesk-style lightweight outbound
agents, and Tailscale-style "device identity plus relay" thinking. The code is
original and Apache-2.0 licensed.

## Architecture

```text
Browser ──HTTPS/WSS──> Relay Server <──WSS── Agent (home PC, NAS, Linux box...)
```

- Agents never need a public IP or inbound firewall rules.
- The server authenticates browsers with an admin token and agents with a
  shared device token.
- Terminal sessions are PTY-backed on the agent and streamed through the
  server as JSON over WebSocket.
- Files can be uploaded to the agent's `uploads/` directory or downloaded from
  an arbitrary path readable by the agent process.

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

For a public server, put a TLS terminator in front (Caddy, Nginx) or pass
`-tls-cert` and `-tls-key` to the server, then agents use `wss://`.

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
