# Sealtun CLI Reference

Use this for interactive Sealtun operation: install, shell completion, guided init, login, expose HTTPS, remote HTTP upstream targets, SSH, or generic TCP, access cluster-internal Services/Pods with connect, build service-level cross-region Mesh, secure public HTTP traffic, observe, bind domains, stop/start, and clean up tunnels.

## Quick Recipes

Use these paths before listing every available flag:

| Request | Commands | Notes |
| --- | --- | --- |
| "I want my local app on the internet" / "让本地项目跑在公网" | `sealtun status`; interactive/dev: `sealtun up`; terminal console: `sealtun tui`; scripted/exact: `sealtun expose <port>` | `up` reuses current project state or discovers/asks for a port. TUI is for interactive management. Defaults to HTTPS and daemon mode. |
| "Open a terminal UI" / "用 TUI 管理隧道" | `sealtun tui` or `sealtun console` | Requires an interactive terminal. Use Tunnels to select a session, press `o` for Tunnel Actions, Create for local ports, and Tools for global commands. |
| "Expose this remote HTTP address" / "把远端地址端口转公网" | dev: `sealtun up --target http://host:port`; scripts: `sealtun expose --target http://host:port` | HTTPS-only. The target must be reachable from the machine running Sealtun. |
| "Help me get started" / "第一次怎么用" | `sealtun status`; `sealtun init`; `sealtun init --apply` only if creation is requested | `init` is read-only by default and prints a recommended command plus YAML. |
| "Give my local app a public domain" / "给本地服务一个公网域名" | `sealtun expose <port> --domain <domain>` or `sealtun domain plan <id> <domain>` | If the tunnel already exists, plan first, then add/set only when mutation is requested. |
| "Expose SSH publicly" / "公网 SSH" | `sealtun expose 22 --protocol ssh` | Return `ssh <user>@<public-host> -p <node-port>`. Do not add HTTPS auth/domain features. |
| "Expose Postgres/MySQL/Redis/MongoDB/MQTT" | `sealtun template postgres`; `sealtun expose 5432 --protocol tcp` | Common protocol templates map to generic TCP. Return `<host>:<node-port>`. |
| "Access a cluster Service from my laptop" / "本机访问集群内服务" | `sealtun connect --check`; Linux `sudo sealtun connect` | Direct TCP access to Service FQDN, Service ClusterIP, and Pod IP. No SOCKS/proxy setup. |
| "Let Pods in one Sealos region call a Service in another" / "不同区服务互通" | `sealtun mesh init`; `sealtun mesh login`; `sealtun mesh up`; `sealtun mesh service publish ...` | Service-level HTTP/TCP import. Not transparent Pod IP/CNI routing. |
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

## Terminal Console

```bash
sealtun tui
sealtun console
sealtun tui --check=false
```

Use the TUI when the user wants an interactive terminal workspace instead of many separate commands. It reuses existing CLI internals and supports:

- Keyboard focus starts on the left menu. Use up/down to switch sections, `enter`/`right`/`tab` to enter content, and `left`/`esc`/`tab` to return to the menu.
- `Tunnels`: list current tunnel sessions. Press `enter` to inspect, or `o` to open `Tunnel Actions`.
- `Tunnel Actions`: object-level inspect, doctor, logs, metrics, events, resources, export, domain, policy, share, resource tuning, repair, rotate, start, stop, and cleanup actions. Mutations show the equivalent CLI command and require confirmation.
- `Create`: discover local listening ports and create basic HTTPS, SSH, or TCP tunnels from discovered ports.
- `Tools`: global-only status, discover, init, template, connect check, domain status, doctor dry-run, export-all, and YAML apply dry-run/diff/apply operations.

