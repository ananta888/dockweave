# Changelog

All notable changes to DockWeave are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
[SemVer](https://semver.org/).

## [0.1.0] — 2026-10-23

Initial release.

### Added

- Three-mount agent architecture (`/agent`, `/memory`, `/workspace` with
  rslave propagation) and hardened agent containers.
- Privileged mount manager: allowlisted sources, canonicalization,
  openat2 TOCTOU protection, RO staging sequence, EBUSY-safe unmount,
  reconcile/recovery without blind cleanup.
- Workspace registry with grants (ro/rw, exclusive, TTL) and audit hash chain.
- REST API v1 over unix socket with SO_PEERCRED auth and SSE event stream.
- CLI (`dockweave`), TUI dashboard, interactive terminals (`agent attach`,
  separate PTY socket), SDK.
- Monitoring: docker + procfs providers, activity derivation from real
  fd/cwd evidence, multi-agent conflict detection; optional Tetragon eBPF
  provider (JSONL export + PID attribution).
- Memory snapshots with restore (traversal-hardened) and retention.
- Optional MCP server (read-only tools) and AI monitor agent
  (deterministic rule-based analysis, restricted reporting API).
- CI: build/vet/test/race/govulncheck, privileged integration tests,
  release workflow with SBOM (amd64/arm64).
