# ADR 0001 — Credential Broker Architecture

- Status: accepted (MVP)
- Date: 2026-11-08
- Branch: `feat/secure-credential-broker`

## Context

DockWeave isolates agent filesystems, mounts and capabilities. It does not
isolate API credentials: agents receive provider keys through `EnvFile`
(`internal/agents/agents.go`, `Start()` reads `a.EnvFile`), through image
environment, or through files in `/memory`. Docker `inspect` exposes the
container environment, a compromised agent can read it, and prompt injection
can exfiltrate it. Credential exfiltration is therefore outside the current
threat model.

The goal is a local, open-source, zero-cloud credential broker where an agent
can call an API without ever holding the real credential.

## Current-state analysis (repository references)

| Area | Finding | Reference |
|---|---|---|
| Agent identity | Stable `Agent` with type/image/env file; env values are persisted in SQLite and passed to Docker | `internal/model/model.go`, `internal/agents/agents.go` |
| Env secrets | `EnvFile` is read at start and merged into the runtime spec | `agents.go` `Start()` |
| Container creation | Three binds (`/agent`, `/memory`, `/workspace`), hardening, `Env` from agent | `internal/dockerclient/dockerclient.go` `Create()` |
| Secret exposure | Env is visible in Docker inspect; env file path is persisted; logs may contain CLI errors | `dockerclient`, `persistence` |
| Socket auth | `SO_PEERCRED` + UID→role policy (`ADMIN_UIDS`, `MONITOR_UIDS`) | `internal/api/auth.go` |
| Filesystem grants | Per-agent workspace grants with ro/rw, TTL, mount lifecycle | `internal/workspaces/workspaces.go` |
| Audit | Hash-chained events in SQLite, no secret redaction pipeline | `internal/persistence` |
| Network | `NetworkMode` bridge/none; no egress enforcement | `internal/dockerclient`, `internal/agents` |
| Persistence | SQLite with versioned migrations; pure-Go `modernc.org/sqlite` | `internal/persistence/persistence.go` |
| Surfaces | REST API v1 over unix socket, CLI, Bubble Tea TUI, SDK | `internal/api`, `cmd/dockweave`, `ui/tui`, `pkg/sdk` |
| Recovery | Supervisor reconciles mounts/agents; no broker concept | `internal/supervisor` |

## Upstream analysis — Infisical Agent Vault

Repository: <https://github.com/Infisical/agent-vault>
Upstream commit examined: `16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba`
(2026-11-06). License: MIT for everything outside `ee/`; `ee/` is separately
licensed. No `ee/` code was used.

The standalone project is explicitly deprecated for new installations in its
README in favour of Infisical's hosted Agent Vault. Relevant components:

| Component | What it does | Reuse assessment |
|---|---|---|
| `internal/crypto` | AES-256-GCM + Argon2id (51 + 51 LOC) | Reused conceptually; copied with attribution and hardened with AAD + atomic writes |
| `internal/store` | SQLite store, ~3.6k LOC, multi-tenant vaults, users, OAuth, postgres support | Too large and coupled; not reused |
| `internal/mitm` | MITM proxy, custom CA, WebSocket forwarding | Not reused: requires installing a CA in agents and bypasses the "no MITM" preference |
| `internal/netguard` | Private-range/IMDS blocking in dialer control | Concept reused for egress design; not wired into this MVP |
| `internal/ratelimit` | Token buckets, sliding windows, semaphores | Concept reused; implemented minimally in `budget.go` |
| `internal/server` | 1.4k LOC HTTP admin API + web UI + multi-user auth | Not reused: DockWeave already has an operator identity model |
| `internal/isolation` | Docker sandbox creation | Not reused: DockWeave owns container creation |

Conclusion: the useful, auditable core is the small crypto layer and the
policy/netguard design. The rest is a product stack (users, web UI, hosted
sync, OAuth, postgres) that would bring stale dependencies and a second
control plane into DockWeave.

## Options considered

| Option | Security | Maintainability | Effort | Fit | Decision |
|---|---|---|---|---|---|
| A. Run original Agent Vault as sidecar | Good, but MITM CA and second admin plane | Deprecated upstream; large dependency tree | Low initially, high long-term | Poor | Rejected |
| B. Fork MIT core into DockWeave | Good | Fork drift, 50k LOC, stale deps | High | Poor | Rejected |
| C. Reuse crypto + implement slim DockWeave broker | Good for HTTP gateway; egress still separate | Small, owned code, few deps | Medium | High | **Accepted** |
| D. Gateway-only with restricted endpoint | Same as C if destination is fixed | Small | Medium | High | **Accepted as the C shape** |

Option C/D is chosen: a standalone, unprivileged `dockweave-broker` binary
with a fixed-destination HTTP gateway. No TLS interception, no custom root CA,
no provider key in the agent. The root daemon stays out of the credential
path.

## Decision

1. **Separate process, separate privilege.** `dockweave-broker` runs as the
   operator, never as root. It performs no mount or Docker operations.
2. **Encrypted local vault** (`internal/credentialvault`): AES-256-GCM,
   Argon2id, versioned envelope, atomic writes, `0600`, per-entry AAD,
   passphrase from environment only.
3. **HTTP gateway, not MITM** (`internal/credentialbroker`): routes
   `/p/{provider}/...` to a fixed provider base URL and injects the credential
   there. The agent configures a provider `baseURL`/`OPENAI_BASE_URL` and uses
   the short-lived session token as its dummy API key.
4. **DENY by default policy** with per-agent credential grants, model
   allowlists, rate/concurrency/request/token/cost limits, expiry.
5. **Short-lived session tokens** (`dws_…`, SHA-256 stored, raw token printed
   once) with immediate revocation and 24 h maximum TTL.
6. **No root daemon expansion.** DockWeave's daemon remains unaware of
   plaintext credentials. Integration is via the broker's own admin socket.
7. **Egress enforcement is out of scope of this increment.** It is documented
   as an open blocker in `docs/egress-security.md`; this build does not claim
   network isolation.

## Consequences

Positive: no provider secret in container env, inspect, logs, SQLite or
`/memory`; small auditable crypto surface; no MITM CA; no new root code.

Negative / open: the agent still needs a way to reach the broker socket or a
restricted TCP address; until egress enforcement lands, a compromised agent
can bypass the broker by calling the provider directly if it has internet
access. Budget enforcement is broker-side estimation, not provider billing
truth. A copied session token is usable within its grant until expiry or
revocation. The TUI and REST API integration are not yet implemented; CLI is
the operator surface in this increment.

## License and provenance

- Cryptography approach and API shape adapted from Infisical Agent Vault
  `internal/crypto` (MIT, Copyright (c) 2022 Infisical Inc.), upstream commit
  `16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba`. The implementation in this
  repository is a rewrite with additional AAD binding and atomic writes.
- No `ee/` code and no web/UI assets were copied.
- All other code in this change is original DockWeave code.