The TUI requires stdin/stdout to be a real terminal. Real transparent networking with `connect`, login/profile switching, and shell completion remain explicit CLI flows.

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
sealtun init
sealtun init --protocol auto --json
sealtun init --protocol postgres --apply
sealtun up
sealtun up --guided
sealtun up 3000
sealtun up --target http://10.0.0.12:8080
sealtun up --target https://10.0.0.12:8443 --insecure
sealtun up 3000 --rate-limit 60/m --audit
sealtun expose 3000
sealtun expose --target http://10.0.0.12:8080
sealtun expose --target https://10.0.0.12:8443 --target-insecure-skip-verify
sealtun expose 3000 --foreground
sealtun expose 3000 --ready-timeout 2m
```

`init` checks local status, discovers local listening ports, and prints a recommended `expose` command plus `sealtun.yaml`. It does not create resources unless `--apply` is present. `up` is the convenience entrypoint: it reuses `.sealtun/state.json` for the current project; without state in an interactive terminal, it guides through login check, port selection, protocol, optional Basic Auth, optional rate limit/audit, optional custom domain, optional `sealtun.yaml` save, and final creation. Use `up --guided` when the user explicitly wants the wizard. Use `up 3000` or `up --target http://host:port` for direct creation, and `expose` for exact scripted creation. `expose` defaults to `https` and daemon mode. The daemon maintains the local side in the background. Use `--foreground` when the current terminal should own the tunnel lifecycle.

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

## Access Cluster Services From Local Tools

`connect` is the reverse direction of `expose`: local tools can directly access Services or Pods inside the active Sealos/Kubernetes namespace. It does not create a public URL and does not expose anything to the internet.

```bash
sealtun connect --check
sealtun connect --check --json
sudo sealtun connect
sudo sealtun connect --namespace ns-3rgvtt74
sudo sealtun connect --mode tun --listen 127.0.0.1:15443
sealtun connect status
sealtun connect status --json
sudo sealtun disconnect
```

Current behavior:

- Uses only the active Sealtun login and active kubeconfig namespace.
- `connect --check` reports capability and expected blockers.
- Linux starts transparent TCP mode by temporarily updating local `iptables` and `/etc/hosts`.
- `connect` runs in the foreground; stop with Ctrl-C or `sudo sealtun disconnect`, which cleans up the managed rules and hosts block.
- Supports Service FQDN, Service ClusterIP, and Pod IP access for TCP clients.
- Does not support ICMP/ping or UDP.
- Do not suggest SOCKS, HTTP CONNECT, `ALL_PROXY`, or `socks5h` setup as a Sealtun user-facing solution.

Examples:

```bash
curl http://my-service.default.svc.cluster.local:8080
curl http://10.96.0.12:8080
curl http://10.244.0.22:3000
```

On macOS/Windows, say the transparent data plane is currently Linux-only.

## Cross-region Mesh

Use Mesh when the user wants Pods or workloads in one Sealos region to call an explicitly published Kubernetes Service in another Sealos region. Mesh is not for exposing a local port to the public internet and is not transparent Pod IP/CNI routing.

```bash
sealtun mesh init --home gzg --regions gzg,hzh,bja
sealtun mesh login --regions gzg,hzh,bja
sealtun mesh auth status
sealtun mesh up

sealtun mesh service publish api \
  --from hzh \
  --k8s-service default/api:8080 \
  --protocol http \
  --import gzg,bja

sealtun mesh service list
sealtun mesh service check api --from gzg
sealtun mesh service unpublish api
sealtun mesh down
```

Behavior:

- `mesh init` creates `~/.sealtun/mesh/mesh.json` with the mesh registry and a shared gateway token.
- `mesh login` stores one named profile per region, such as `mesh-gzg` and `mesh-hzh`.
- `mesh up` deploys one Sealtun Mesh gateway per configured region using the same-version Sealtun image.
- `mesh service publish` imports the source Service into selected regions as `mesh-<name>.<namespace>.svc.cluster.local:<port>`.
- HTTP and TCP are supported. ICMP/ping, UDP, and transparent cross-cluster Pod IP routing are not supported.

Verification:

```bash
sealtun mesh auth status
sealtun mesh status
sealtun mesh service check <name> --from <import-region>
```

`mesh service check` should report source gateway state, import Service state, and target Kubernetes Service readiness. If it fails, classify the issue as auth/profile, gateway resource, import Service, target Service, or remote ready Pods.

Token constraints and behavior:

