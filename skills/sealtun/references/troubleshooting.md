# Sealtun Troubleshooting

Prefer Stable `status`, `list`, `inspect`, `doctor`, and `logs` checks first. Standalone `discover`, `events`, `metrics`, `resources`, and `watch` are Alpha diagnostics; disclose their stability when they are the right tool for the reported problem.

Use this when users report tunnel failures, auth issues, SSH/TCP connection failures, stale sessions, stop/start confusion, domain problems, missing metrics, or confusing CLI output.

## Fault Signature Map

Start with the symptom, then confirm the layer before changing anything:

| Symptom | Likely layer | First checks | Typical fix |
| --- | --- | --- | --- |
| Public URL shows offline/degraded | Local app, upstream target, or daemon | `list --check`, `inspect <id>`, `curl 127.0.0.1:<port>` or `curl <target>` | Start the local app, fix target reachability, or restart/resume tunnel |
| `ssh` connects then closes after auth starts | Local sshd/user auth | `ssh -vvv`, `logs <id>`, local sshd logs | Fix user/key/password/PAM on the machine running Sealtun |
| TCP connection opens then closes | Local target protocol/auth | `inspect <id> --remote`, protocol client logs | Fix database/service bind/auth/TLS expectations |
| Custom domain not ready | DNS/CNAME or certificate | `domain plan`, `domain verify`, `domain doctor` | Correct CNAME, then wait/verify certificate |
| `resources` output shows missing or warning resources | Remote Kubernetes | `doctor <id>`, `events <id>`, `inspect <id> --remote` | Fix image/pod/service/ingress/cert issue named by diagnostics |
| Basic Auth/Bearer/link/rate limit fails | HTTPS access policy | `inspect <id>`, `policy show <id>`, `policy audit <id>`, token length/expiry/IP/rate | Fix credential source, token, temporary link expiry, IP rules, or rate limit |
| Cross-region Mesh import cannot be reached | Mesh profile, gateway, import Service, or source Service | `mesh auth status`, `mesh status`, `mesh service check <name> --from <region>` | Re-login missing region profile, run `mesh up`, fix import/source Service readiness |

Do not jump straight to `cleanup --all`; it is destructive and only appropriate when the user intentionally wants all tracked remote resources removed.

## Fast Local Checks

```bash
sealtun status
sealtun region current
sealtun profile current
sealtun list --check
sealtun inspect <tunnel-id>
sealtun doctor
```

Start by checking login, active region/profile, local session records, and whether the configured target is reachable from the machine running Sealtun.

## Login, Region, Profile

Symptoms:

- `not logged in. Please run 'sealtun login' first`
- A tunnel appears in the wrong namespace or region.
- `apply` refuses to update a tunnel because region or namespace differs.
- First-time setup pauses while waiting for browser/device authorization.

Actions:

```bash
sealtun status --json
sealtun region list
sealtun region current
sealtun profile list
sealtun profile use <name>
sealtun login <region> --profile <name>
```

Profiles are stored under `~/.sealtun/profiles/<name>`. Switching a profile updates the active auth and kubeconfig used by later commands.

For first-time authorization, make the flow explicit: Sealtun needs Sealos authorization to obtain Kubernetes credentials for the active workspace. Ask the user to complete the browser/device flow, then verify with `sealtun status` before running `expose`, real `apply`, `domain add`, or cleanup operations.

## Daemon And Session Issues

Symptoms:

- Tunnel is listed but not connected.
- A stale tunnel remains after the owner process exits.
- Local daemon does not pick up an applied tunnel.

Actions:

```bash
sealtun list
sealtun inspect <tunnel-id>
sealtun doctor
sealtun doctor <tunnel-id>
sealtun doctor <tunnel-id> --report
sealtun doctor --fix --dry-run
sealtun doctor <tunnel-id> --fix --dry-run
sealtun stop <tunnel-id>
sealtun cleanup
```

