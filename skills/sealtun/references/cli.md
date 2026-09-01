# Sealtun CLI Reference

Use this for interactive Sealtun operation: login, `up`, expose HTTPS, remote HTTP upstream targets, SSH, or generic TCP, secure public HTTP traffic, observe, bind domains, stop/start, and clean up tunnels. Install, shell completion, and the three-step quick path live in QuickStart; this file is the command-level reference.

## Intent Routing

| Request | Commands | Notes |
| --- | --- | --- |
| "Local app on the internet" / "让本地项目跑在公网" | `status`; interactive/dev: `up`; scripted: `expose <port>` | Defaults to HTTPS and daemon mode. |
| "Remote HTTP address to public" / "把远端地址转公网" | `up --target http://host:port`; scripts: `expose --target http://host:port` | HTTPS-only; target must be reachable from the Sealtun machine. |
| "Public domain" / "公网域名" | `expose <port> --domain <domain>` or `domain plan <id> <domain>` | Plan first, add only when mutation is requested. |
| "Public SSH" / "公网 SSH" | `expose 22 --protocol ssh` | Return `ssh <user>@<public-host> -p <node-port>`. |
| "Expose Postgres/MySQL/Redis/MongoDB/MQTT" | `expose <port> --protocol tcp` | Return `<host>:<node-port>`. |
| "Secure this public URL" | HTTPS `expose`, `policy set`, or `share` | Prefer env-backed secrets. Not for SSH/TCP NodePort. |

After any live operation, verify with `list --check`, `inspect <id>`, `domain status/verify`, `policy show`, or `doctor <id>`.

## Login, Regions, Profiles

```bash
sealtun login
sealtun login gzg
sealtun status
sealtun region list / current
sealtun profile list / current / save / use / delete
```

Known regions: `gzg`, `hzh`, `bja`, `cloud`, `usw`. Bare `sealtun login` shows a keyboard region selector in an interactive terminal; use `sealtun login <region>` in scripts and CI. Login state, kubeconfig, and profiles live under `~/.sealtun`.

First-use behavior: check `status` before creating cloud resources; if a browser/device authorization flow opens, wait for the user to finish instead of retrying; verify with `status` after login. For multiple accounts/regions/workspaces, prefer `login <region> --profile <name>` and `profile use <name>` over silently overwriting the active login. To switch regions, run `login <region>` again.

## Expose A Port

```bash
sealtun up                                        # guided; reuses .sealtun/state.json
sealtun up 3000 / --template postgres / --guided
sealtun up --target http://10.0.0.12:8080
sealtun up --target https://10.0.0.12:8443 --insecure
sealtun expose 3000
sealtun expose --target http://10.0.0.12:8080
sealtun expose --target https://10.0.0.12:8443 --target-insecure-skip-verify
sealtun expose 3000 --foreground
```

`up` reuses the current project's tunnel state; without state in an interactive terminal it guides through login check, port selection (with local port discovery), protocol choice (templates for `https/ssh/tcp/mysql/postgres/redis/mongodb/mqtt`), optional Basic Auth / rate limit / audit / custom domain / YAML save, and creation. `expose` is for exact scripted creation and defaults to `https` + daemon mode; the daemon keeps the local side running in the background. Use `--foreground` when the current terminal should own the tunnel lifecycle.

Use `https` for browser URLs, webhook/OAuth/payment callbacks, public previews, Basic Auth, Bearer tokens, temporary links, IP rules, or custom domains. `--target` forwards the public URL to an existing HTTP/HTTPS upstream instead of `localhost:<port>` and is HTTPS-only; use `--target-insecure-skip-verify` only when the user accepts skipping upstream TLS verification for a private upstream.

## Public Access Controls

Enforced by the Sealtun server proxy layer on public business traffic (including `--target` upstream traffic), not on `/_sealtun/ws`, health checks, or secret-protected metrics. Prefer environment variables for credentials:

```bash
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 --basic-auth-user admin --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD
export SEALTUN_BEARER_TOKEN='share-secret'
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN
sealtun expose 3000 --ip-allowlist 203.0.113.10,198.51.100.0/24 --ip-denylist 198.51.100.9
export SEALTUN_TEMP_TOKEN='review-link-secret'
sealtun expose 3000 --temporary-access-token-env SEALTUN_TEMP_TOKEN --temporary-access-ttl 1h
sealtun expose 3000 --rate-limit 60/m --audit
```