- Bearer and temporary tokens must be at least 8 characters.
- Stored runtime policy uses SHA-256 token hashes.
- Temporary access uses `?_sealtun_token=<token>` and strips that query parameter before forwarding to the local service or `--target` upstream.
- IP rules accept individual IPs or CIDR ranges. Sealtun reads `X-Real-IP`, then the last valid proxy-confirmed client IP in `X-Forwarded-For`, then `RemoteAddr`.
- When Basic Auth and Bearer or temporary links are both configured, either authentication path can allow the request, subject to IP rules.
- Rate limits use fixed-window specs such as `60/m` or `1000/h`.
- Access audit records allow/deny reason, status, path, and client IP. It must not expose plaintext tokens, Authorization headers, Basic Auth passwords, or temporary-link tokens.

Manage policy on an existing HTTPS tunnel:

```bash
sealtun policy show <tunnel-id>
sealtun policy set <tunnel-id> --rate-limit 60/m --audit
sealtun policy set <tunnel-id> --clear-rate-limit
sealtun policy set <tunnel-id> --no-audit
sealtun policy audit <tunnel-id> --since 10m
sealtun policy audit <tunnel-id> --since 10m --json
sealtun policy audit <tunnel-id> --since 10m --limit 200
```

Use `policy audit` when the user asks why access was allowed or denied. Reasons include Basic Auth, Bearer, temporary token, IP rule, and rate limit.

## Custom Domains

```bash
sealtun expose 3000 --domain app.example.com
sealtun expose 3000 --domain app.example.com --wait-domain --domain-timeout 5m

sealtun domain plan <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com --wait --timeout 5m
sealtun domain set <tunnel-id> app.example.com
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

Prefer `domain plan` when the user only needs DNS guidance. Use `domain add --wait` when the user explicitly wants Sealtun to wait for DNS, attach the domain, and wait for certificate readiness. `domain set` remains the direct attach command when DNS is already known to be ready.

## Protocol Templates

```bash
sealtun template https --name web --port 3000 --domain app.example.com
sealtun template ssh
sealtun template tcp --name debug --port 9000
sealtun template mysql
sealtun template postgres
sealtun template redis --name cache
sealtun template mongodb
sealtun template mqtt
```

Use templates when the user asks how to expose a common protocol or wants a starter `sealtun.yaml`. Templates are read-only and print both a one-shot `sealtun expose` command and a YAML snippet. `mysql`, `postgres`, `redis`, `mongodb`, and `mqtt` map to generic `tcp`; only `https` templates accept `--domain`.

## SSH Over Sealtun

For regions that support public TCP NodePort, prefer direct L4 SSH:

```bash
sealtun expose 22 --protocol ssh
ssh <user>@<public-host> -p <node-port>
```

`--protocol ssh` exposes only a public TCP NodePort for user traffic. HTTPS is kept only as the internal control channel used by the local daemon, not as a default application URL. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains are HTTP-layer features and are rejected for SSH tunnels.

Use SSH mode only when the user wants to expose a local SSH server or direct TCP SSH entry. It prints `Public SSH host`, `Public SSH port`, and an `ssh <user>@<public-host> -p <node-port>` command. Do not promise a custom domain for SSH; users connect with the generated host plus NodePort.

When direct NodePort is unavailable, use the WebSocket ProxyCommand fallback:

```bash
sealtun expose 22
ssh -o ProxyCommand='sealtun ssh connect <tunnel-id>' <user>@sealtun
```

`sealtun ssh connect <tunnel-id>` opens `wss://<sealos-host>/_sealtun/tcp` with the tunnel's internal secret, then bridges stdin/stdout to the remote server's active yamux session.

## Generic TCP Over Sealtun

For non-HTTP protocols such as databases, queues, or debugging services, use generic L4 TCP:

```bash
sealtun expose 5432 --protocol tcp
```

The command prints `Public TCP host`, `Public TCP port`, and `Public TCP endpoint` as `<public-host>:<node-port>`. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains are HTTPS proxy-layer features and are rejected for TCP tunnels.

## Observe And Manage

