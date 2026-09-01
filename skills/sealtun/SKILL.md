---
name: sealtun
description: "Use for Sealtun CLI help: expose/up HTTPS/SSH/TCP/targets, YAML apply/diff, domains, policy/audit/share, lifecycle, logs, doctor, profiles, and regions. Avoid generic Kubernetes, DNS-only, or ordinary SSH administration."
---

# Sealtun

## First Decision

Classify the request before answering or editing:

- User operation: install, shell completion, login, `up`, expose HTTPS/SSH/TCP, secure public HTTP traffic, show/set policy, audit access, create/list/revoke/rotate temporary share links, rotate server secret, plan/add/verify a custom domain, inspect state, watch with `list --watch` or `inspect --watch`, stop/start, or clean up. Read `references/cli.md`.
- Declarative configuration: `sealtun.yaml`, `apply -f` with `--dry-run`, multi-tunnel management, stable names, `ttl`, Pod resources, HTTPS access policies, SSH declarations, or generic TCP declarations. Read `references/declarative.md`.
- Troubleshooting: login/profile mismatch, daemon/session issues, local port failures, SSH/TCP direct NodePort problems, remote Kubernetes problems, DNS, Ingress, certificate, logs, metrics, or events. Read `references/troubleshooting.md`.
- Skill maintenance or quality review: trigger precision, workflow scoring, or regression prompts. Read `references/evals.md`.

Inside this repository, prefer current source, README, and QuickStart docs over these references when they conflict. Use `rg` to inspect Cobra commands and flags before changing CLI guidance.

## Intent Routing

| User intent | Primary path | Verify with |
| --- | --- | --- |
| Make a local web app, callback, preview, or webhook public | `status` -> `up` for interactive use; `expose <port>` for scripts | output URL, `list --check`, `inspect <id>` |
| Make an HTTP upstream public | `status` -> `up --target http://host:port`; `expose --target` for scripts | output URL, `inspect <id>` |
| Add HTTPS access controls | `expose`, `policy`, `share`, or YAML access policy | `inspect`, `policy show/audit`, protected request |
| Expose SSH directly | `expose 22 --protocol ssh` | printed host/port, `inspect <id> --remote` |
| Expose database, queue, MQTT, or arbitrary TCP | `expose <port> --protocol tcp` | printed `<host>:<node-port>`, protocol client, `list --check` |
| Manage many tunnels or stable config | `apply --dry-run --format diff`, then real `apply` when requested | `list`, `inspect` |
| Custom domain | `domain plan` first; `domain add --wait` only when mutation is requested | `domain verify/status` |
| Debug connectivity or unclear state | `status`, `list --check`, `inspect`, `doctor`, `logs` | layer-specific finding and next action |

## Required Execution Flow

1. Scope gate: verify the request concerns Sealtun tunnels, local-to-public exposure, Sealtun troubleshooting, or declarative Sealtun config. Do not force Sealtun into generic production, DNS-only, Kubernetes, or ordinary SSH requests.
2. Select a mode: guidance gives commands without running cloud operations; live operation runs preflight, requested mutation, and verification; troubleshooting starts with non-mutating diagnostics.
3. Gather minimum context. Prefer `sealtun --version`, `status`, `profile current`, `region current`, `list`, `inspect`, and `doctor`.
4. If not logged in, guide `sealtun login` or `sealtun login <region> --profile <name>`, then verify with `status`.
5. Do not run `up`, `expose`, real `apply`, `share create/revoke`, `domain add/clear`, `stop`, `cleanup`, or `logout` unless explicitly requested. Prefer `apply --dry-run` and `apply --dry-run --format diff` before real apply.
6. After live operations, report the exact command sequence, endpoint, tunnel ID, and final state without printing secrets.

## Verification Contracts

- `up`/`expose`: capture tunnel ID, endpoint, and protocol output; verify with `list --check` and `inspect`.
- `apply`: preview with `apply --dry-run` and `apply --dry-run --format diff`; after real apply verify every tunnel with `list` and `inspect`.
- `domain add/clear`: verify with `domain status` or `domain verify`.
- `share create/revoke/rotate`: verify with `policy show` (temporary link metadata); never repeat a one-time token.
- `stop/start/cleanup`: verify with `list` or `inspect`; `stop` preserves entry resources while `cleanup` removes eligible resources.

## Operating Rules

- Do not expose user secrets in answers, logs, commits, or generated docs. Prefer `*Env` fields and environment variables.
- Public access controls are enforced in the Sealtun server proxy layer. They protect HTTPS business traffic, not the internal control channel or SSH/TCP direct NodePort traffic.
- For SSH use `sealtun expose 22 --protocol ssh` and report the generated host and port. For generic TCP use `sealtun expose <port> --protocol tcp`.
- For declarative work, use `apply --dry-run` and `apply --dry-run --format diff` before real `apply` when feasible.
- Supported tunnel protocols are `https`, dedicated `ssh`, and generic `tcp`; UDP/gRPC are unsupported unless the repository changes.

## Response Shape

For usage questions, give a short working command sequence and only the relevant caveats. For troubleshooting, start with the lowest-cost local checks, then escalate to remote Kubernetes diagnostics.
