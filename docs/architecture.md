# Architecture Overview

## Components

```
┌─────────────────────────────────────────────────────────────┐
│  dockweaved (root daemon, CAP_SYS_ADMIN)                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐  │
│  │ REST API │  │  Agents  │  │Workspaces│  │ Supervisor │  │
│  │  v1      │  │  Service │  │ Service  │  │ (recovery) │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └──────┬─────┘  │
│       │              │              │               │        │
│  ┌────┴──────────────┴──────────────┴───────────────┴─────┐ │
│  │               internal/persistence (SQLite)            │ │
│  └────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────┐  ┌──────────────────────────────┐  │
│  │ internal/mounts     │  │ internal/monitor             │  │
│  │ (privileged ops)    │  │ (Docker + procfs providers)  │  │
│  └─────────────────────┘  └──────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
        │ unix socket                  │ docker API
        ▼                              ▼
   dockweave (CLI)              Docker Engine
   dockweave tui (TUI)               │
                                ┌────┴────┐
                                │ Agent   │
                                │Container│
                                └─────────┘
```

## Data Flow

### Agent Lifecycle

```
CLI: dockweave agent create codex-01 --image ...
  → API POST /api/v1/agents
    → agents.Service.Create()
      → persistence: INSERT agent
      → mounts.PrepareAgentTree() (bind + make-rshared)
      → audit event

CLI: dockweave agent start codex-01
  → API POST /api/v1/agents/codex-01/start
    → agents.Service.Start()
      → docker.EnsureImage()
      → docker.Create() (3 binds: /agent, /memory, /workspace:rslave)
      → docker.Start()
      → persistence: UPDATE state=running
```

### Dynamic Workspace Sharing

```
CLI: dockweave grant create myproject codex-01 --mode rw
  → API POST /api/v1/grants
    → workspaces.Service.Grant()
      → mounts.Add(agent, source, target, mode)
        [RO staging if mode=ro]
      → persistence: INSERT grant + mount_state
      → audit event
```

### Mount Propagation (Phase-1 validated)

```
Host:  /var/lib/dockweave/agents/<id>/workspace  (rshared)
         └── myproject/  ← bind mount (added at runtime)
                        ↕ propagates to agent container
Container: /workspace   (rslave bind of workspace tree)
         └── myproject/  ← visible immediately, no restart
```

**Key constraint:** RO binds use the staging sequence
(private bind → remount ro → mount --move) because remount flags do not
propagate to slave namespaces. See `docs/validation/phase1-mounts.de.md`.

## Persistence Schema

| Table | Purpose |
|---|---|
| `agents` | Agent identities (independent of containers) |
| `workspaces` | Registered shareable host directories |
| `grants` | Per-agent access authorizations |
| `mounts` | Desired mount state (Soll-Zustand) |
| `events` | Audit log with hash chain |
| `meta` | Schema version, config |

## Module Map

| Package | Responsibility |
|---|---|
| `internal/model` | Domain types (Agent, Workspace, Grant, MountState, Event) |
| `internal/persistence` | SQLite registry, migrations, hash chain |
| `internal/mounts` | Privileged mount manager (add/remove/reconcile) |
| `internal/dockerclient` | Docker Engine API wrapper |
| `internal/agents` | Agent lifecycle service |
| `internal/workspaces` | Workspace registry + grant logic |
| `internal/monitor` | Activity tracking (Docker + procfs providers) |
| `internal/api` | REST API v1 over Unix socket |
| `internal/supervisor` | Startup recovery, desired-vs-actual reconciliation |
| `pkg/sdk` | Go client for the API |
| `ui/tui` | Bubble Tea terminal UI |
