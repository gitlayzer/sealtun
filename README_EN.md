# Sealtun

[中文版本](./README.md)

Sealtun is a powerful, elegant CLI tool that provides a `cloudflared` tunnel-like experience entirely built on **Sealos Cloud** and **Kubernetes**. 

It connects your local development machine straight to the internet by dynamically provisioning Kubernetes resources (Deployments, Services, Ingresses) and tunneling the traffic securely via bidirectional multiplexed WebSocket streams (`yamux`).

## Features

- 🔑 **Password-less OAuth2 Login**: Connect easily with `sealtun login` using the Device Authorization Grant flow.
- 🚀 **One-Command Expose**: Execute `sealtun expose 8080`, and get a fully trusted HTTPS URL for your localhost securely routed.
- 🌐 **Optimized for Higress & Sealos**: Native support for Sealos Cloud domain suffixes (`.app`) and Higress gateway protocols (WS/GRPC).
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
```
*Note: This automatically retrieves your Kubernetes API configuration and tokens securely storing them within `~/.sealtun`.*

### 2. Expose a local port
For instance, to make your local Web Server running on Port `3000` accessible to everyone on the Internet:
```bash
# Default https protocol (compatible with WebSocket)
sealtun expose 3000

# Expose a gRPC service
sealtun expose 50051 --protocol grpcs
```

Sealtun will:
1. Spin up a tunnel proxy Pod in your Sealos namespace.
2. Establish the Ingress routes.
3. Automatically connect via WebSockets and proxy all L7 connections back to `localhost:3000`.

## Architecture Details

- **Protocol**: Yamux over Websocket.
- **Sealos Resources**: When you trigger `sealtun expose`, it creates `sealtun-*` variants of `Deployment`, `Service`, and `Ingress` in the active cluster context.
- **Images**: Relies on a single Docker image built natively targeting `ghcr.io/gitlayzer/sealtun`.

## License

MIT License.