Inline one-shot forms (`--basic-auth admin:pass`, `--bearer-token x`) work but enter shell history; warn the user.

## SSH And Generic TCP

```bash
sealtun expose 22 --protocol ssh        # prints ssh <user>@<public-host> -p <node-port>
sealtun expose 5432 --protocol tcp      # prints <public-host>:<node-port>
```

Both use a public TCP NodePort for user traffic; HTTPS remains only the internal control channel. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains are HTTP-layer features and are rejected for SSH/TCP tunnels. Do not promise a custom domain for SSH/TCP; users connect with the generated host plus NodePort.

## Custom Domains

```bash
sealtun expose 3000 --domain app.example.com [--wait-domain --domain-timeout 5m]
sealtun domain plan <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com [--wait --timeout 5m]
sealtun domain verify <tunnel-id> [--wait --timeout 5m]
sealtun domain status [--verbose] [<tunnel-id>]
sealtun domain clear <tunnel-id>
```

Sealtun keeps a generated Sealos host as the control-plane host and CNAME target; the user configures `CNAME app.example.com -> <sealos-host>`. Only after CNAME verification does Sealtun write the custom host to Ingress and manage cert-manager resources. Use `domain plan` for DNS guidance, `domain add` when DNS is ready, `domain add --wait` to wait for DNS then certificate readiness, and `domain status --verbose` for detailed DNS/Ingress/certificate diagnostics.

## Observe And Manage

Operations are `status`, `list`, `inspect`, `logs`, and `doctor`. Remote diagnostics, metrics, events, and the resource inventory are `inspect` flags; continuous refresh is `list --watch` / `inspect --watch`.

```bash
sealtun status [--json]
sealtun list [--check] [--json] [--watch --interval 5s --count 20]
sealtun inspect <id> [--remote] [--metrics] [--resources] [--json] [--watch]
sealtun logs <id> [--tail 200] [--follow] [--since 10m]
sealtun doctor [<id>] [--json] [--report [--report-file p.md]] [--fix --dry-run] [--fix]
```

`inspect --remote` adds best-effort remote Kubernetes diagnostics and recent events. `inspect --metrics` adds local process state, Kubernetes readiness, Pod counts/restarts, and server counters when the remote image supports them; older images degrade to a warning. `inspect --resources` shows the Kubernetes resource inventory with occupancy hints; Secret data is never displayed and it is not a billing estimate. Resource sizing changes go through YAML `resources` and `apply`.

`doctor --fix --dry-run` prints conservative fixes without executing: starting stopped tunnels, cleaning expired/stale sessions, or starting the local daemon. It must not run `cleanup --all`, logout, change DNS providers, or clean active tunnels. `doctor <id> --report` produces a redacted Markdown report without tokens, secrets, Authorization headers, Basic Auth passwords, or kubeconfig data.

## Share Links

```bash
sealtun share create <tunnel-id> --name review --ttl 1h [--json] [--open]
sealtun share rotate <tunnel-id> review --ttl 1h
sealtun share revoke <tunnel-id> review
```

HTTPS tunnels only. `share create` prints a `?_sealtun_token=...` URL exactly once because Sealtun stores only the token hash. `share rotate` invalidates the old URL and prints the new one once. Link names and expiry metadata are visible in `policy show <tunnel-id>`.

## Rotation

```bash
sealtun rotate <tunnel-id> --server-secret [--json]
```

Rotates the tunnel server secret: the new secret is shown once, saved locally, and the remote Deployment rolls to it. Does not change the SSH/TCP access model.

## Stop And Clean Up

```bash
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup [<tunnel-id>] [--all]
sealtun logout [--force]
```

`stop` scales the remote Deployment to zero and keeps the domain, Service, Ingress, secrets, NodePort Service, and local session. `start` reopens the same tunnel through the daemon. `cleanup` deletes stopped, expired, stale, or error tunnels and their local records; `cleanup --all` force-deletes every tracked tunnel and should require explicit intent. `logout` first tries to clean up tracked tunnel resources; `--force` skips that guarantee and only removes local credentials.
