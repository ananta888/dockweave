# Memory: Snapshots, Restore, Backup & Retention

Every memory store has its own identity (`mem_<id>`) under
`/var/lib/dockweave/memories/<mem-id>`, decoupled from agent identities
(review-009). Exactly one agent is the *write owner* at a time; a second
writer is rejected (AGENTS rule 6). The store survives container
recreation, workspace changes and agent upgrades — and can be attached to
a replacement agent without data loss.

## Identity, attach & detach

```bash
dockweave memory memories              # list memories and their write owners
dockweave memory detach codex-01       # unbind (data is kept)
dockweave memory attach codex-02 mem_ab12…   # bind to a replacement agent
```

- Attach/detach are refused while the agent container runs (its `/memory`
  mount is live); stop the agent first.
- A memory can only be attached when it is unbound (or already owned by the
  target agent); an agent holds at most one memory. Both rules are enforced
  by the registry (UNIQUE constraints) — not just by convention.
- Every attach/detach is written to the audit log (`memory.attached`,
  `memory.detached`).
- On daemon start, legacy per-agent directories
  (`agents/<id>/memory`) are migrated to memory identities automatically
  (atomic rename; no data loss).

## Snapshots

```bash
dockweave memory snapshot codex-01     # create tar.gz snapshot
dockweave memory list codex-01         # list snapshots (newest first)
dockweave memory restore codex-01 20261023-221015.421.tar.gz
dockweave memory delete codex-01 20261023-221015.421.tar.gz
```

Properties:

- Deterministic `tar.gz` archives under `/var/lib/dockweave/snapshots/<id>/`.
- Archives are created atomically (temp file + rename).
- Restore **replaces** the memory directory; the previous state is kept as
  `<memory>.bak-<timestamp>` sibling.
- Restore is refused while the agent container runs (`agent is running`)
  — restoring under a live SQLite writer corrupts state.
- Snapshots are hardened: absolute paths and `..` entries are rejected,
  device/fifo entries are never created.

## Retention

Every snapshot creation applies the default policy (keep newest 10).
`ApplyRetention` supports `MaxCount` and `MaxAge` and is available on the
daemon for future configuration via API.

## Backup targets

Snapshots are plain files — ship them with any file-based backup
(rsync, restic, borg):

```bash
rsync -a /var/lib/dockweave/snapshots/ backup-host:/backup/dockweave/
```

## API

```
POST   /api/v1/agents/{id}/memory/snapshot
GET    /api/v1/agents/{id}/memory/snapshots
POST   /api/v1/agents/{id}/memory/restore        {"snapshot": "..."}
DELETE /api/v1/agents/{id}/memory/snapshots/{name}
```

All operations produce audit events (`memory.snapshot`,
`memory.restore`, `memory.snapshot_delete`).
