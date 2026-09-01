# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Removed
- **Region switch duplication**: Removed `region use`; switch regions with `login <region>` (identical underlying flow).
- **Share list duplication**: Removed `share list`; temporary link names and expiry metadata are visible in `policy show <tunnel-id>`.

### Changed
- **Domain diagnostics merged**: `domain doctor` is now `domain status --verbose`.
- **Diff merged into apply**: `sealtun diff` is now `sealtun apply --dry-run --format diff`.



## [v0.0.39] - 2026-08-31

### Removed
- **Alpha command surface**: Removed `mesh`, `connect`, `tui`/`console`, `ssh connect`, standalone `discover`, `template`, `export`, `init`, `events`, `metrics`, `resources`, and `watch` commands along with their dedicated packages and TUI dependencies.
- **Compatibility entries**: Removed `repair` (use `doctor --fix`), `domain set` (use `domain add`), and all command aliases (`resume`, `share add`/`delete`/`remove`, `profile rm`/`remove`).

### Changed
- **Diagnostics consolidated into `inspect`**: `inspect --remote` covers remote Kubernetes diagnostics and recent events, `inspect --metrics` covers local/Kubernetes/server counters, and `inspect --resources` covers the resource inventory with occupancy hints.
- **Continuous refresh**: `list --watch` and `inspect --watch` replace the standalone `watch` command, with `--interval` and `--count` controls and NDJSON samples in `--json` mode.
- **Resource sizing**: Tunnel Pod CPU/memory changes now go exclusively through YAML `resources` declarations and `sealtun apply`; there are no imperative resource-set commands.
- **Port discovery and protocol templates**: These capabilities remain available inside `up` (guided flow and `--template`) without standalone commands.

## [v0.0.11] - 2026-05-07

### Added
- **Custom Domains**: Added `expose --domain` plus `sealtun domain set/clear`; custom domains are attached only after CNAME ownership verification.
- **Certificate Resources**: Custom-domain tunnels now create cert-manager `Issuer` and `Certificate` resources and keep the Sealos host as the CNAME target.
- **Custom Domain Diagnostics**: `inspect --remote` and `sealtun domain verify` report DNS CNAME, Ingress host/TLS, and custom-domain certificate status.
- **Domain Readiness Wait**: Added `expose --wait-domain` and `sealtun domain verify --wait` for explicit DNS, Ingress attachment, and certificate readiness waiting.

### Fixed
- **Custom Domain Safety**: Reject IP/custom domains that point at generated or reserved Sealos hosts, require verified CNAME before writing custom hosts to Ingress, validate wait timeouts, and include Ingress host/TLS plus certificate DNS names in readiness checks.
- **Cleanup Reliability**: Tunnel cleanup now always attempts Sealtun-owned Certificate, Issuer, and TLS Secret deletion by tunnel ID, even if local custom-domain session metadata is missing.

## [v0.0.10] - 2026-05-07

### Added
- **Region Management**: Added `sealtun region list`, `sealtun region current`, and `sealtun region use` for built-in Sealos Cloud regions.
- **Sealos Domain Discovery**: Login now fetches Launchpad init data and stores `SEALOS_DOMAIN` for ingress host generation.
- **Diagnostics Controls**: Added `list --check` for local target port probing and `inspect --remote` for opt-in Kubernetes diagnostics.

### Changed
- **Session Health Model**: `inspect` and `doctor` now report degraded tunnels when the tunnel owner is alive but the local target port is unreachable.
- **Legacy Migration**: First-run migration from `~/.sealos` now copies only auth and kubeconfig files, not old tunnel session records.
- **Region Contract**: Login now accepts only built-in regions to avoid partially supported custom region endpoint combinations.

### Fixed
- **Login Browser URL Handling**: Device authorization URLs are selected before printing and are restricted to safe `http`/`https` schemes.
- **Doctor Reliability**: Remote diagnostics now use bounded worker scheduling to reduce noisy timeout cascades on slow clusters.
- **Cleanup Safety**: Cleanup paths use session-scoped kubeconfig data and avoid broad app-deploy-manager label deletion.

## [v0.0.8] - 2026-04-20

### Added
- **Session Management**: Added `sealtun status` and `sealtun logout` commands for inspecting and clearing the local login session.
- **Structured Status Output**: `sealtun status` now supports `--json` output and reports kubeconfig context, cluster, namespace, and local warning conditions.
- **Test Coverage**: Added unit tests for expose validation, auth config lifecycle, and tunnel unavailable responses.

### Changed
- **Configuration Directory**: Standardized auth storage under `~/.sealtun` and added automatic migration from the legacy `~/.sealos` path.
- **Expose Validation**: `sealtun expose` now validates the local port and protocol before provisioning remote resources.
- **Readiness Handling**: Added a configurable `--ready-timeout` for waiting on the remote tunnel pod.

### Fixed
- **Tunnel Error UX**: When the local app is not listening, public requests now return a Sealtun-branded status page explaining that the local port is offline.
- **Kubernetes Apply Semantics**: Resource reconciliation now distinguishes `NotFound` from real API errors when creating or updating Deployments, Services, and Ingresses.

## [v0.0.1] - 2026-04-07

### Added
- **Authentication**: Fully aligned login flow with `sealos-auth.mjs` using OAuth2 Device Grant.
- **Browser Integration**: Automatic browser opening for a seamless authorization experience.
- **Configuration Management**: Unified storage under `~/.sealos` directory, consistent with the Sealos ecosystem.
- **Workspace Identification**: Robust automatic detection of private workspaces (supports both numeric and string `nstype`).
- **Release Automation**: Integrated GoReleaser for automated multi-platform binary builds (Linux, Windows, macOS).

### Fixed
- **Ingress Logic**: Resolved TLS certificate verification issues by ensuring one-level subdomains with the `.sealosgzg.site` suffix.
- **Protocol Mapping**: Optimized `backend-protocol` rendering to default to HTTPS and only apply special mappings (like GRPC or WS) when explicitly requested.
