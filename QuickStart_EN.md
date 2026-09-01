# Sealtun QuickStart

[Back to README](./README_EN.md) | [中文 QuickStart](./QuickStart.md)

This guide covers installation, login, tunnel creation, access controls, custom domains, observability and operations, and declarative config. Pricing is kept in [README_EN.md](./README_EN.md#pricing).

## 📦 Installation

Install the `sealtun` CLI with npm, or download the binary for your platform from GitHub Releases. Remote tunnel Pods use the matching `ghcr.io/gitlayzer/sealtun` container image.

Override the remote Pod image with `SEALTUN_SERVER_IMAGE` for dev/staging; normal use does not need it.

Global install with npm:

```bash
npm install -g sealtun
sealtun --version
```

One-off run with npx:

```bash
npx sealtun@latest --version
npx sealtun@latest login
```

The npm package installs the matching optional platform binary automatically; macOS, Linux, and Windows on `amd64/x64` and `arm64` are supported.

On Windows with nvm-windows, or when Node/npm live in a directory that needs admin rights, a global install can fail because the npm global prefix is not writable. Prefer `npx sealtun@latest --version`, or move the npm global directory into your user directory:

```powershell
npm config set prefix "$env:APPDATA\npm"
$env:PATH += ";$env:APPDATA\npm"
npm install -g sealtun
sealtun --version
```

If it says `@gitlayzer/sealtun-win32-x64` / `@gitlayzer/sealtun-win32-arm64` cannot be found, make sure you did not pass `--omit=optional` or set `npm config set optional false`. If still blocked, download the GitHub Release binary directly with the PowerShell snippet below.

Quick install on macOS / Linux:

```bash
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

curl -L "https://github.com/gitlayzer/sealtun/releases/latest/download/sealtun_${OS}_${ARCH}.tar.gz" -o sealtun.tar.gz
tar -xzf sealtun.tar.gz sealtun
chmod +x sealtun
sudo mv sealtun /usr/local/bin/sealtun
sealtun --version
```

Quick download on Windows PowerShell:

```powershell
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
Invoke-WebRequest -Uri "https://github.com/gitlayzer/sealtun/releases/latest/download/sealtun_windows_$arch.zip" -OutFile sealtun.zip
Expand-Archive .\sealtun.zip -DestinationPath .
.\sealtun.exe --version
```

Build a local debug binary from source:

```bash
git clone https://github.com/gitlayzer/sealtun.git
cd sealtun
make build
./sealtun --version
```

## 🤖 Codex Skill

The repo ships `skills/sealtun` so Codex-style AI agents can understand and use the Sealtun CLI accurately. The skill triggers passively when users mention `sealtun`, `sealtun.yaml`, NAT traversal, exposing local ports, temporary public preview links, third-party callbacks into a local service, tunnel access control, or public SSH/TCP tunnels.

When triggered, the skill first checks that the request really is about exposing a local/dev service through Sealtun, then follows usage guidance, live operation, or troubleshooting flows. Without an explicit request it will not run state-changing commands such as `sealtun expose/apply/domain add/stop/cleanup/logout`.

Install the skill straight from the repo:

```bash
npx skills add https://github.com/gitlayzer/sealtun
```

When developing inside this repository, sync the directory into Codex's global skills directory:

```bash
mkdir -p ~/.codex/skills
cp -R skills/sealtun ~/.codex/skills/sealtun
```

## 🚀 Quick Start

All commands are Stable: `up`, `expose`, `apply/diff`, tunnel lifecycle, security policies, custom domains, account scoping, and the `doctor/logs/inspect/list` diagnostics.

Shortest path:

```bash
# 1. Log in to Sealos
sealtun login

# 2. Interactively pick a port, protocol, and optional security settings
sealtun up

# 3. Expose a local web service precisely in scripts, e.g. localhost:3000
sealtun expose 3000

# 4. Check status, diagnostics, and logs
sealtun list
sealtun doctor
sealtun logs <tunnel-id>

# 5. Stop, restart, or clean up
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup <tunnel-id>
```

### 1. Log in to Sealos
Device authentication (like `gh auth login`, no password typing):
```bash
sealtun login

# Non-interactive scripts or pinning a region
sealtun login gzg

# List supported regions
sealtun region list

# Switch region
sealtun region use hzh

# Log in and save as a named profile
sealtun login gzg --profile gzg-main

# List and switch saved profiles
sealtun profile list
sealtun profile use hzh-dev
```
Built-in regions:

| Name | Region API | Ingress domain suffix |
| --- | --- | --- |
| `gzg` | `https://gzg.sealos.run` | `sealosgzg.site` |
| `hzh` | `https://hzh.sealos.run` | `sealoshzh.site` |
| `bja` | `https://bja.sealos.run` | `sealosbja.site` |
| `cloud` | `https://cloud.sealos.io` | `cloud.sealos.io` |
| `usw` | `https://usw-1.sealos.io` | `usw-1.sealos.app` |

*Note: only built-in Sealos Cloud regions are supported. In an interactive terminal `sealtun login` first asks you to pick a region with the keyboard; use `sealtun login <region>` in scripts, CI, or when pinning a region. Login obtains Kubernetes credentials and the region's `SEALOS_DOMAIN`, stored safely under `~/.sealtun`. Named profiles are saved to `~/.sealtun/profiles/<name>`; switching a profile swaps the active `auth.json` and `kubeconfig`.*

### 2. Expose a local port
For example, make a web service on local port `3000` publicly reachable:
```bash
# Recommended for daily development: reuses the current project tunnel; full guided flow otherwise
sealtun up
sealtun up --guided
sealtun up 3000
sealtun up --target https://10.0.0.12:8443 --insecure
sealtun up 3000 --rate-limit 60/m --audit

# https protocol by default (works for plain HTTP and WebSocket app traffic)
sealtun expose 3000

# Or point the public HTTPS entry at an HTTP upstream reachable from this machine
sealtun expose --target http://10.0.0.12:8080

# Skip upstream certificate verification for private HTTPS targets with self-signed certs
sealtun expose --target https://10.0.0.12:8443 --target-insecure-skip-verify
```

`sealtun up` is the smart entry to `expose`: it reuses the tunnel recorded in the current directory's `.sealtun/state.json`; with no state in an interactive terminal it walks through login check → port pick → protocol pick → auth → rate limit → domain → save config → create, auto-discovering local listening ports with protocol hints. With an explicit port/`--target` or in non-interactive scripts, `up` calls the same expose engine directly; `--guided` forces the wizard. `--template` supports `https/ssh/tcp/mysql/postgres/redis/mongodb/mqtt`. `--target` only applies to the default HTTPS tunnel and must be an `http://` or `https://` address reachable from the machine running Sealtun; SSH/TCP L4 tunnels keep the local-port + NodePort model. `--target-insecure-skip-verify` / `up --insecure` only affects the Sealtun client's certificate check against the HTTPS upstream; keep it off unless the upstream is a private self-signed service.

Enable Basic Auth for public traffic:
```bash
# Recommended: read the password from an env var, out of shell history
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 --basic-auth-user admin --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD

# One-off inline form
sealtun expose 3000 --basic-auth admin:change-me
```

Basic Auth is enforced by the Sealtun server proxy layer, not Ingress annotations; it protects public business paths only and never intercepts the `/_sealtun/ws` control channel, health checks, or the Bearer-secret-protected metrics.

You can also enable Ingress-independent access policies:
```bash
# Bearer token
export SEALTUN_BEARER_TOKEN='share-secret'
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN

# IP allowlist / denylist, single IPs or CIDRs
sealtun expose 3000 --ip-allowlist 203.0.113.10,198.51.100.0/24 --ip-denylist 198.51.100.9

# Temporary access link, expires after 1 hour by default
export SEALTUN_TEMP_TOKEN='review-link-secret'
sealtun expose 3000 --temporary-access-token-env SEALTUN_TEMP_TOKEN --temporary-access-ttl 1h

# Rate limit and access audit
sealtun expose 3000 --rate-limit 60/m --audit
```

Bearer and temporary-link tokens need at least 8 characters, are stored as SHA-256 hashes, and never appear in Deployment args; temporary links use `?_sealtun_token=...`, and Sealtun strips that query parameter before forwarding to the local service or `--target` upstream. IP rules prefer the proxy-supplied `X-Real-IP`, falling back to the last valid client IP in `X-Forwarded-For`. When Basic Auth and Bearer/temporary links are both configured, passing any one of them grants access. `--rate-limit` uses fixed-window notation such as `60/m` or `1000/h`; the audit log records only allow/deny reasons, status codes, paths, and client IPs — never token plaintext, Authorization headers, or Basic Auth passwords.

Create, list, and revoke temporary share links for an existing HTTPS tunnel:
```bash
# Auto-generate a token, 1h expiry by default; the URL is shown exactly once
sealtun share create <tunnel-id> --name review --ttl 1h

# List link metadata without leaking token plaintext
sealtun share list <tunnel-id>

# Rotate a link: old token dies immediately, new URL shown once
sealtun share rotate <tunnel-id> review --ttl 1h

# Revoke a link by name
sealtun share revoke <tunnel-id> review
```

`share` only works for HTTPS tunnels. SSH/TCP L4 entries have no HTTP query-token auth layer, so temporary share links do not apply.

Show and update the HTTPS access policy:
```bash
sealtun policy show <tunnel-id>
sealtun policy set <tunnel-id> --rate-limit 60/m --audit
sealtun policy set <tunnel-id> --clear-rate-limit
sealtun policy set <tunnel-id> --no-audit

# Audit events from the last 10 minutes
sealtun policy audit <tunnel-id> --since 10m
sealtun policy audit <tunnel-id> --since 10m --json
```

Rotate the tunnel server secret:
```bash
sealtun rotate <tunnel-id> --server-secret
```

The new server secret is printed exactly once, written back to the local session, and the remote tunnel rolls to it. `policy`, `share`, and `rotate` only affect tunnels recorded in the local session; HTTPS access policies never apply to SSH/TCP NodePort traffic.

### 3. Public SSH access
If your Sealos region supports public TCP NodePorts, use L4 SSH mode to connect via a public host and port:

```bash
# Port 22 is the usual sshd port on macOS/Linux; any other local sshd port works too
sealtun expose 22 --protocol ssh
```

The command prints the public SSH entry:
```bash
ssh <user>@<public-host> -p <node-port>
```

Or put it in `~/.ssh/config` and just `ssh sealtun-dev`:
```sshconfig
Host sealtun-dev
  HostName <public-host>
  User <user>
  Port <node-port>
```

With `--protocol ssh` the public business entry is a TCP NodePort only; there is no default HTTPS business URL. Sealtun still keeps an internal control channel for the local daemon to reach the remote Pod, but it is not a user-facing SSH entry. Basic Auth, Bearer tokens, temporary links, IP rules, and custom domains apply to HTTPS tunnels only, not to the SSH L4 entry.

### 4. Generic public TCP access
Beyond SSH, expose local databases, debug services, or any non-HTTP protocol over generic L4 TCP:

```bash
sealtun expose 5432 --protocol tcp
```

The command prints the public TCP entry:
```bash
<public-host>:<node-port>
```

`--protocol tcp` uses public TCP NodePorts just like `--protocol ssh`, keeps only the HTTPS control channel for the local daemon, and provides no default HTTPS business URL. Basic Auth, Bearer tokens, temporary links, IP rules, and custom domains are HTTPS proxy-layer features and do not apply to the TCP L4 entry.

### 5. Custom domains
Generate the official Sealos domain and CNAME target when creating a tunnel:
```bash
sealtun expose 3000 --domain app.example.com

# If you configure DNS while the command waits, wait for CNAME validation, attach, and certificate readiness
sealtun expose 3000 --domain app.example.com --wait-domain
```

Or attach to an existing tunnel after DNS is live:
```bash
# First check the DNS you need
sealtun domain plan <tunnel-id> app.example.com

# Attach once DNS resolves
sealtun domain add <tunnel-id> app.example.com

# Or wait for DNS, attach, and keep waiting for the certificate
sealtun domain add <tunnel-id> app.example.com --wait --timeout 5m
```

Sealtun keeps a Sealos-managed subdomain as the tunnel control plane and CNAME target. Only after your CNAME points at that Sealos host will Sealtun write the custom domain into the Ingress and create the cert-manager `Issuer` and `Certificate`. Configure this at your DNS provider:
```text
CNAME app.example.com -> <sealos-host>
```

Verify CNAME, Ingress, and certificate state:
```bash
sealtun domain verify <tunnel-id>

# Keep waiting until DNS and certificate are ready or the timeout hits
sealtun domain verify <tunnel-id> --wait --timeout 5m

# Summary of all custom domains
sealtun domain status

# Detailed diagnostics for one tunnel
sealtun domain status <tunnel-id> --verbose
```

Remove a custom domain:
```bash
sealtun domain clear <tunnel-id>
```

### 6. Observability and operations
View remote tunnel Pod logs:
```bash
sealtun logs <tunnel-id>
sealtun logs <tunnel-id> --tail 200
sealtun logs <tunnel-id> --follow
```

Inspect one tunnel with optional remote diagnostics, metrics, or the resource inventory:
```bash
# Local session detail
sealtun inspect <tunnel-id>

# Add remote Kubernetes diagnostics and recent events
sealtun inspect <tunnel-id> --remote

# Add local, Kubernetes, and server metrics
sealtun inspect <tunnel-id> --metrics

# Add the Kubernetes resource inventory and occupancy hints
sealtun inspect <tunnel-id> --resources
```

`inspect --metrics` aggregates local session state, remote Deployment/Pod/Ingress state, and — when the remote Pod supports it — the Bearer-secret-protected `/_sealtun/metrics` request counters. TCP/SSH tunnels additionally expose TCP connection counts, active connections, byte counts, and errors. Unavailable remote diagnostics or older images without server metrics degrade to warnings instead of failing the command.

One-shot diagnostics for local and remote state:
```bash
# Global health check
sealtun doctor

# Per-tunnel diagnostics: local port, daemon, remote resources, next steps
sealtun doctor <tunnel-id>
sealtun doctor <tunnel-id> --json
sealtun doctor <tunnel-id> --report
sealtun doctor <tunnel-id> --report --report-file ./doctor.md

# Show conservative auto-fix actions without executing
sealtun doctor --fix --dry-run

# Apply low-risk fixes: restart stopped tunnels, clean expired/stale tunnels, start the daemon
sealtun doctor --fix
```

`doctor --fix` performs conservative actions only: restarting stopped tunnels, cleaning expired/stale tunnels, or starting the local daemon. It never runs `cleanup --all`, never deletes active tunnels, and never touches DNS providers. `doctor <id> --report` produces a redacted Markdown troubleshooting report safe to share — no tokens, secrets, Authorization headers, Basic Auth passwords, or kubeconfigs.

Watch tunnels or the global state live:
```bash
# Continuously refresh the tunnel list
sealtun list --watch

# Continuously inspect one tunnel
sealtun inspect <tunnel-id> --watch

# Control cadence and iterations; --count 0 keeps running
sealtun list --watch --interval 5s --count 10
sealtun inspect <tunnel-id> --watch --interval 2s --count 0
```

Stop, restart, and clean up tunnels:
```bash
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup
sealtun cleanup <tunnel-id>
sealtun cleanup --all
```

`stop` preserves the domain, Service, Ingress, NodePort Service, and local session, scaling the remote Pod to zero; `start` reopens the same tunnel. Default `cleanup` removes only stopped, expired, stale, or error tunnels; `cleanup <tunnel-id>` cleans one eligible tunnel; `cleanup --all` force-deletes remote resources for every local record — use it only when you really want all tunnels gone.

### 7. Declarative configuration
Create `sealtun.yaml`:
```yaml
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
      audit:
        enabled: true
      ipAllowlist:
        - 203.0.113.10
        - 198.51.100.0/24
      ipDenylist:
        - 198.51.100.9
      temporaryLinks:
        - name: review
          tokenEnv: SEALTUN_TEMP_TOKEN
          ttl: 1h
    waitDomain: false
    readyTimeout: 90s
    domainTimeout: 5m
```

Remote HTTP upstreams can use `target` directly, without a local listening port:
```yaml
version: v1
tunnels:
  - name: upstream-api
    target: http://10.0.0.12:8080
    protocol: https
```

For private HTTPS upstreams with self-signed certificates:
```yaml
version: v1
tunnels:
  - name: upstream-api
    target: https://10.0.0.12:8443
    protocol: https
    targetTls:
      insecureSkipVerify: true
    resources:
      requests:
        cpu: 20m
        memory: 64Mi
      limits:
        cpu: 300m
        memory: 256Mi
```

Apply the configuration:
```bash
# Offline validation and preview, no login required
sealtun apply -f sealtun.yaml --dry-run

# Compare local sessions with the declared configuration
sealtun apply -f sealtun.yaml --dry-run --format diff

# Create or update tunnels
sealtun apply -f sealtun.yaml
```

Resource sizing is declared exclusively through the YAML `resources` field. Remote Pods default to `requests: cpu=10m memory=32Mi` and `limits: cpu=200m memory=128Mi`; `apply` updates the Deployment template to the desired values, with omitted fields falling back to the defaults.

You can also use the expanded plaintext form:
```yaml
basicAuth:
  username: admin
  password: change-me
```

Or read the password from an environment variable:
```yaml
basicAuth:
  username: admin
  passwordEnv: SEALTUN_BASIC_AUTH_PASSWORD
```

`name` doubles as the stable tunnel ID, so re-running `apply` updates the same `sealtun-<name>` resources. `tunnels` declares multiple tunnels at once; `target` only supports HTTPS tunnels and must be an `http://` or `https://` URL — if `localPort` is also set, the ports must match. `targetTls.insecureSkipVerify` applies to `https://` targets for private self-signed upstreams. `resources` declares remote Pod CPU/memory requests and limits, with defaults for omitted fields. `ttl` is written to the local session's `expiresAt`; once expired, the local daemon removes the remote resources and local record. Custom domains still follow the CNAME-then-attach rule: for new tunnels with unverified CNAMEs, `apply` keeps the official Sealos domain and prints the follow-up `domain add` command; existing tunnels reject unverified domain changes to avoid clobbering live domain configuration.

## 📄 License

MIT License.
