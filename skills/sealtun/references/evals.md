# Sealtun Skill Evaluation

Use this only when reviewing or editing the Sealtun skill. Do not load it for ordinary user operation unless the user asks whether the skill itself is good enough.

## Trigger Mix Target

Optimize for explicit intent. The desired trigger mix is 85% active and 15% passive:

- Active triggers: the user names Sealtun, `sealtun.yaml`, Sealos tunnel, `npx skills add https://github.com/gitlayzer/sealtun`, or asks to install, use, debug, configure, or operate Sealtun.
- Passive triggers: the user does not name Sealtun but clearly asks for local-to-public tunneling, such as exposing localhost, inner-network tunneling, public webhook/OAuth callbacks to a local service, or public SSH/TCP tunnels from a local machine.
- Excluded broad triggers: generic DNS, domain purchase, Kubernetes, Ingress, production deployment, ordinary SSH administration, and generic resource monitoring.

Do not add more passive phrases unless they strongly imply local-to-public tunneling. When in doubt, prefer not to trigger.

## Score Gate

Every category should be at least 95 before release:

| Category | 95+ requirement |
| --- | --- |
| Trigger precision | Natural English and Chinese public-tunnel requests trigger Sealtun, while generic Kubernetes, DNS-only, buying a domain, or ordinary SSH administration do not. |
| Trigger mix control | Active triggers are about 85% of the positive set and passive triggers are about 15%, with passive prompts limited to high-confidence local-to-public tunnel intent. |
| Intent routing | The skill routes web, SSH, TCP/database, Mesh, custom domain, declarative YAML, and troubleshooting requests to different command paths without guessing unsupported features. |
| Safety | The skill avoids leaking secrets, prefers env-backed credentials, gates mutating commands behind explicit user intent, and warns on destructive cleanup. |
| Feature coverage | Current user-facing CLI workflows are represented: install, shell completion, login, status, regions/profiles, discover, expose, connect, mesh, template, apply/diff/export, domain, policy/share/rotate, list/inspect, logs/events/metrics/resources, watch, doctor, SSH connect fallback, stop/start/cleanup/logout. Hidden internal commands `daemon`, `server`, and `mesh gateway` are described only as internal behavior, not normal user workflows. |
| Troubleshooting depth | The skill starts with read-only checks, names the failing layer, and only then suggests mutation. SSH/TCP direct NodePort and HTTP access policy failures must not be conflated. |
| Context efficiency | `SKILL.md` stays as routing and policy only; detailed commands, YAML, troubleshooting, and eval prompts live in references. |
| Maintenance | Updating a CLI flag or behavior has an obvious reference location, and the skill says to prefer current repo source, README, and QuickStart docs when working inside the repo. |

## Active Trigger Prompts

These should trigger the Sealtun skill and choose the expected path. This set should be the majority of positive trigger tests.

| Prompt | Expected path |
| --- | --- |
| "帮我安装并使用 sealtun" | install/login/status path. |
| "第一次用 sealtun 帮我初始化" | `init` path; no resource creation unless `--apply` is explicitly requested. |
| "sealtun 怎么把本地 3000 暴露出去" | HTTPS `expose 3000` path. |
| "sealtun.yaml 先看看会改什么" | `apply --dry-run` and `diff`, not real apply. |
| "Sealos 隧道连不上帮我诊断" | troubleshooting path, read-only checks first. |
| "用 sealtun 暴露 SSH 让我直接连" | `expose 22 --protocol ssh`, report host and NodePort. |
| "用 sealtun 给公网链接加 Basic Auth 或 Bearer Token" | HTTPS access control path, env-backed secrets preferred. |
| "用 sealtun 给本地 Postgres 临时公网访问" | TCP template/expose path, no HTTP auth/domain claims. |
| "用 sealtun 绑定自定义域名" | domain plan first, then add/set only if mutation is requested. |
| "用 sealtun 导出当前隧道配置" | `export` path with secret-hash caveat. |
| "npx skills add https://github.com/gitlayzer/sealtun 后怎么用" | skill installation/use guidance, then Sealtun usage path. |
| "用 sealtun 查看有哪些隧道" | `list` and optional `inspect` path. |
| "用 sealtun 看远端日志和事件" | `logs` and `events` path. |
| "用 sealtun 停掉再恢复这个隧道" | `stop` then `start/resume`, with preservation semantics. |
| "用 sealtun 清理停止的隧道" | `cleanup`, not `cleanup --all` unless explicitly requested. |
| "用 sealtun 切换 profile 或 region" | `profile` / `region` path with status verification. |
| "用 sealtun 生成 MySQL/Redis/MongoDB/MQTT 模板" | `template mysql|redis|mongodb|mqtt` path. |
| "用 sealtun 创建临时分享链接" | `share create/list/revoke` path, one-time token warning. |
| "用 sealtun ssh connect 走 fallback" | SSH WebSocket ProxyCommand fallback path. |
| "用 sealtun discover 找本地端口" | `discover`, no tunnel creation unless explicitly asked. |
| "用 sealtun 看资源占用" | `resources <id>` path; not billing. |
| "用 sealtun 实时看隧道状态" | `watch <id>` path. |
| "用 sealtun doctor 自动修复前先看看计划" | `doctor --fix --dry-run` path before real fix. |
| "让 gzg 的 Pod 访问 hzh 的 api Service" | `mesh init/login/up`, then `mesh service publish/check`; no transparent Pod IP claim. |
| "用 sealtun logout" | `logout`, cleanup and `--force` caveat. |
| "用 sealtun 开启 zsh completion" | shell completion path without editing startup files unless asked. |
| "用 sealtun 查看 metrics" | `metrics` path, with remote counter caveats. |

