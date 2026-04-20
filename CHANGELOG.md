# Changelog

All notable changes to this project will be documented in this file.

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
