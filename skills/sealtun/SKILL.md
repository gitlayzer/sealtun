---
name: sealtun
description: "Use for Sealtun CLI help: mesh, tui/console, expose/up HTTPS/SSH/TCP/targets, connect to cluster Services, YAML, domains, policy/audit/share, resources, doctor. Avoid generic Kubernetes or DNS-only tasks."
---

# Sealtun

## First Decision

Classify the request before answering or editing:

- User operation: install, shell completion, TUI/console, guided init, login, `up`, discover local ports, expose HTTPS, remote HTTP upstream targets, SSH, or generic TCP, access cluster-internal Services/Pods with `connect`, build service-level cross-region Mesh, generate protocol templates, secure public HTTP traffic, show/set policy, audit access, create/list/revoke/rotate temporary share links, rotate server secret, plan/add/verify a custom domain, inspect state, watch status, view or tune resources, stop/start/resume, clean up, or export YAML. Read `references/cli.md`.
- Declarative configuration: `sealtun.yaml`, `apply -f`, `diff -f`, `export`, multi-tunnel management, stable names, `ttl`, Pod resources, HTTPS access policies, SSH tunnel declarations, or generic TCP tunnel declarations. Read `references/declarative.md`.
- Troubleshooting: login/profile mismatch, daemon/session issues, local port discovery/failures, SSH/TCP direct NodePort problems, remote Kubernetes problems, resource lists/resource occupancy, DNS, Ingress, certificate, logs, metrics, or events. Read `references/troubleshooting.md`.
- Skill maintenance or quality review: trigger precision, workflow scoring, or regression prompts for this skill. Read `references/evals.md`.

If the request is inside the Sealtun repository, prefer the current source tree, README, and QuickStart docs over these references when they conflict. Use `rg` to inspect Cobra commands and flags before changing CLI usage guidance.

## Intent Routing

Use the user's intent to choose the shortest safe path:

| User intent | Primary path | Verify with |
| --- | --- | --- |
| Make a local web app, dev server, callback, preview, or webhook public | `status` -> `up` for interactive/dev use; `expose <port>` for scripts | URL from output, `list --check`, `inspect <id>` |
| Manage tunnels from an interactive terminal | `tui` or `console` | selected tunnel state, confirmed action result, `list --check` |
| Make an HTTP service reachable from this machine public | `status` -> `up --target http://host:port` for dev use; `expose --target` for scripts | URL from output, `inspect <id>`, protected request behavior |
| Add Basic Auth, Bearer token, IP rules, rate limit, audit, or temporary links | HTTPS `expose`, `policy`, `share`, or YAML access policy | `inspect <id>`, `policy show/audit`, protected request behavior |
| Expose SSH directly | `expose 22 --protocol ssh` | printed SSH host/port, `inspect <id> --remote`, user SSH client output |
| Expose database, queue, MQTT, or arbitrary TCP | `template <protocol>` for guidance, then `expose <port> --protocol tcp` | printed `<host>:<node-port>`, protocol client, `list --check` |
| Access a Service/Pod inside the active Sealos namespace from local tools | `connect --check`, then Linux `sudo sealtun connect` | Service FQDN, ClusterIP, or Pod IP TCP access; no SOCKS/proxy config |
| Import a Kubernetes Service across Sealos regions | `mesh init`, `mesh login`, `mesh up`, then `mesh service publish/check` | imported `mesh-<name>` ClusterIP Service and `mesh service check` |
| Manage many tunnels or stable config | edit `sealtun.yaml`, then `apply --dry-run`, `diff`, real `apply` only when requested | apply output, `list`, `inspect` |
| Custom domain | `domain plan` first; `domain add --wait` only when mutation is requested | `domain verify/status`, DNS CNAME, certificate status |
| Tune tunnel Pod resources | `resources <id>` first, then `resources set` or YAML `resources` when requested | `resources <id>`, rollout/start behavior |
| Debug connectivity or unclear state | non-mutating checks first: `status`, `list --check`, `inspect`, `resources`, `doctor`, `logs/events/metrics` | layer-specific finding and next action |
| Watch or repair tunnel state | `watch` for status changes; `doctor --fix --dry-run` or `repair <id> --dry-run` before execution | dry-run plan, then verified state |

## Required Execution Flow

Follow this flow after the skill triggers:

1. Scope gate: verify the request is about making a local/dev service publicly reachable, operating a Sealtun tunnel, troubleshooting Sealtun, or declarative Sealtun config. If it is only generic production deployment, buying a domain, DNS-only configuration, generic Kubernetes, or generic SSH without Sealtun tunneling, do not force Sealtun into the answer.
2. Select one mode before acting:
   - Guidance mode: user asks how to use Sealtun. Load the matching reference and give commands; do not run live tunnel/cloud commands.
   - Live operation mode: user explicitly asks to execute, create, apply, stop, clean up, or bind a domain. Run preflight checks first, then the requested command, then verification.
   - Troubleshooting mode: user reports a problem. Run non-mutating diagnostics first, identify the likely layer, then propose or perform fixes only when the requested action is clear.