`expose` normally starts daemon mode unless `--foreground` is used. `apply` also ensures the local daemon is running after successful cloud changes. `stop` intentionally preserves remote entry resources and scales the pod to zero; `start` or `resume` reopens it. `cleanup` deletes stopped, expired, stale, or error tunnels; `cleanup --all` force deletes all locally tracked tunnels.
Use `doctor <tunnel-id> --fix --dry-run` before `doctor <tunnel-id> --fix`. Automatic fixes are conservative: start stopped tunnels, clean expired/stale sessions, or start the local daemon. They must not clean active tunnels, run `cleanup --all`, logout, or modify DNS provider settings. The old `repair` command is a Deprecated compatibility alias. Use `doctor <id> --report` when a user needs a redacted Markdown artifact for support; it must not leak tokens, secrets, Authorization headers, Basic Auth passwords, or kubeconfig data.

## SSH Direct TCP Problems

Symptoms:

- `ssh <user>@<public-host> -p <node-port>` connects then closes.
- SSH works through ProxyCommand but not direct NodePort.
- User expects a normal HTTPS URL for an SSH tunnel.

Actions:

```bash
sealtun inspect <tunnel-id> --remote
sealtun logs <tunnel-id> --tail 200
sealtun list --check
ssh -vvv <user>@<public-host> -p <node-port>
```

Direct SSH uses `--protocol ssh` and a public TCP NodePort. The public host plus NodePort is the user-facing entry; HTTPS is only the internal Sealtun control channel. Basic Auth, Bearer tokens, temporary links, IP policies, and custom domains do not apply to SSH. If the SSH debug log reaches authentication and then closes, inspect the local sshd authentication policy, user, keys/password, and PAM/keyboard-interactive behavior on the machine running Sealtun.

For generic TCP, use `--protocol tcp` and connect to `<public-host>:<node-port>` with the protocol-specific client, for example `psql -h <public-host> -p <node-port>`. If the connection opens and closes, inspect the local target service logs and authentication settings on the machine running Sealtun.

## Target Unreachable

Symptoms:

- Public URL returns the Sealtun offline page.
- `list --check` shows degraded local target health.
- `inspect` shows the tunnel owner alive but target port unreachable.

Actions:

```bash
sealtun discover
sealtun list --check
sealtun inspect <tunnel-id>
lsof -i :3000
curl -v http://127.0.0.1:3000/
curl -v http://10.0.0.12:8080/
```

Use `sealtun discover` when the user is unsure which local port is actually listening; it scans local TCP listening ports only and provides protocol/template hints without creating a tunnel. For `--target`, do not use discovery; verify the explicit upstream URL from the machine running Sealtun. Fix the local service or upstream reachability first. Sealtun forwards to `localhost:<localPort>` for local tunnels, or to `target` for HTTPS upstream tunnels.

If an HTTPS upstream works through a local reverse proxy but direct `--target https://...` returns the Sealtun 502 page, check the upstream certificate from the machine running Sealtun:

```bash
curl -vk https://10.0.0.12:8443/
```

For private/self-signed HTTPS upstreams, use `--target-insecure-skip-verify` or YAML `targetTls.insecureSkipVerify: true` only when skipping upstream certificate verification is acceptable. This does not weaken the public Sealtun HTTPS URL; it only changes the local client to upstream TLS check.

## Remote Kubernetes Or Pod Problems

Symptoms:

- Timed out waiting for tunnel server.
- Remote pod is not ready.
- Image pull, service, or ingress errors.

Actions:

```bash
sealtun doctor <tunnel-id>
sealtun doctor <tunnel-id> --report
sealtun inspect <tunnel-id> --remote
sealtun resources <tunnel-id>
sealtun logs <tunnel-id> --tail 200
sealtun metrics <tunnel-id> --json
sealtun events <tunnel-id>
sealtun doctor --json
```

Remote diagnostics inspect the Sealtun-managed Deployment, Service, Ingress, Pod, Events, and readiness where available. If code changes are needed, inspect `pkg/k8s`, `cmd/inspect.go`, `cmd/doctor.go`, and related tests.
Prefer `sealtun doctor <tunnel-id>` before asking the user to manually inspect Kubernetes. It summarizes local owner state, local port reachability, remote resource readiness, and next-step suggestions.

