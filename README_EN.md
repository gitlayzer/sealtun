# Sealtun

[中文版本](./README.md)

Sealtun is a powerful, elegant CLI tool that provides a `cloudflared` tunnel-like experience entirely built on **Sealos Cloud** and **Kubernetes**. 

It connects your local development machine straight to the internet by dynamically provisioning Kubernetes resources (Deployments, Services, Ingresses) and tunneling the traffic securely via bidirectional multiplexed WebSocket streams (`yamux`).

## Features

- 🔑 **Password-less OAuth2 Login**: Connect easily with `sealtun login` using the Device Authorization Grant flow.
- 🌍 **Region Switching**: List built-in Sealos Cloud regions and switch regions by re-running login with `sealtun region use`.
- 🚀 **One-Command Expose**: Execute `sealtun expose 8080`, and get a fully trusted HTTPS URL for your localhost securely routed.
- 🌐 **Optimized for Sealos**: Native support for Sealos Cloud domains, HTTPS traffic, and WebSocket tunnels.
- 🐳 **All-in-One Binary**: The client and the server agent live comfortably in the exact same compact binary and Docker image.
- ☸️ **Cloud-Native by Design**: Resources on Sealos are natively managed using standard Kubernetes API constructs.

## Installation / Setup

You can build the Sealtun CLI from source:

```bash
git clone https://github.com/gitlayzer/sealtun.git
cd sealtun
go build -o sealtun main.go
```

## Quick Start

### 1. Login to Sealos
Perform the device authentication (which operates smoothly without passwords similar to `gh auth login`):
```bash
sealtun login

# List supported regions
sealtun region list

# Switch to another region
sealtun region use hzh
```
*Note: Only built-in Sealos Cloud regions are currently supported. Login retrieves your Kubernetes credentials and the region's `SEALOS_DOMAIN`, then stores them under `~/.sealtun`.*

### 2. Expose a local port
For instance, to make your local Web Server running on Port `3000` accessible to everyone on the Internet:
```bash
# Default https protocol (compatible with WebSocket)
sealtun expose 3000

```

Sealtun will:
1. Spin up a tunnel proxy Pod in your Sealos namespace.
2. Establish the Ingress routes.
3. Automatically connect via WebSockets and proxy all L7 connections back to `localhost:3000`.

## Architecture Details

- **Protocol**: Yamux over Websocket.
- **Sealos Resources**: When you trigger `sealtun expose`, it creates `sealtun-*` variants of `Deployment`, `Service`, and `Ingress` in the active cluster context.
- **Images**: Relies on a single Docker image built natively targeting `ghcr.io/gitlayzer/sealtun`.

## Hardening Notes

- `expose` now validates port and protocol inputs before provisioning remote resources.
- `--protocol` currently supports only `https`. TCP, UDP, and gRPC are intentionally out of scope until there is a dedicated transport design for them.
- Ingress host generation prefers the `SEALOS_DOMAIN` returned by Sealos Launchpad instead of guessing from the region host.
- `list` reads local session records by default; use `list --check` to probe local target ports and report degraded sessions.
- `inspect` shows local session state by default; use `inspect --remote` to include best-effort Kubernetes diagnostics.
- `doctor` summarizes daemon, login, session, local port, and remote Deployment, Service, Ingress, Pod, and Event diagnostics.
- Tunnel pod readiness now has a default `90s` timeout, configurable via `--ready-timeout`.
- Configuration is stored in `~/.sealtun`; first run only migrates legacy auth and kubeconfig files from `~/.sealos`, not old tunnel session records.

## License

MIT License.
