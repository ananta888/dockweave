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
