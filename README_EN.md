<p align="center">
  <img src="./assets/sealtun-logo.svg.png" alt="Sealtun logo" width="220">
</p>

# Sealtun

[中文](./README.md) · [QuickStart](./QuickStart_EN.md) · [Changelog](./CHANGELOG.md)

A local tunnel CLI for **Sealos Cloud** and **Kubernetes** users. One command publishes local web apps, remote HTTP upstreams, SSH, databases, or debugging services to the internet.

```bash
sealtun login
sealtun expose 3000
# => https://sealtun-xxxx-ns-xxxx.sealosgzg.site
```

## Features

- **HTTPS tunnels**: Ingress + reverse proxy with Basic Auth, Bearer tokens, temporary share links, IP rules, rate limits, audit, and custom domains; `--qr` prints a terminal QR code for instant mobile access; `routes`/`--route` multiplexes several local services into one tunnel by path prefix (prefix is stripped)
- **SSH/TCP tunnels**: `expose 22 --protocol ssh`, `expose 5432 --protocol tcp` via direct NodePort
- **Smart entry**: `up` discovers local ports, guides configuration, and reuses project tunnels
- **Declarative management**: `apply -f sealtun.yaml` creates/updates tunnels idempotently, with dry-run and diff previews
- **Diagnostics**: `inspect --remote/--metrics/--resources`, `list --check/--watch`, `doctor --fix`, `logs`
- **Accounts**: OAuth device-flow login, multiple regions, named profiles

## Quick Start

```bash
npm install -g sealtun
sealtun login          # complete device authorization in the browser
sealtun up             # create your first tunnel interactively
```

For detailed installation, access controls, custom domains, SSH/TCP, and declarative configuration, see [QuickStart_EN.md](./QuickStart_EN.md).

## Pricing

Sealtun itself is free. Costs come from the Sealos Cloud resources allocated to your tunnel: pod CPU + memory, public ports, and network traffic, billed hourly. Per-region prices are on the Sealos Cloud console pricing page.

To lower costs: `stop` unused tunnels to scale to zero, lower requests/limits via YAML `resources`, and open short-lived debug tunnels on demand.

## License

MIT License.
