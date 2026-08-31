# Sealtun CLI Reference

Use this for interactive Sealtun operation: install, shell completion, login, `up`, expose HTTPS, remote HTTP upstream targets, SSH, or generic TCP, secure public HTTP traffic, observe, bind domains, stop/start, and clean up tunnels.

## Quick Recipes

Use these paths before listing every available flag:

| Request | Commands | Notes |
| --- | --- | --- |
| "I want my local app on the internet" / "让本地项目跑在公网" | `sealtun status`; interactive/dev: `sealtun up`; scripted/exact: `sealtun expose <port>` | `up` reuses current project state or discovers/asks for a port. Defaults to HTTPS and daemon mode. |
| "Expose this remote HTTP address" / "把远端地址端口转公网" | dev: `sealtun up --target http://host:port`; scripts: `sealtun expose --target http://host:port` | HTTPS-only. The target must be reachable from the machine running Sealtun. |
| "Help me get started" / "第一次怎么用" | `sealtun status`; `sealtun up` | `up` guides through port, protocol, and optional security settings. |
| "Give my local app a public domain" / "给本地服务一个公网域名" | `sealtun expose <port> --domain <domain>` or `sealtun domain plan <id> <domain>` | If the tunnel already exists, plan first, then add only when mutation is requested. |
| "Expose SSH publicly" / "公网 SSH" | `sealtun expose 22 --protocol ssh` | Return `ssh <user>@<public-host> -p <node-port>`. Do not add HTTPS auth/domain features. |
| "Expose Postgres/MySQL/Redis/MongoDB/MQTT" | `sealtun expose 5432 --protocol tcp` | Common database ports map to generic TCP. Return `<host>:<node-port>`. |
| "Secure this public URL" | HTTPS `expose`, `policy set`, or `share` with Basic Auth, Bearer token, IP rules, rate limit, audit, or temporary links | Prefer env-backed secrets. HTTP access controls do not protect SSH/TCP NodePort. |

After any live operation, verify using the matching command: `list --check`, `inspect <id>`, `domain status/verify`, `share list`, or `doctor <id>`.

## Install

```bash
npm install -g sealtun
sealtun --version

npx sealtun@latest --version
npx sealtun@latest login
```

Direct binaries are published on GitHub Releases. The npm package installs a platform-specific optional binary package for macOS, Linux, or Windows on x64/amd64 and arm64.

## Shell Completion

```bash
sealtun completion bash
sealtun completion zsh
sealtun completion fish
sealtun completion powershell
```

Use the generated script according to the user's shell. If the user only asks whether completion exists, show the matching command instead of editing shell startup files.

## Login, Regions, Profiles

```bash
sealtun login
sealtun login gzg
sealtun status
sealtun region list
sealtun region current
sealtun region use hzh

sealtun login gzg --profile gzg-main
sealtun profile list
sealtun profile current
sealtun profile save hzh-dev
sealtun profile use hzh-dev
sealtun profile delete hzh-dev
```

Known regions include `gzg`, `hzh`, `bja`, `cloud`, and `usw`. In an interactive terminal, bare `sealtun login` shows a keyboard selector for these regions; in scripts, CI, or when the user wants a specific region, use `sealtun login <region>` such as `sealtun login gzg`. Login state, kubeconfig, and profiles live under `~/.sealtun`.

First-use behavior:

- Before creating cloud resources, check `sealtun status` when feasible.
- If the user is not logged in, explain that interactive `sealtun login` first asks for a region, then opens a Sealos authorization flow and stores the resulting auth/kubeconfig under `~/.sealtun`.
- If a browser/device authorization flow opens, wait for the user to finish it. Do not retry repeatedly while the user is authorizing.
- After login, verify with `sealtun status`, `sealtun region current`, and `sealtun profile current` when profiles are involved.
- For multiple accounts, regions, or workspaces, prefer `sealtun login <region> --profile <name>` and `sealtun profile use <name>` instead of overwriting the active login without explanation.

## Expose A Port

```bash
sealtun up
sealtun up --guided
sealtun up 3000
sealtun up --template postgres
sealtun up --target http://10.0.0.12:8080
sealtun up --target https://10.0.0.12:8443 --insecure
sealtun up 3000 --rate-limit 60/m --audit
sealtun expose 3000
sealtun expose --target http://10.0.0.12:8080
sealtun expose --target https://10.0.0.12:8443 --target-insecure-skip-verify
sealtun expose 3000 --foreground
sealtun expose 3000 --ready-timeout 2m
```

