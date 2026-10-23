# Memory: Snapshots, Restore, Backup & Retention

Every agent identity owns a memory directory
(`/var/lib/dockweave/agents/<id>/memory`), bound 1:1 to that identity. It
survives container recreation, workspace changes and agent upgrades.

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