```bash
sealtun status
sealtun status --json

sealtun discover
sealtun discover --protocol auto
sealtun discover --protocol tcp
sealtun discover --json --limit 20

sealtun resources <tunnel-id>
sealtun resources <tunnel-id> --json
sealtun resources set <tunnel-id> --request-cpu 20m --request-memory 64Mi --limit-cpu 300m --limit-memory 256Mi
sealtun resources unset <tunnel-id>

sealtun watch
sealtun watch <tunnel-id>
sealtun watch <tunnel-id> --json
sealtun watch <tunnel-id> --interval 5s --count 20

sealtun list
sealtun list --check
sealtun list --json

sealtun inspect <tunnel-id>
sealtun inspect <tunnel-id> --remote
sealtun inspect <tunnel-id> --json

sealtun logs <tunnel-id>
sealtun logs <tunnel-id> --tail 200 --follow
sealtun logs <tunnel-id> --since 10m

sealtun metrics <tunnel-id>
sealtun metrics <tunnel-id> --json
sealtun metrics <tunnel-id> --remote=false
sealtun metrics <tunnel-id> --server=false

sealtun events <tunnel-id>
sealtun events <tunnel-id> --json
sealtun events <tunnel-id> --timeout 8s

sealtun doctor
sealtun doctor <tunnel-id>
sealtun doctor --json
sealtun doctor <tunnel-id> --json
sealtun doctor <tunnel-id> --report
sealtun doctor <tunnel-id> --report --report-file ./doctor.md
sealtun doctor --fix --dry-run
sealtun doctor --fix
sealtun repair <tunnel-id> --dry-run
sealtun repair <tunnel-id>
```

`sealtun discover` scans only local TCP listening ports. It does not probe external networks or create tunnels. For a remote HTTP upstream, use `sealtun expose --target http://host:port`. Standard local hints are `22 -> ssh`, `3306 -> mysql/tcp`, `5432 -> postgres/tcp`, `6379 -> redis/tcp`, `27017 -> mongodb/tcp`, `1883 -> mqtt/tcp`, and other listening ports default to HTTPS/web.

`sealtun resources` uses the current active profile/region/namespace and shows Kubernetes resource occupancy for one tunnel: Deployment replicas, Pod count, Service type, NodePort, Ingress host count, Certificate presence, Issuer, Secret metadata, and Pod CPU/memory requests/limits. It is not a cloud billing estimate, and Secret data is not displayed. `resources set` updates the remote Deployment template and stores the config in the local session; stopped tunnels stay scaled to 0 and use the new resources on the next `start`. `resources unset` resets to Sealtun defaults: request CPU `10m`, request memory `32Mi`, limit CPU `200m`, limit memory `128Mi`.

`sealtun watch` refreshes tunnel or global status until interrupted. Use `--json` for newline-delimited events when another tool needs to consume state changes.

`doctor --fix --dry-run` prints conservative automatic fixes without executing them. `doctor --fix` may start stopped tunnels, clean expired/stale sessions, or start the local daemon. It must not run `cleanup --all`, logout, DNS provider changes, or cleanup active tunnels. `repair <tunnel-id>` is the single-tunnel wrapper around this safe repair flow; prefer `repair <id> --dry-run` before execution.

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

## Export YAML

```bash
sealtun export <tunnel-id>
sealtun export --all -o sealtun.yaml
sealtun export --all --json
sealtun export --all --include-secret-placeholders
```

`export` converts local session records back into `sealtun.yaml`. It can safely export protocol, local port, custom domain, TTL, and IP allowlist/denylist. It cannot recover Basic Auth passwords, bearer tokens, or temporary link tokens because Sealtun stores only hashes; use `--include-secret-placeholders` when the user wants `passwordEnv`, `bearerTokenEnv`, and `tokenEnv` placeholders to fill manually.

## Stop And Clean Up

```bash
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun resume <tunnel-id>
sealtun cleanup
sealtun cleanup <tunnel-id>
sealtun cleanup --all
sealtun logout
sealtun logout --force
```

`stop` scales the remote tunnel Deployment to zero and keeps the domain, Service, Ingress, secrets, NodePort Service for SSH, and local session record. Use `start` or its `resume` alias to scale the same tunnel back up and reconnect it through the local daemon.

`cleanup` deletes stopped, expired, stale, or error tunnels and removes their local session records. `cleanup <tunnel-id>` targets one eligible tunnel. `cleanup --all` force deletes every locally tracked tunnel, including active ones, and should be used only when you intentionally want to remove all tracked remote resources.

`logout` first tries to clean up locally tracked tunnel resources before deleting credentials. Use `--force` only when cleanup cannot complete and local credentials must be removed anyway.