`up` is the convenience entrypoint: it reuses `.sealtun/state.json` for the current project; without state in an interactive terminal, it guides through login check, port selection (with local port discovery), protocol choice (with protocol templates for `https/ssh/tcp/mysql/postgres/redis/mongodb/mqtt`), optional Basic Auth, optional rate limit/audit, optional custom domain, optional `sealtun.yaml` save, and final creation. Use `up --guided` when the user explicitly wants the wizard. Use `up 3000` or `up --target http://host:port` for direct creation, and `expose` for exact scripted creation. `expose` defaults to `https` and daemon mode. The daemon maintains the local side in the background. Use `--foreground` when the current terminal should own the tunnel lifecycle.

Use `https` when the user wants a browser URL, webhook callback URL, OAuth callback, payment callback, public preview link, Basic Auth, Bearer tokens, temporary access links, IP allowlist/denylist, or custom domain.

Use `--target` when the public HTTPS URL should forward to an existing HTTP/HTTPS upstream address instead of `localhost:<port>`. The target must be reachable from the machine running the Sealtun CLI. For private HTTPS upstreams with self-signed or name-mismatched certificates, use `--target-insecure-skip-verify` only when the user explicitly accepts skipping upstream TLS verification. `--target` is rejected for `--protocol ssh` and `--protocol tcp`; those remain direct L4 local-port tunnels.

## Public Access Controls

Access controls are enforced by the Sealtun server proxy layer, independent of Ingress annotations. They apply to public business traffic, including `--target` upstream traffic, not `/_sealtun/ws`, health checks, or internal metrics protected by the tunnel secret.

Prefer environment variables for credentials:

```bash
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 \
  --basic-auth-user admin \
  --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD

export SEALTUN_BEARER_TOKEN='share-secret'
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN

sealtun expose 3000 \
  --ip-allowlist 203.0.113.10,198.51.100.0/24 \
  --ip-denylist 198.51.100.9

export SEALTUN_TEMP_TOKEN='review-link-secret'
sealtun expose 3000 \
  --temporary-access-token-env SEALTUN_TEMP_TOKEN \
  --temporary-access-ttl 1h

sealtun expose 3000 --rate-limit 60/m --audit
```

One-shot forms exist, but warn that they can enter shell history:

```bash
sealtun expose 3000 --basic-auth admin:change-me
sealtun expose 3000 --bearer-token share-secret
sealtun expose 3000 --temporary-access-token review-link-secret --temporary-access-ttl 1h
```

## Custom Domains

```bash
sealtun expose 3000 --domain app.example.com
sealtun expose 3000 --domain app.example.com --wait-domain --domain-timeout 5m

sealtun domain plan <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com --wait --timeout 5m
sealtun domain verify <tunnel-id>
sealtun domain verify <tunnel-id> --wait --timeout 5m
sealtun domain status
sealtun domain doctor <tunnel-id>
sealtun domain clear <tunnel-id>
```

Sealtun keeps a generated Sealos host as the control-plane host and CNAME target. The user must configure:

```text
CNAME app.example.com -> <sealos-host>
```

Only after CNAME ownership verification does Sealtun write the custom host to Ingress and manage cert-manager resources.

Prefer `domain plan` when the user only needs DNS guidance. Use `domain add` when DNS is ready, or `domain add --wait` when the user explicitly wants Sealtun to wait for DNS, attach the domain, and wait for certificate readiness.

## SSH Over Sealtun

For regions that support public TCP NodePort, use direct L4 SSH:

```bash
sealtun expose 22 --protocol ssh
ssh <user>@<public-host> -p <node-port>
```

`--protocol ssh` exposes only a public TCP NodePort for user traffic. HTTPS is kept only as the internal control channel used by the local daemon, not as a default application URL. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains are HTTP-layer features and are rejected for SSH tunnels.

Use SSH mode only when the user wants to expose a local SSH server or direct TCP SSH entry. It prints `Public SSH host`, `Public SSH port`, and an `ssh <user>@<public-host> -p <node-port>` command. Do not promise a custom domain for SSH; users connect with the generated host plus NodePort.

## Generic TCP Over Sealtun

For non-HTTP protocols such as databases, queues, or debugging services, use generic L4 TCP:

```bash
sealtun expose 5432 --protocol tcp
```

The command prints `Public TCP host`, `Public TCP port`, and `Public TCP endpoint` as `<public-host>:<node-port>`. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains are HTTPS proxy-layer features and are rejected for TCP tunnels.

## Observe And Manage

Operations are `status`, `list`, `inspect`, `logs`, and `doctor`. Diagnostics that used to live in standalone commands are flags on `inspect`; continuous refresh is a flag on `list` and `inspect`.