Use `sealtun resources <tunnel-id>` for a read-only resource list and resource occupancy hints. It shows Deployment, Pod, HTTP Service, TCP NodePort Service, Ingress, Certificate, Issuer, and Secret metadata, but it does not estimate cloud billing and never displays Secret data.

## Cross-region Mesh Problems

Symptoms:

- `mesh service check` reports missing gateway or import Service.
- Pods in one region cannot access `mesh-<name>.<namespace>.svc.cluster.local:<port>`.
- A region profile is missing or points to the wrong namespace.

Actions:

```bash
sealtun mesh auth status
sealtun mesh status
sealtun mesh service list
sealtun mesh service check <name> --from <import-region>
sealtun mesh up
```

Mesh is service-level HTTP/TCP import, not transparent cross-cluster Pod IP/CNI routing. Do not debug it as generic Ingress or local `connect`. Check layers in order:

1. The source and import regions must exist in `~/.sealtun/mesh/mesh.json`.
2. Each region must have a valid `mesh-<region>` profile.
3. Each region must have a ready Mesh gateway Deployment, Service, and Ingress.
4. Each import region must have a `mesh-<service>` ClusterIP Service selecting the current Mesh gateway.
5. The source region must have the target Kubernetes Service and ready backend Pods.

## Custom Domain, DNS, Certificate

Symptoms:

- Custom domain still points to the Sealos host only.
- `domain add` or `apply` refuses an unverified domain.
- Certificate is not ready.

Actions:

```bash
sealtun domain status
sealtun domain plan <tunnel-id> app.example.com
sealtun domain add <tunnel-id> app.example.com --wait --timeout 5m
sealtun domain verify <tunnel-id>
sealtun domain verify <tunnel-id> --wait --timeout 5m
sealtun domain doctor <tunnel-id>
```

Confirm the user configured:

```text
CNAME <custom-domain> -> <sealos-host>
```

Sealtun intentionally verifies the CNAME before writing the custom host to Ingress. This avoids claiming arbitrary hosts in shared Ingress infrastructure. Certificate readiness depends on cert-manager after the custom host is attached.
Use `domain plan` for non-mutating DNS guidance. Use `domain add --wait` only when the user explicitly wants to bind the domain and wait for readiness.

## Access Control Problems

Symptoms:

- Basic Auth prompt is missing or rejects credentials.
- Bearer token requests return unauthorized.
- Temporary link stopped working.
- IP allowlist denies a caller unexpectedly.
- Requests return 429 or users are unsure why a request was allowed/denied.

Actions:

```bash
sealtun inspect <tunnel-id>
sealtun policy show <tunnel-id>
sealtun policy audit <tunnel-id> --since 10m
sealtun logs <tunnel-id> --tail 200
sealtun metrics <tunnel-id> --json
```

Check the session access policy. Tokens must be at least 8 characters. Temporary links expire at `expiresAt`; query parameter name is `_sealtun_token`. IP decisions use `X-Real-IP`, then the last valid proxy-confirmed client IP in `X-Forwarded-For`, then `RemoteAddr`, so upstream proxy headers matter. Rate limits use fixed-window specs such as `60/m`; `429` responses should appear in `policy audit` with reason `rate-limit`.

Access audit must not show plaintext tokens, Authorization headers, Basic Auth passwords, or temporary-link tokens. If a user needs to replace a leaked or stale temporary link, use `share rotate <tunnel-id> <name> --ttl 1h`; for the internal tunnel server secret, use `rotate <tunnel-id> --server-secret`.

## Metrics

`metrics` combines local session data, remote Kubernetes readiness, and server counters where the remote image supports them. Missing server counters usually mean the remote pod is older or unreachable, not necessarily that the tunnel is down.


## Troubleshooting Response Shape

Answer in this order:

1. State the suspected layer and why.
2. Run or recommend the lowest-cost read-only checks.
3. Interpret the result in Sealtun terms: local port, daemon/session, remote pod/service/ingress, DNS/certificate, access policy, or user protocol/auth.
4. Only then suggest a mutation such as `start`, `stop`, `domain add`, or `cleanup`.
