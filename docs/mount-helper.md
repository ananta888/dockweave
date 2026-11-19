# Privilege split: dockweaved and dockweave-mountd

DockWeave needs `mount(2)` to attach workspaces to running agents. Instead of
running the whole daemon as root, the privileged part is a separate process
that only accepts directory file descriptors.

## Components

| Process | User | Privileges | Responsibility |
|---|---|---|---|
| `dockweaved` | `dockweave` (+ `docker` group) | none beyond a normal user | API, DB, Docker, sessions |
| `dockweave-mountd` | root | `CAP_SYS_ADMIN` only | `mount`, `mount_setattr`, `umount` on received fds |

## Protocol

One JSON line plus optional fds via `SCM_RIGHTS` over
`/run/dockweave/mountd.sock`:

```json
{"op":"bind","flags":16385,"recursive":true}
{"op":"remount","flags":16421}
{"op":"move"}
{"op":"unmount","flags":0}
{"op":"propagation","flags":1064960}
{"op":"set_readonly","recursive":true}
```

- `bind`, `move`: two fds (source, target)
- `remount`, `unmount`, `propagation`, `set_readonly`: one fd (target)
- Targets must lie below `--root` (`/var/lib/dockweave`); the helper verifies
  this with `readlink(/proc/self/fd/N)` before the syscall.
- The socket is `0660` in group `dockweave`; `--uid` restricts the client. The
mountd unit runs with `Group=dockweave`, `RuntimeDirectoryMode=0770` and
`RuntimeDirectoryPreserve=yes`, so the unprivileged daemon (member of
`dockweave`) can keep its socket in the same runtime directory across helper
restarts.

## Deployment

```bash
install -m 0755 bin/dockweave-mountd /usr/local/bin/
install -m 0644 deployments/systemd/dockweave-mountd.service /etc/systemd/system/
install -m 0644 deployments/systemd/dockweaved.service /etc/systemd/system/
useradd --system --home /var/lib/dockweave --shell /usr/sbin/nologin dockweave
usermod -aG docker dockweave
chown -R dockweave:dockweave /var/lib/dockweave
systemctl daemon-reload
systemctl enable --now dockweave-mountd dockweaved
```

The units harden both processes (`NoNewPrivileges`,
`CapabilityBoundingSet`). The daemon unit deliberately grants **no**
capabilities; only the helper has `CAP_SYS_ADMIN`. The helper unit must **not**
use `ProtectSystem`, `PrivateTmp` or similar options: they create a private
mount namespace, and mounts made there would never reach the host or the
containers. The daemon runs as the dedicated `dockweave` user; `--uid` in the
helper unit must match that uid.

Mounts use plain `mount(2)`/`umount(2)`/`mount_setattr(2)` (no
`open_tree`/`move_mount`/`AT_EMPTY_PATH`). Both source and target are resolved
from the received fd via `readlink(/proc/self/fd/N)`; the target must lie below
the state root and, for `bind`, an inode check guards against a swapped target.
The bind *source* is re-resolved by path — a `/proc/self/fd` bind source is
rejected with `EINVAL` on the WSL2 deployment kernel, so the fd cannot be
pinned there. This leaves a small TOCTOU window on the source path (see
`docs/security-decisions.md` SD-019).

## Without the helper

If `DOCKWEAVE_MOUNTD_SOCKET` is unset, `dockweaved` falls back to the direct
root backend and refuses to start as a non-root user.

## Migrating from a root daemon

Two idempotent scripts automate the steps for an existing instance:

```bash
sudo ./scripts/migrate-unprivileged.sh /home/krusty   # system side (binaries,
                                                      # units, user, ownership)
./scripts/migrate-broker-user.sh                      # user side (broker socket)
```

`migrate-unprivileged.sh` takes the host directories the daemon must be able
to traverse (e.g. the home that contains registered workspaces) and adds
`o+x` to them. It never recurses into mounts and never grants read/write.