```bash
sealtun status
sealtun status --json

sealtun list
sealtun list --check
sealtun list --json

sealtun list --watch
sealtun list --watch --interval 5s --count 20

sealtun inspect <tunnel-id>
sealtun inspect <tunnel-id> --remote
sealtun inspect <tunnel-id> --metrics
sealtun inspect <tunnel-id> --resources
sealtun inspect <tunnel-id> --json
sealtun inspect <tunnel-id> --watch --interval 5s --count 20

sealtun logs <tunnel-id>
sealtun logs <tunnel-id> --tail 200 --follow
sealtun logs <tunnel-id> --since 10m

sealtun doctor
sealtun doctor <tunnel-id>
sealtun doctor --json
sealtun doctor <tunnel-id> --json
sealtun doctor <tunnel-id> --report
sealtun doctor <tunnel-id> --report --report-file ./doctor.md
sealtun doctor --fix --dry-run
sealtun doctor --fix
sealtun doctor <tunnel-id> --fix --dry-run
sealtun doctor <tunnel-id> --fix
```

`inspect --remote` includes best-effort remote Kubernetes diagnostics and recent events. `inspect --metrics` adds local process state, Kubernetes readiness, Pod counts/restarts, and — when the remote image supports it — server request counters; older images degrade to a warning instead of failing. `inspect --resources` shows the Kubernetes resource inventory for one tunnel (Deployment replicas, Pod count, Service type, NodePort, Ingress hosts, Certificate, Issuer, Secret metadata) with occupancy hints; Secret data is never displayed and it is not a cloud billing estimate. Resource sizing changes go through YAML `resources` in `sealtun.yaml` and `apply`.

`list --watch` and `inspect --watch` refresh state until interrupted or `--count` samples are printed. Use `--json` for newline-delimited samples when another tool consumes state changes.

`doctor --fix --dry-run` prints conservative automatic fixes without executing them. `doctor --fix` may start stopped tunnels, clean expired/stale sessions, or start the local daemon. It must not run `cleanup --all`, logout, DNS provider changes, or cleanup active tunnels.

Use `doctor <tunnel-id>` for "why can't I connect" issues. It checks the local session, owner process or daemon, local target port, remote resources where credentials are available, and prints next-step suggestions. Use `doctor --fix --dry-run` before any automatic fix. Use `doctor <id> --report` to create a redacted Markdown report for support or issue triage; it should not include tokens, secrets, Authorization headers, Basic Auth passwords, or kubeconfig data.

## Share Links

```bash
sealtun share create <tunnel-id> --name review --ttl 1h
sealtun share create <tunnel-id> --name qa --ttl 2h --open
sealtun share create <tunnel-id> --name qa --ttl 2h --json
sealtun share list <tunnel-id>
sealtun share list <tunnel-id> --json
sealtun share rotate <tunnel-id> review --ttl 1h
sealtun share rotate <tunnel-id> review --ttl 1h --json
sealtun share rotate <tunnel-id> review --ttl 1h --open
sealtun share revoke <tunnel-id> review
```

Temporary share links only apply to HTTPS tunnels. `share create` updates the tunnel access policy and prints a URL with `?_sealtun_token=...`; the URL is shown only once because Sealtun stores only a token hash. `share rotate` replaces an existing named token, invalidates the old URL, and prints the new URL once. `share list` shows names and expiry metadata without tokens. `share revoke` removes the token by name.

## Rotation

```bash
sealtun rotate <tunnel-id> --server-secret
sealtun rotate <tunnel-id> --server-secret --json
```

Use this for tunnel server secret rotation. The new secret is shown once and saved locally. It updates the remote Deployment and does not change the SSH/TCP access model; HTTPS access policies still apply only to public HTTPS traffic.

## Stop And Clean Up

```bash
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup
sealtun cleanup <tunnel-id>
sealtun cleanup --all
sealtun logout
sealtun logout --force
```

`stop` scales the remote tunnel Deployment to zero and keeps the domain, Service, Ingress, secrets, NodePort Service for SSH, and local session record. Use `start` to scale the same tunnel back up and reconnect it through the local daemon.

`cleanup` deletes stopped, expired, stale, or error tunnels and removes their local session records. `cleanup <tunnel-id>` targets one eligible tunnel. `cleanup --all` force deletes every locally tracked tunnel, including active ones, and should be used only when you intentionally want to remove all tracked remote resources.

`logout` first tries to clean up locally tracked tunnel resources before deleting credentials. Use `--force` only when cleanup cannot complete and local credentials must be removed anyway.