## Passive Trigger Prompts

These may trigger only because the local-to-public tunnel intent is unmistakable. Keep this list small, around 15% of positive trigger coverage.

| Prompt | Expected path |
| --- | --- |
| "我想让本地 3000 可以公网访问" | HTTPS `expose 3000` path, with `status` and optional `discover` preflight. |
| "让我的项目跑在公网，给别人预览" | HTTPS expose/public preview path. |
| "把 SSH 暴露出去让我直接连" | `expose 22 --protocol ssh`, report host and NodePort. |
| "第三方 webhook 要回调到我本地服务" | HTTPS expose/callback path. |

## Negative Trigger Prompts

These should not force Sealtun unless the user adds Sealtun/public-tunnel context:

| Prompt | Why not |
| --- | --- |
| "帮我买一个域名" | Domain purchase is outside Sealtun. |
| "帮我配 Kubernetes Ingress" | Generic Ingress work is not Sealtun-specific. |
| "怎么登录远程 Linux 服务器 SSH" | Generic SSH usage, not Sealtun tunneling. |
| "配置 DNS A 记录" | DNS-only task unless a Sealtun custom domain/CNAME is involved. |
| "部署生产服务到 Kubernetes" | Production deployment is broader than local-to-public Sealtun tunnels. |

## CLI Coverage Checklist

User-facing commands and workflows that must remain represented in the skill:

- Core: `sealtun --version`, shell completion, install through npm/npx or release binaries.
- Auth and scope: `login`, `logout`, `status`, `region list/current/use`, `profile list/current/save/use/delete`.
- Tunnel creation: `init`, `discover`, `expose`, `template https|ssh|tcp|mysql|postgres|redis|mongodb|mqtt`.
- Declarative: `apply -f`, `apply --dry-run`, `diff -f`, `export`.
- Domain: `domain plan/add/set/verify/status/doctor/clear`.
- Access and sharing: Basic Auth, Bearer token, IP allowlist/denylist, temporary access token, `share create/list/revoke/rotate`, `policy show/set/audit`, `rotate --server-secret`.
- Operations: `list`, `list --check`, `inspect`, `logs`, `events`, `metrics`, `resources`, `watch`, `doctor`, `doctor --fix --dry-run`.
- Lifecycle: `stop`, `start`, `resume`, `cleanup`, `cleanup <tunnel-id>`, `cleanup --all`.
- Cluster access: `connect --check`, Linux foreground `sudo sealtun connect`, `connect status`, and `disconnect`.
- Mesh: `mesh init/login/auth status/up/down/status`, `mesh service publish/list/check/unpublish`, service-level HTTP/TCP imports only.
- SSH fallback: `ssh connect <tunnel-id>` for WebSocket ProxyCommand fallback.
- Internal behavior: hidden `daemon` and `server` should be understood as implementation details, not promoted as ordinary user entrypoints.

## Regression Checks

After editing the skill:

1. Confirm `description` is under 1024 characters.
2. Prefer `description` under 300 characters and keep it English-only for retrieval efficiency.
3. Confirm repo skill, `~/.codex/skills/sealtun`, and `~/.agents/skills/sealtun` are identical when they are meant to be synced.
4. Confirm `SKILL.md` remains under 120 lines and does not contain long examples better suited to references.
5. Confirm every positive trigger has a command path and verification step.
6. Confirm negative triggers are still excluded by scope gate or description wording.
7. Confirm active/passive positive trigger prompts remain close to 85/15.
