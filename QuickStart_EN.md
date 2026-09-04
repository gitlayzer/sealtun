# Sealtun QuickStart

[README](./README_EN.md) · [中文](./QuickStart.md) · [Changelog](./CHANGELOG.md)

## Install

```bash
npm install -g sealtun
```

Alternatives: one-off run via npx (`npx sealtun@latest login`), direct binaries from GitHub Releases, or `make build` from source.

## Three Steps

```bash
sealtun login      # 1. authorize in the browser
sealtun up         # 2. create a tunnel interactively (auto-discovers local ports)
sealtun list       # 3. view tunnels
```

The public URL is printed at creation time.

## Login and Accounts

```bash
sealtun login gzg                      # log in with a specific region
sealtun login gzg --profile gzg-main   # save as a named profile
sealtun profile list / use / delete    # manage profiles
sealtun region list / current          # query regions (switch with login <region>)
sealtun status                         # current login state
```

Built-in regions: `gzg` (Guangzhou), `hzh` (Hangzhou), `bja` (Beijing), `cloud` (Global), `usw` (US West). Credentials live in `~/.sealtun`; profiles in `~/.sealtun/profiles/<name>`.

## Creating Tunnels

**HTTPS (default)**: publish a local port or a remote HTTP upstream as a public URL.

```bash
sealtun expose 3000                              # local port
sealtun expose --target http://10.0.0.12:8080    # remote HTTP upstream
sealtun expose 3000 --qr                         # terminal QR of the public URL; scan with a phone to open
sealtun up                                        # interactive guide (recommended for daily use)
```

**SSH**: `expose 22 --protocol ssh`, prints `ssh <user>@<public-host> -p <node-port>`.

**Generic TCP**: `expose 5432 --protocol tcp` (databases, queues, MQTT, etc.), prints `<public-host>:<node-port>`.

SSH/TCP use direct NodePort forwarding; HTTPS authentication, domains, and policies do not apply.

`up` is the smart entry to `expose`: it reuses the project's `.sealtun/state.json`, auto-discovers local ports, supports `--template` (https/ssh/tcp/mysql/postgres/redis/mongodb/mqtt), and `--guided` to force the wizard. `--target` is HTTPS-only; use `--target-insecure-skip-verify` for private self-signed upstreams.

## Access Controls (HTTPS only)

```bash
# Basic Auth (env-backed recommended)
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 --basic-auth-user admin --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD

# Bearer token
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN

# IP allow/deny lists
sealtun expose 3000 --ip-allowlist 203.0.113.10 --ip-denylist 198.51.100.9

# Rate limit and audit
sealtun expose 3000 --rate-limit 60/m --audit
```

Authentication is enforced by the Sealtun server proxy layer and protects public business traffic only, not the control channel. Tokens are stored as SHA-256 hashes.

**Temporary share links** (URL shown once at creation):

```bash
sealtun share create <tunnel-id> --name review --ttl 1h
sealtun share rotate / revoke <tunnel-id> review
```

**Policy management**:

```bash
sealtun policy show / set <tunnel-id> --rate-limit 60/m --audit
sealtun policy audit <tunnel-id> --since 10m
```

**Rotate server secret**: `sealtun rotate <tunnel-id> --server-secret` (new secret shown once).

## Custom Domains (HTTPS only)

```bash
sealtun domain plan <tunnel-id> app.example.com   # see the DNS you need
# At your DNS provider: CNAME app.example.com -> <sealos-host>
sealtun domain add <tunnel-id> app.example.com --wait   # attach and wait for the certificate
sealtun domain verify / status / clear <tunnel-id>
```

The custom host is written to Ingress and a cert-manager certificate is created only after CNAME verification. `domain status --verbose` prints detailed DNS/Ingress/certificate diagnostics.

## Operations

```bash
sealtun list / --check / --watch                 # list, local port probing, live refresh
sealtun inspect <id>                              # single tunnel detail
sealtun inspect <id> --remote                    # + remote K8s diagnostics and events
sealtun inspect <id> --metrics                   # + metrics (older images degrade to a warning)
sealtun inspect <id> --resources                 # + resource inventory (Secrets redacted)
sealtun inspect <id> --watch                     # continuous inspection
sealtun logs <id> --tail 200 --follow            # remote pod logs
sealtun doctor [--fix --dry-run] [--fix]          # diagnostics and conservative fixes
sealtun stop / start / cleanup <id>               # pause (resumable) / resume / delete
```

## Declarative Configuration

```yaml
# sealtun.yaml
version: v1
tunnels:
  - name: web
    localPort: 3000
    protocol: https
    domain: app.example.com
    ttl: 2h
    basicAuth:
      credential: admin:change-me
    accessPolicy:
      bearerTokenEnv: SEALTUN_BEARER_TOKEN
      rateLimit: 60/m
      audit: { enabled: true }
    resources:
      requests: { cpu: 20m, memory: 64Mi }
      limits:   { cpu: 300m, memory: 256Mi }
```

```bash
sealtun apply -f sealtun.yaml --dry-run                 # execution plan preview (no login needed)
sealtun apply -f sealtun.yaml --dry-run --format diff   # field-level diff against local sessions
sealtun apply -f sealtun.yaml                            # create/update
```

`name` doubles as the stable tunnel ID, so re-running apply updates the same resources. Resource sizing goes exclusively through YAML `resources` + apply; defaults are requests `10m/32Mi`, limits `200m/128Mi`.

## Appendix

**Codex Skill**: the repo ships `skills/sealtun` so Codex-style AI agents can use the Sealtun CLI accurately: `npx skills add https://github.com/gitlayzer/sealtun`.

**Environment variables**: `SEALTUN_SERVER_IMAGE` overrides the remote pod image (dev use); `SEALTUN_HOME` overrides the `~/.sealtun` config directory.

**Troubleshooting**: see `skills/sealtun/references/troubleshooting.md`.

## License

MIT License.
