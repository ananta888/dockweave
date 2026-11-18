# Privilege split: dockweaved and dockweave-mountd

DockWeave needs `mount(2)` to attach workspaces to running agents. Instead of
running the whole daemon as root, the privileged part is a separate process
that only accepts directory file descriptors.

## Components

| Process | User | Privileges | Responsibility |
|---|---|---|---|
| `dockweaved` | `dockweave` (+ `docker` group) | none beyond a normal user | API, DB, Docker, sessions |
| `dockweave-mountd` | root | `CAP_SYS_ADMIN`, `CAP_DAC_READ_SEARCH` | `mount`, `mount_setattr`, `umount` on received fds |

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
containers.

`CAP_DAC_READ_SEARCH` is needed because the helper resolves received fds to
paths (e.g. probe directories created with mode 0700) and must be able to
traverse them; it grants read/search only, no write bypass.

## Without the helper

If `DOCKWEAVE_MOUNTD_SOCKET` is unset, `dockweaved` falls back to the direct
root backend and refuses to start as a non-root user.
