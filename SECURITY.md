# Security Model

## Threat Model

### Assumptions

1. The host kernel is trusted and up-to-date.
2. `dockweaved` runs as root with `CAP_SYS_ADMIN` and is the only component
   performing mount operations.
3. The Docker Engine API socket is accessible only to `dockweaved`.
4. Agent processes are **untrusted**: they may attempt to escape isolation,
   access unauthorized paths, or corrupt shared state.

### Adversaries

| Adversary | Goal | Mitigation |
|---|---|---|
| Compromised agent | Escape to host filesystem | Path allowlist, `openat2` TOCTOU protection, no `CAP_SYS_ADMIN`, seccomp |
| Compromised agent | Access other agents' memory | Per-agent memory directories, never shared |
| Compromised agent | Modify host mounts | Mount namespace isolation; agent mounts do not propagate to host (`rslave`) |
| Local unprivileged user | Access daemon socket | `SO_PEERCRED` authorization, socket permissions `0660` |
| Tampered audit log | Hide malicious actions | Hash chain (SHA-256), `VerifyEventChain` detects modifications |

### Non-Goals (documented limitations)

- **No FD revocation:** `umount(2)` cannot forcibly close already-open file
  descriptors. A process with an open FD retains access after unmount.
  `umount -l` (lazy) detaches the mount but does not revoke FDs.
- **No loss-free file monitoring:** The procfs provider observes open FDs and
  working directories. It does not intercept every read/write syscall.
  Tetragon (eBPF) can improve coverage but is optional and incomplete.
- **No protection against kernel exploits:** A compromised kernel bypasses
  all userspace isolation.

## Security Controls

### Mount Manager (privileged, security-critical)

1. **Read-only enforcement:** RO binds are staged privately, remounted `ro`,
   then moved into the shared tree. This avoids the propagation race where
   `mount --bind -o ro` leaks RW clones into slave namespaces.
2. **Path validation:** All source paths are canonicalized (`EvalSymlinks`),
   checked against an allowlist, and re-verified with
   `openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS)`.
3. **Target sanitization:** Mount target names must be single path components.
4. **EBUSY handling:** `umount(2)` failures with `EBUSY` are returned as typed
   errors. `MNT_DETACH` is only used with explicit `force` and is audited.
5. **Idempotency:** Duplicate mounts are detected and no-op'd.
6. **No blind cleanup:** Reconcile reports orphans but never removes mounts
   not registered to DockWeave.

### Container Hardening

- `CapDrop: ALL`
- `Privileged: false`
- `SecurityOpt: no-new-privileges`
- No `docker.sock` mount
- Seccomp: engine default profile
- CPU/memory limits configurable per agent

### API Authentication

- Unix socket with `SO_PEERCRED` peer verification
- Socket permissions `0660 root:<SOCKET_GROUP>` (default group
  `dockweave`; empty keeps `root:root`). The group is set by the daemon
  at startup; missing groups are reported, not silently ignored.
- **Authorization is role-based** (SO_PEERCRED identifies, the policy
  decides): `ADMIN_UIDS` may use the full API and terminals,
  `MONITOR_UIDS` may only read (GET/HEAD), all other UIDs are denied.
  Both lists are configured in `/etc/dockweave/dockweaved.conf`.
- No TCP exposure by default

### Audit Log

- All mutations (agent lifecycle, grants, mounts) recorded
- SHA-256 hash chain: `hash = SHA256(prev_hash | fields)`
- `VerifyEventChain` detects tampering

## Privilege Separation

```
CLI/TUI ──(unprivileged)──> dockweaved (root, CAP_SYS_ADMIN)
                                │
                    ┌───────────┼───────────┐
                    │           │           │
                SQLite     Docker API   Mount Manager
```

- CLI and TUI never perform mount operations
- Only `dockweaved` has `CAP_SYS_ADMIN`
- Agent containers have zero Linux capabilities
