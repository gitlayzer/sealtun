<p align="center">
  <img src="./assets/sealtun-logo.svg.png" alt="Sealtun logo" width="220">
</p>

# Sealtun

[中文版本](./README.md)

Sealtun is a local tunnel CLI for **Sealos Cloud** and **Kubernetes** users. It can quickly publish local Web apps, remote HTTP upstreams, SSH, databases, or debugging services to the internet.

> For installation, login, tunnel creation, access controls, custom domains, operations, and declarative config, see [QuickStart_EN.md](./QuickStart_EN.md).

## Features

- 🔑 **Password-less OAuth2 Login**: Connect easily with `sealtun login` using the Device Authorization Grant flow.
- 🌍 **Region Switching**: List built-in Sealos Cloud regions and switch regions by re-running login with `sealtun region use`.
- 👤 **Named Profiles**: Save different Sealos accounts, regions, workspaces, and kubeconfigs as named profiles and switch between them.
- 🚀 **One-Command Expose**: Run `sealtun expose 8080` or `sealtun expose --target http://10.0.0.12:8080` to get a trusted HTTPS URL.
- 🌐 **Custom Domain Automation**: Use `domain plan/add/verify/status/doctor` to generate CNAME guidance, wait for DNS, attach domains, and inspect certificate readiness.
- 🔗 **Temporary Share Links and Rotation**: Use `share create/list/revoke/rotate` to generate, revoke, or rotate expiring links for HTTPS tunnels.
- 🛡️ **Security Operations**: HTTPS tunnels support Basic Auth, Bearer tokens, temporary links, IP rules, rate limits, access audit, and server secret rotation.
- 📊 **Status and Diagnostics**: Use `doctor <tunnel-id>`, `inspect --remote/--metrics/--resources`, `logs`, and `list --check` to diagnose local ports, daemon state, remote Pods, Services, Ingresses, and certificates.
- 🧭 **Guided UX and Safe Fixes**: `up` provides daily guided creation with local port discovery and protocol templates; `doctor --fix --dry-run` previews conservative repairs before applying them.
- 👀 **Live Watching**: `list --watch` and `inspect --watch` continuously refresh tunnel state with `--interval` and `--count` controls.
- 🧾 **Declarative Config**: Use `apply -f sealtun.yaml` to declare tunnels (including resource requests/limits) in YAML and create or update them with stable names; use `diff` to compare local sessions against the desired configuration first.
- 🌐 **Optimized for Sealos**: Native support for Sealos Cloud domains, certificates, and Kubernetes resources.

## Pricing
Sealtun itself does not charge a separate software fee. The actual cost comes from the Sealos Cloud resources allocated to the remote tunnel Pod and the public entrypoint. The CLI can show resource occupancy hints in `inspect --resources`, but that is not billing estimation; the authoritative source is still the Sealos Cloud pricing page and your actual bill.

Based on the current Sealos Cloud pricing page, Sealtun tunnel cost is easiest to understand through these dimensions:

- `CPU`: billed per core / hour
- `Memory`: billed per GB / hour
- `Port`: billed per public port / hour
- `Network`: billed by traffic usage

Common Sealtun cost sources are:

- one remote tunnel Pod: `CPU + Memory`
- HTTP/HTTPS tunnels and TCP/SSH NodePort exposure: `Port`
- public traffic served through the tunnel: `Network`

Unit prices differ by region. Based on the current Sealos Cloud console screenshots, `Hangzhou H`, `Singapore B`, `Beijing A`, `Guangzhou G`, and `US West` all have different CPU, memory, and port prices, so the same tunnel can have different hourly cost across regions.

You can currently use this hourly price table as a direct reference:

| Region | CPU (core/hour) | Memory (GB/hour) | Port (port/hour) | Network |
| --- | ---: | ---: | ---: | ---: |
| `Hangzhou H` | `0.027671` | `0.013956` | `0.013900` | `0.000781 /M` |
| `Singapore B` | `0.067000` | `0.033792` | `0.013900` | `0.000781 /M` |
| `Beijing A` | `0.017125` | `0.008637` | `0.007000` | `0.000781 /M` |
| `Guangzhou G` | `0.017420` | `0.008786` | `0.007000` | `0.000781 /M` |
| `US West` | `0.020833` | `0.012500` | `0.006944` | `0.000107 /M` |

For a minimal HTTPS tunnel, you should usually expect at least:

- one remote Pod worth of `CPU + Memory`
- one public entry port
- the actual public traffic consumed

Use this rough formula when estimating:

```text
total cost ~= Pod(CPU + Memory) + public port + network traffic
```

To keep cost lower, prioritize:

- stopping unused tunnels, or using `sealtun stop` to scale replicas to 0
- lowering Pod requests / limits in `sealtun.yaml` `resources` and re-running `apply`
- avoiding oversized resources for short-lived debugging tunnels
- opening low-traffic tunnels on demand instead of keeping them always on