3. Gather minimum context. Inside this repo, inspect current code/README/QuickStart docs before relying on references. Outside the repo, use the references as the command source. Prefer non-mutating checks such as `sealtun --version`, `sealtun status`, `sealtun init`, `sealtun profile current`, `sealtun region current`, `sealtun discover`, `sealtun list`, `sealtun inspect`, `sealtun resources`, and `sealtun doctor`.
4. Handle first-use authorization gently. If `status` shows no login, explain that Sealtun needs Sealos authorization and kubeconfig before creating cloud resources. Guide the user through `sealtun login`, or `sealtun login <region> --profile <name>` when region/profile matters. If a browser/device authorization flow opens, tell the user to complete it in the browser and wait; do not treat the pause as a failure. After login, verify with `sealtun status`, `sealtun region current`, and optionally `sealtun profile current`.
5. Control mutations. Do not run `sealtun up`, `sealtun expose`, real `sealtun apply`, `sealtun mesh up/down`, `sealtun mesh service publish/unpublish`, `sealtun share create/revoke`, `sealtun domain add/set/clear`, `sealtun stop`, `sealtun cleanup`, or `sealtun logout` unless the user explicitly asked for that operation in the current task. `sealtun template`, `sealtun domain plan`, `sealtun mesh status`, `sealtun mesh service list/check`, `sealtun share list`, `sealtun export`, `apply --dry-run`, `diff`, and read-only diagnostics are safe guidance steps. For declarative changes, prefer `apply --dry-run` and `diff` before real `apply`.
6. Verify completion. After live operations, use the contract below, then report the exact command sequence and final state without printing secrets.

## Verification Contracts

- `up`/`expose`: capture tunnel ID, public endpoint, and protocol-specific output; verify with `sealtun list --check` and `sealtun inspect <tunnel-id>`. For `up`, mention whether it reused `.sealtun/state.json`, selected a discovered port, or used explicit port/target input.
- `apply`: run or recommend `apply --dry-run` and `diff` first; after real apply, verify every intended tunnel with `list` and `inspect`.
- `domain add/set/clear`: verify with `domain status` or `domain verify`; for `add --wait`, report DNS/CNAME and certificate readiness separately.
- `share create/revoke/rotate`: verify with `share list`; never repeat a one-time share token unless it is the command's immediate output.
- `stop/start/cleanup`: verify with `list` or `inspect`; remember `stop` preserves entry resources while `cleanup` removes stopped, expired, stale, or error tunnel resources.
- `resources set/unset`: verify with `resources <id>`; stopped tunnels stay scaled to 0 while their Deployment template is updated.
- `mesh`: verify `mesh auth status`, `mesh status`, and `mesh service check <name> --from <region>`; explain that Mesh v1 is service-level HTTP/TCP import, not transparent Pod IP/CNI routing.
- Troubleshooting: name the failing layer before proposing a mutation: local login/profile, local port, daemon/session, remote resource, DNS/certificate, access policy, or user protocol/auth. Use `doctor <id> --report` when the user needs a redacted support artifact.

## Operating Rules

- Do not expose user secrets in final answers, logs, commits, or generated docs. Prefer `*Env` fields and environment variables for passwords and tokens unless the user explicitly wants a one-shot inline example.
- Explain that Sealtun public access controls are enforced in the Sealtun server proxy layer, not by Ingress annotations. They protect HTTPS public business traffic, including `--target` upstream traffic, not the internal `/_sealtun/ws` control channel and not SSH/TCP direct NodePort traffic.
- For SSH exposure, prefer `sealtun expose 22 --protocol ssh` when the region supports public TCP NodePort. Use `sealtun ssh connect <tunnel-id>` only as a WebSocket ProxyCommand fallback.
- For generic TCP exposure, prefer `sealtun expose <port> --protocol tcp` and report the generated `<public-host>:<node-port>` endpoint.
- For declarative work, run or recommend `sealtun apply -f sealtun.yaml --dry-run` and `sealtun diff -f sealtun.yaml` before a real apply when feasible.
- For HTTPS policy operations, use `policy show`, `policy set --rate-limit 60/m --audit`, and `policy audit --since 10m`; audit output must not include plaintext tokens, Authorization headers, or Basic Auth passwords.
- For temporary share links, use `sealtun share create <tunnel-id> --ttl 1h --name review` only for HTTPS tunnels; tell users the URL is shown once because Sealtun stores only a token hash. Use `share rotate` to invalidate an old token and print a new one-time URL, `share list` for metadata, and `share revoke` by name.
- For server secret rotation, use `sealtun rotate <tunnel-id> --server-secret`; note the new secret is one-time output and SSH/TCP access policy remains unchanged.
- For exporting config, use `sealtun export <tunnel-id>` or `sealtun export --all -o sealtun.yaml`. Explain that stored password/token hashes cannot be recovered; `--include-secret-placeholders` emits env var placeholders.
- For cluster-internal access, use `sealtun connect --check`, then on Linux `sudo sealtun connect`. Explain that it transparently redirects TCP Service FQDN, Service ClusterIP, and Pod IP traffic through Kubernetes `pods/portforward`; it is not SOCKS/HTTP proxy config and does not support ICMP/ping or UDP.
- For cross-region Mesh, use `sealtun mesh init --regions ...`, `mesh login`, `mesh up`, and `mesh service publish <name> --from <region> --k8s-service <namespace>/<service>:<port> --import ...`. Explain that it creates one gateway per region and imports Services as `mesh-<name>` ClusterIP Services in other regions.
- For first-time users, prioritize a clear path: install, `sealtun login`, `sealtun init`, then `sealtun up` for interactive local development or `sealtun expose` for exact scripted creation. Mention that login stores credentials under `~/.sealtun`, project tunnel state lives in `.sealtun/state.json`, and profiles are useful for multiple Sealos accounts, regions, or workspaces.
- Use exact command names and flags from the repository when modifying instructions. Supported tunnel protocols are `https`, dedicated `ssh`, and generic `tcp`; UDP/gRPC are not supported unless the repo adds them.

## Response Shape

For usage questions, give a short working command sequence and explain only the relevant gotchas. For troubleshooting, start with the lowest-cost local checks, then escalate to remote Kubernetes diagnostics.
