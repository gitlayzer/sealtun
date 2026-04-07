# Changelog

All notable changes to this project will be documented in this file.

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
