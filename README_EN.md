<p align="center">
  <img src="./assets/sealtun-logo.svg.png" alt="Sealtun logo" width="220">
</p>

# Sealtun

[中文版本](./README.md)

Sealtun is a local tunnel CLI for **Sealos Cloud** and **Kubernetes** users. It can quickly publish local Web apps, remote HTTP upstreams, SSH, databases, or debugging services to the internet, and on Linux it can also let local tools directly access in-cluster Services and Pods.

> For installation, login, tunnel creation, access controls, custom domains, TUI, cluster access, operations, and declarative config, see [QuickStart_EN.md](./QuickStart_EN.md).

> Stability: core tunnels, declarative configuration, security, and lifecycle commands follow compatibility requirements. Entrypoints marked `[Alpha]` may change interface or be removed in a future release. Current Alpha entrypoints are `init`, `tui/console`, `connect`, `mesh`, `ssh connect`, and the standalone `discover`, `template`, `export`, `events`, `metrics`, `resources`, and `watch` commands; no existing capability was removed.

## Features

- 🔑 **Password-less OAuth2 Login**: Connect easily with `sealtun login` using the Device Authorization Grant flow.
- 🌍 **Region Switching**: List built-in Sealos Cloud regions and switch regions by re-running login with `sealtun region use`.
- 👤 **Named Profiles**: Save different Sealos accounts, regions, workspaces, and kubeconfigs as named profiles and switch between them.
- 🚀 **One-Command Expose**: Run `sealtun expose 8080` or `sealtun expose --target http://10.0.0.12:8080` to get a trusted HTTPS URL.
- 🌐 **Custom Domain Automation**: Use `domain plan/add/verify/status/doctor` to generate CNAME guidance, wait for DNS, attach domains, and inspect certificate readiness.
- 🔗 **Temporary Share Links and Rotation**: Use `share create/list/revoke/rotate` to generate, revoke, or rotate expiring links for HTTPS tunnels.
- 🛡️ **Security Operations**: HTTPS tunnels support Basic Auth, Bearer tokens, temporary links, IP rules, rate limits, access audit, and server secret rotation.
- 📊 **Status and Diagnostics**: Use stable `doctor <tunnel-id>`, `inspect --remote`, and `logs`, plus Alpha `events`, `metrics`, `resources`, and `watch`, to diagnose local ports, daemon state, remote Pods, Services, Ingresses, and certificates.
- 🧭 **Guided UX and Safe Fixes**: Use stable `up` for daily guided creation; Alpha `init` generates first-run command/YAML recommendations; use Alpha `resources` and `watch`, or stable `doctor --fix --dry-run`, to understand and conservatively repair tunnel state.
- 🖥️ **Terminal Console (Alpha)**: Use `tui` / `console` in an interactive terminal to discover ports, create tunnels, and manage logs, events, resources, domains, policies, share links, and lifecycle actions.
- 🔌 **Cluster Service Access (Alpha)**: On Linux, `sudo sealtun connect` lets TCP clients directly reach Service FQDNs, Service ClusterIPs, and Pod IPs without SOCKS or client-side proxy config.
- 🕸️ **Cross-Region Mesh (Alpha)**: Use `mesh` to import a Kubernetes Service from one Sealos region into other regions as local ClusterIP Services for service-level HTTP/TCP communication.
- 🧩 **Protocol Templates (Alpha)**: Use `template https|ssh|tcp|mysql|postgres|redis|mongodb|mqtt` to generate commands and `sealtun.yaml` examples.
- 🧾 **Declarative Config**: Use stable `apply -f sealtun.yaml` to declare tunnels in YAML and create or update them with stable names; use Alpha `export` to turn local sessions back into YAML.
- 🌐 **Optimized for Sealos**: Native support for Sealos Cloud domains, certificates, and Kubernetes resources.

## Pricing
Sealtun itself does not charge a separate software fee. The actual cost comes from the Sealos Cloud resources allocated to the remote tunnel Pod and the public entrypoint. The CLI can show resource occupancy hints in `resources`, but that is not billing estimation; the authoritative source is still the Sealos Cloud pricing page and your actual bill.

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
- lowering Pod requests / limits with `sealtun resources set` or `unset`
- avoiding oversized resources for short-lived debugging tunnels
- opening low-traffic tunnels on demand instead of keeping them always on
