# Sealtun

[中文版本](./README.md)

Sealtun is a powerful, elegant CLI tool that provides a `cloudflared` tunnel-like experience entirely built on **Sealos Cloud** and **Kubernetes**. 

It connects your local development machine straight to the internet by dynamically provisioning Kubernetes resources (Deployments, Services, Ingresses) and tunneling the traffic securely via bidirectional multiplexed WebSocket streams (`yamux`).

## Features

- 🔑 **Password-less OAuth2 Login**: Connect easily with `sealtun login` using the Device Authorization Grant flow.
- 🌍 **Region Switching**: List built-in Sealos Cloud regions and switch regions by re-running login with `sealtun region use`.
- 🚀 **One-Command Expose**: Execute `sealtun expose 8080`, and get a fully trusted HTTPS URL for your localhost securely routed.
- 🌐 **Custom Domains**: Use `--domain` to print the required CNAME target while creating a tunnel, then attach the domain only after DNS ownership is verified.
- 🌐 **Optimized for Sealos**: Native support for Sealos Cloud domains, HTTPS traffic, and WebSocket tunnels.
- 🐳 **All-in-One Binary**: The client and the server agent live comfortably in the exact same compact binary and Docker image.
- ☸️ **Cloud-Native by Design**: Resources on Sealos are natively managed using standard Kubernetes API constructs.

## Installation / Setup

Download the `sealtun` binary for your platform from GitHub Releases. Remote tunnel Pods use the matching `ghcr.io/gitlayzer/sealtun` container image.

For local development, build from source:

```bash
git clone https://github.com/gitlayzer/sealtun.git
cd sealtun
make build
./sealtun --version
```

`make build` injects the current Git short hash into the local binary version by default, which makes it easy to verify that the local binary matches the pushed commit. Tagged releases are built by GitHub Actions using the tag version for GitHub Release assets and container images.

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

### 3. Use a custom domain
Create the tunnel first and print the Sealos-managed CNAME target:
```bash
sealtun expose 3000 --domain app.example.com

# If you will configure DNS while the command waits, verify CNAME, attach it, and wait for the certificate
sealtun expose 3000 --domain app.example.com --wait-domain
```

Or attach one to an existing tunnel after DNS is ready:
```bash
sealtun domain set <tunnel-id> app.example.com
```

Sealtun keeps a Sealos-managed host as the tunnel control endpoint and CNAME target. It writes the custom host to Ingress and creates cert-manager `Issuer` and `Certificate` resources only after the CNAME points to that Sealos host. Configure DNS at your provider:
```text
CNAME app.example.com -> <sealos-host>
```

Verify DNS, Ingress, and certificate readiness:
```bash
sealtun domain verify <tunnel-id>

# Keep waiting until DNS and certificate are ready or the timeout expires
sealtun domain verify <tunnel-id> --wait --timeout 5m
```

Remove the custom domain:
```bash
sealtun domain clear <tunnel-id>
```

## Architecture Details

- **Protocol**: Yamux over Websocket.
- **Sealos Resources**: When you trigger `sealtun expose`, it creates `sealtun-*` variants of `Deployment`, `Service`, and `Ingress` in the active cluster context.
- **Images**: Relies on a single Docker image built natively targeting `ghcr.io/gitlayzer/sealtun`.

## Hardening Notes

- `expose` now validates port and protocol inputs before provisioning remote resources.
- `--protocol` currently supports only `https`. TCP, UDP, and gRPC are intentionally out of scope until there is a dedicated transport design for them.
- Ingress host generation prefers the `SEALOS_DOMAIN` returned by Sealos Launchpad instead of guessing from the region host.
- Custom domains must pass CNAME ownership verification before Sealtun writes the custom host to Ingress, preventing unverified host preemption on shared Ingress controllers.
- After attachment, custom domains keep both hosts on the Ingress: the daemon uses the Sealos host for the control tunnel, while user traffic can use the CNAME-backed custom domain.
- `--wait-domain` waits for DNS CNAME, Ingress attachment, and cert-manager certificate readiness only when `--domain` is also provided; timeout does not delete the tunnel, and you can retry with `sealtun domain set` or recheck with `sealtun domain verify`.
- `list` reads local session records by default; use `list --check` to probe local target ports and report degraded sessions.
- `inspect` shows local session state by default; use `inspect --remote` to include best-effort Kubernetes diagnostics.
- `doctor` summarizes daemon, login, session, local port, and remote Deployment, Service, Ingress, Pod, and Event diagnostics.
- Tunnel pod readiness now has a default `90s` timeout, configurable via `--ready-timeout`.
- Configuration is stored in `~/.sealtun`; first run only migrates legacy auth and kubeconfig files from `~/.sealos`, not old tunnel session records.

## License

MIT License.
