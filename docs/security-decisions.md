# Security Decisions

This file records security-relevant architecture decisions and accepted
findings, as mandated by AGENTS.md.

## SD-001 — RO-Staging sequence for read-only binds (Phase-1)

`mount --bind -o ro` propagates the bind as RW into slave namespaces before
the RO remount applies (remount flags do not propagate). DockWeave stages
RO binds privately, remounts them read-only, then moves them into the
shared tree. Validated in `docs/validation/phase1-mounts.de.md`.

## SD-002 — No lazy unmount by default

`umount(2)` returns EBUSY while files are open; open descriptors stay
valid regardless. MNT_DETACH is only used with explicit `--force` and is
audited. For untrusted agents, revocation can stop the agent process first.

## SD-003 — Terminal stream socket (term.go)

Interactive PTY sessions use a dedicated unix socket with the same
SO_PEERCRED authorization as the HTTP API. In-band control frames use a
0x01 prefix; the attach client owns the raw tty and treats Ctrl-A as its
escape key, so user data never contains unescaped 0x01.

## SD-004 — Accepted dependency findings: GO-2026-4883, GO-2026-4887 (2026-10-23)

`govulncheck` reports two findings against `github.com/docker/docker`
v28.5.2 (`+incompatible`):

- GO-2026-4887 — Moby AuthZ plugin bypass via oversized request bodies
- GO-2026-4883 — off-by-one in Moby plugin privilege validation

Assessment and decision:

- Both vulnerabilities are in **Docker daemon (Moby) server-side code**
  (authorization plugins, plugin privilege validation). DockWeave imports
  only the Engine **client** packages; the flagged call chains are package
  `init()` functions pulled in transitively, not reachable attack surface
  in `dockweaved`/`dockweave` binaries.
- The vulnerable component that must be patched is the **Docker Engine on
  the host**, which is operator-deployed infrastructure, not shipped by
  this project.
- No fixed module version exists yet ("Fixed in: N/A"); upgrading cannot
  remediate the finding.

Decision: findings accepted for client-only usage; the CI govulncheck step
is advisory (non-blocking) until fixed releases exist. Re-evaluate when a
fixed `github.com/docker/docker` release ships; upgrade immediately then.

## SD-005 — AI monitor restricted write surface

The AI monitor agent may only submit reports via
`POST /api/v1/monitor/reports`. The endpoint validates and size-limits
payloads and appends a `monitor.report` audit event; it cannot mutate
grants, mounts or agent state.

## SD-006 — Mount source identity and fd-coupled mounts (review-001, review-004)

Two mount-manager findings from the 2026-10 review are treated as
threat-model relevant:

1. **Source identity in dedup.** `Add()` previously accepted any existing
   mount at the target as idempotent when the RO/RW mode matched, without
   comparing the bind source. A grant for project B could silently keep
   project A mounted (e.g. RW-A instead of requested RO-B). The dedup path
   now compares device+inode of the mounted root against the verified
   source fd and returns `ErrConflict` on mismatch (identity check before
   mode check).

2. **TOCTOU on the source path.** The openat2(RESOLVE_BENEATH|NO_MAGICLINKS)
   verification used to close its fd and the mount re-resolved the path
   string afterwards. `ResolveSource` now returns the open verified fd and
   the mount is performed via `/proc/self/fd/<n>`; the dedup identity check
   uses the same fd (`Fstat`). A path swap after verification can no longer
   redirect the mount. Kernels without openat2 fall back to a plain
   `O_DIRECTORY` open of the canonical path — the fd still couples
   verification and mount.

Verified by privileged tests: `TestAddSourceIdentityCheck`,
`TestFDMountIgnoresPathSwap` (path swap after verification mounts the
verified directory).

## SD-007 — Grant lifecycle states, unmount before revoke (review-002)

Grants now carry an explicit lifecycle status
(`mounting → active | mount_failed`, `revoking → revoked | unmount_failed`).
A grant is only *active* after its kernel mount succeeded; `Revoke`
unmounts **before** revoking the record and leaves a visible, retryable
`unmount_failed` state on failure — a revoked-while-mounted state is no
longer reachable. `ReconcileGrants` converges interrupted lifecycles at
daemon start. The uniqueness claim covers only pending states, so failed
creations do not block retries. Schema migration 2.

## SD-008 — Prune/force-detach policy (review-003)

`agents.Remove(prune)` previously discarded cleanup errors, detached
mounts with silent `MNT_DETACH` and deleted the registry record before
pruning. Now: errors are aggregated and returned (the agent record is kept
until clean — retryable), unmounting is graceful-first, `MNT_DETACH` is
used only with the explicit force flag and every detach is audited
(`mount.unmount_forced` event), and the workspace tree self-bind is torn
down as part of prune.

## SD-009 — Socket role authorization and non-root container default (review-005)

SO_PEERCRED only identified peers; every peer with socket access could
mutate state. A UID→role policy now gates the API: `admin` (full access),
`monitor` (read-only GET/HEAD), unknown UIDs denied. The terminal socket
requires the admin role. Config: `ADMIN_UIDS`, `MONITOR_UIDS`.

Containers previously ran as UID 0 by default (image default). Agent
creation now assigns the configured non-root identity (`AGENT_UID`/
`AGENT_GID`, default 1000:1000) and hands the host tree to it; explicit
root requires `ALLOW_ROOT_CONTAINER=true` and is audited
(`agent.identity` event). The Docker client always sets an explicit
`user` identity.

Socket access itself is layered underneath the roles: the daemon chowns
both unix sockets to `SOCKET_GROUP` (default `dockweave`, empty =
`root:root`) with mode 0660, so an unprivileged admin group can connect
without world-writable sockets. A missing group is reported at startup;
socket mode alone never grants API rights — the UID→role policy does.

## SD-010 — Recursive read-only enforcement via mount_setattr (review-007)

The privileged integration suite found that
`MS_REMOUNT|MS_BIND|MS_RDONLY|MS_REC` does not flip the read-only flag of
*nested submounts* of a recursive bind on current kernels: a rw bind nested
inside a staged source stayed rw and would have leaked write access through
the "read-only" share. The staging sequence now additionally calls
`mount_setattr(AT_RECURSIVE, MOUNT_ATTR_RDONLY)` on the staged subtree
(Linux >= 5.12). On kernels without mount_setattr the existing defensive
rw-submount check refuses the mount — failing closed, never leaking rw.
Verified by `TestROPropagationIntoSlaveNamespace` (nested submount must be
ro inside an rslave namespace; writes must fail).
