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

## SD-011 — Credential broker is a separate unprivileged process (2026-11-08)

Provider credentials are moved out of agent containers into a dedicated
`dockweave-broker` process. It runs as the operator (never root), performs no
mount or Docker operations, and holds decrypted values only in process
memory. The root daemon must not grow a credential store: that would widen
`CAP_SYS_ADMIN` blast radius and put plaintext secrets next to Docker control.
See `docs/adr/0001-credential-broker.md`.

## SD-012 — Encrypted vault format and master passphrase handling

`internal/credentialvault` uses Argon2id (64 MiB, t=3, p=4) and AES-256-GCM
with a random nonce per write. Ciphertext is bound to the credential name via
AEAD additional data, preventing entry swaps. The envelope is versioned and
written atomically (`temp + fsync + rename`, `0600`, directory `0700`) under
an exclusive `flock`. The passphrase is read only from
`DOCKWEAVE_VAULT_PASSPHRASE`; it is never accepted as an argument, written to
disk, logged, or embedded in a config file. Rotating the passphrase
re-encrypts every entry under a fresh salt. This follows the MIT-licensed
crypto approach of Infisical Agent Vault `internal/crypto` (upstream commit
`16ec8d5a5db38be9d2c77fe9c19c7fd7125246ba`) with the AAD and atomic-write
hardening added here.

## SD-013 — HTTP gateway instead of MITM; fixed destinations, no redirects

The broker is an explicit gateway (`/p/{provider}/...`) over a fixed provider
base URL, not a TLS-intercepting forward proxy. This avoids installing a
custom root CA in agents and avoids a TLS-verification bypass. Agent-supplied
`Authorization`, `x-api-key`, `x-goog-api-key` and proxy auth headers are
removed before the real credential is injected. Redirects are not followed
(`CheckRedirect` returns `ErrUseLastResponse`), so a 3xx cannot carry the
injected credential to a destination that policy did not check. Path
traversal and unknown providers are rejected. Session tokens (`dws_…`, only
SHA-256 persisted, max TTL 24h) are bearer capabilities: immediate revocation
is enforced on every request, but a copied token is usable within its grant
until expiry. This limitation is tracked in the threat model and TODO.

## SD-014 — Egress enforcement is an explicit open blocker

`dockweave run` and the broker do **not** isolate network egress. A
compromised agent with bridge networking can bypass the gateway and call the
provider directly. `dockweave egress status` reports this and exits non-zero.
No network profile is presented as enforced until the `egress-*` tasks are
implemented and verified in the privileged test environment. This follows the
project rule not to pretend security exists.

## SD-015 — Egress profiles via Docker internal networks (2026-11-08)

`offline` (`--network none`) and `broker-only` are enforced by the daemon.
`broker-only` creates/uses the internal Docker network
`dockweave-broker-only`; the container has no external route. The daemon sets
`DOCKWEAVE_BROKER_BASE_URL=http://<gateway>:8787` and the operator binds the
broker to that gateway. A privileged integration test proves external HTTPS
fails with `ENETUNREACH` while the host gateway is reachable. Any failure to
create or verify the network refuses the agent start (fail closed).
`restricted-development` is intentionally not implemented and fails closed.
Residual risk: the agent can reach host services bound to the gateway;
nftables port filtering remains open (`egress-003`). This is stated in
`docs/egress-security.md` and not claimed as complete isolation.

## SD-016 — Workspace mounts use `nosymfollow` against symlink planting (2026-11-12)

**Hinweis (finding):** Ein Pentest-Agent mit `rw`-Workspace konnte einen
Symlink `flucht -> /home/<user>/.ssh` im Projektverzeichnis anlegen. Im
Container war er wirkungslos (die Containergrenze isoliert `/home`), aber da
der Workspace ein Bind-Mount eines Host-Verzeichnisses ist, lag derselbe
Symlink auch im Host-Dateisystem und löste dort auf das echte `~/.ssh` auf
(Private-Keys). Die direkte Exfiltration schlug fehl (Containergrenze hält),
aber der Symlink blieb eine „Falle“ für jeden Host-seitigen Akteur, der dem
Symlink folgt (Backup, `rsync`, `tar`, Monitoring, anderer Agent mit breiterem
Grant).

**Angriffsvektor:** „Symlink planting" / Confused Deputy. Der Agent nutzt sein
`rw`-Recht am Workspace, um einen Symlink auf einen sensiblen Host-Pfad zu
hinterlassen. Schaden entsteht nicht beim Anlegen, sondern wenn später ein
privilegierterer Akteur dem Link folgt.

**Korrektur:** Die Projekt-Mounts (`internal/mounts`) werden mit
`MS_NOSYMFOLLOW` (Linux ≥ 5.10) gebunden, sodass der Kernel Symlinks unter dem
Mount nicht mehr auflöst. Seit 2026-11-19 schlägt der Bind fehl, wenn
`MS_NOSYMFOLLOW` nicht verfügbar ist (`ErrSymlinkProtectionUnavailable`,
fail-closed statt stiller Degradation — siehe SD-019). Restrisiko:
`nosymfollow` schützt nur die Mount-Sicht; Host-seitige Werkzeuge müssen
zusätzlich vermeiden, Symlinks in Workspace-Verzeichnissen zu folgen
(dokumentiert, nicht als vollständige Isolation behauptet).

**Testfall:** `docs/validation/symlink-planting.md` — Pentest-Reproduktion,
Nachweis dass (a) die direkte Exfiltration blockiert wird und (b) der
Symlink nach der Korrektur unter dem Mount nicht mehr auflösbar ist.

## SD-017 — OAuth credential bundles and broker-side refresh (2026-11-13)

**Finding:** Existing agent logins are not always static API keys. Codex uses a
ChatGPT OAuth login (`auth.openai.com`, rotating refresh tokens,
`chatgpt.com/backend-api/codex`) and OpenCode Go needs an additional
`x-opencode-session` header. The original broker model — one static auth header
into a fixed OpenAI-compatible base URL — cannot represent either, so these
logins would have to stay in the container (the state the vault is meant to
remove).

**Decision:** The vault stores an OAuth **bundle** (`access_token`,
`refresh_token`, `expires_at`, `account_id`, `id_token`) as encrypted JSON. A
provider may declare an `oauth` block (fixed token endpoint, client id, scope,
account-id header) and an operator-defined `headers` map. Before injecting, the
broker refreshes the bundle when it is within 120 s of expiry, serializes
refreshes under a lock, persists the rotated bundle, and sends only the access
token plus derived headers upstream. The agent still only receives a `dws_…`
session token. Refresh tokens are never returned to the agent or written to the
request log.

**Fixed destinations:** the token endpoint is part of the operator-defined
provider (https only, no agent input, redirects not followed). The existing
`/p/{provider}/{path}` gateway still pins the upstream host.

**Residual risks:** (a) refresh tokens rotate, so the same login must not be
used by an independent client in parallel — two consumers invalidate each
other; use a separate login for direct terminal use or let the broker own it.
(b) A broker compromise exposes the refresh token, same as any vault secret.
(c) Provider terms of use are the operator's responsibility. (d) The bundle is
JSON in the vault; the admin API still never returns values.

**Test:** `internal/credentialbroker/oauth_test.go` (refresh, persistence,
header injection, refresh failure, static-secret compatibility) and a live
Codex CLI run against a temporary broker.

## SD-018 — Split privileges: unprivileged daemon + fd-only mount helper (2026-11-18)

**Decision:** `dockweaved` may run as a dedicated unprivileged user
(`dockweave`, member of `docker`) when `DOCKWEAVE_MOUNTD_SOCKET` is set. All
mount syscalls then go through `dockweave-mountd`, a small root helper that
accepts **directory file descriptors** (SCM_RIGHTS) and performs only
`mount`/`mount_setattr`/`umount` on `/proc/self/fd/<n>`.

**Why:** CAP_SYS_ADMIN in the initial namespace is effectively root. Splitting
the API/DB/HTTP surface from the mount capability means a compromise of the
daemon does not directly grant host mount control.

**Boundary of the helper:**
- Targets must resolve below the state root (`/var/lib/dockweave`); anything
  else is rejected before the syscall.
- Source and target are resolved from the received fd via `readlink`; targets
  must lie below the state root and, for `bind`, an inode check guards against
  a swapped target. The bind source is re-resolved by path (see SD-019 for the
  residual TOCTOU window).
- Source paths are validated by the daemon (allowlist + `openat2`), matching
  the existing TOCTOU model (SD-001, SD-016).
- The RO-staging sequence (bind → make private → remount ro → `mount_setattr`
  → move) is orchestrated by the daemon; the helper only executes the
  individual syscalls it is asked to run.
- The socket is `0660`, and `--uid` restricts the client.

**Residual risks:** (a) the helper trusts the daemon's source validation; a
compromised daemon can mount any directory it can open, but not over arbitrary
targets. (b) The Docker socket remains root-equivalent unless a socket proxy or
rootless Docker is used — this decision does not change that. (c) The helper
itself still holds CAP_SYS_ADMIN and is therefore a small, auditable attack
surface; it must stay minimal. Mounts use plain `mount(2)`/`umount(2)`/
`mount_setattr(2)`; there is no `open_tree`/`move_mount`/`AT_EMPTY_PATH` (see
SD-019).

**Fallback:** without `MOUNTD_SOCKET` the daemon keeps the direct root backend.

**Tests:** `internal/mounthelper/helper_test.go` (ping, target outside state
root rejected, unknown op rejected) plus the existing mount tests.

## SD-019 — Mount/gateway hardening round (2026-11-19)

A code review of the security-critical paths found several places where the
code fell short of its own documented contract. Fixed in this round:

1. **Helper source resolution (open_tree/move_mount).** The helper originally
   resolved the bind *source* via `readlink` (a small TOCTOU window: the path is
   re-resolved between the daemon's `openat2` verification and `mount(2)`). A
   `/proc/self/fd/<n>` bind source was rejected with `EINVAL` on the WSL2
   kernel. The helper now prefers `open_tree`/`move_mount` (pin the received
   source fd, Linux ≥ 5.2) and falls back to the readlink path where the new
   mount API is unavailable. The `open_tree` path still needs privileged
   verification on the deployment kernel.

2. **Explicit private staging.** The RO-staging sequence bound into
   `<root>/.staging` but never set `MS_PRIVATE`; privacy relied on location
   alone. The staged bind is now made private with `MS_PRIVATE|MS_REC` before
   the read-only remount (defense in depth on top of the location guarantee).

3. **`MS_NOSYMFOLLOW` fail-closed.** The symlink-planting protection (SD-016)
   silently degraded to a plain bind on kernels/filesystems without
   `MS_NOSYMFOLLOW` (log only). It now returns
   `ErrSymlinkProtectionUnavailable` and the mount fails — a hard security
   property is not silently dropped.

4. **Web gateway role enforcement.** The gateway computed OIDC roles but never
   enforced them, so a `monitor` (read-only) user had full daemon write access
   via the proxy (the daemon sees only the gateway process UID). The gateway
   now enforces: token/bearer/cookie holders are admin; an OIDC principal
   without the admin role is read-only (GET/HEAD/OPTIONS only), and
   `/api/v1/fs/dirs` plus `/ws/term` are admin-only. This closes the
   "monitor = nur lesen" gap at the web layer.

5. **Broker proxy surface (accepted limitation).** The broker forwards any
   upstream path under `/p/{provider}/...` and the model allowlist only
   applies when the body carries a `model` field. Models are enforced when
   present, but an agent could call non-LLM endpoints or omit the field.
   Restricting the surface is a design change with breakage risk for running
   agents; tracked as a follow-up (todo/archive/hardening-2026-11.json hard-007).

**Tests:** `internal/mounts/hardening_test.go` (staging sequence + fail-closed
NOSYMFOLLOW), `internal/mounthelper/helper_test.go` (fd source link),
`internal/webui/server_test.go` (role enforcement).

## SD-020 — Pipeline enforcement at the execution endpoint (2026-11-21)

**Context:** The 2D graph can release an agent B only after agent A's work
passed a deterministic gate (`proceed` edge). Until now nothing enforced it:
the graph lived with the web gateway and the daemon never read it. Paperclip
drives agents through the OpenAI-compatible endpoint, not through container
start. Full decision record: `docs/adr/0002-pipeline-enforcement.md`.

**Decision:**

1. The daemon refuses pipeline work at the execution point:
   `POST /v1/chat/completions` (streaming and non-streaming) and
   `POST /api/v1/agents/{id}/herdr/prompt` evaluate the admission after
   resolving the agent and before anything reaches it. Blocked requests get
   `409 pipeline_blocked`. Interactive terminal sessions are out of scope.
2. Fail-closed admission: missing or failed runs, `tamper`, a changed
   workspace (`stale`), an uncomputable fingerprint, unknown gates and any
   internal error block. Only agents without incoming edges are unaffected.
3. Freshness is a content fingerprint of the workspace (sorted paths and
   SHA-256 of contents; symlinks hashed by target, never followed; dependency
   and build trees ignored like the gate diff). The daemon reads the
   workspace only below the allowed roots (AGENTS.md rule 4) and within fixed
   limits (100 000 files, 2 GiB, 60 s).
4. Edges are written only by admin peers (`PUT /api/v1/pipelines`), agent ids
   and canonical workspaces are validated, the replacement is atomic and an
   older graph revision never overwrites a newer one. A failed push keeps the
   previous (stricter or equal) enforcement and is reported in the UI.
5. Admin overrides need a reason, expire (default 15 minutes, max. 24 h), are
   single use by default (consumed transactionally) and never release
   `tamper` or `denied` (a deliberate `deny`/`rework` after a fresh PASS;
   decided 2026-11-21: overrides bridge missing, failed or stale evidence only). Creation and use are audited (`pipeline.override`,
   `pipeline.override_used`); blocks are audited as `pipeline.blocked`
   (rate-limited).
6. The Paperclip wrapper additionally asks the admission before each turn
   and treats an unreachable daemon or invalid answer as blocked;
   `blocked-check.py` became fail-closed (opt-out
   `DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN=1`).

**Residual risk:** the legacy `docker exec` path of `dockweave-run.sh`
(`dockweave-<cid>` targets) bypasses the daemon and therefore the pipeline;
it is deprecated. A host user with access to the agent's interactive
terminal can still drive the agent — by design, the gate governs pipeline
work, not humans.

**Tests:** `internal/pipeline/admission_test.go` (all reason codes,
multi-edge, overrides, tamper), `internal/api/pipelines_test.go` (roles,
validation, revision guard, 409 without agent call, fail-closed),
`internal/persistence/pipelines_test.go` (atomic replace, revision guard,
concurrent override consumption), `tests/integration/pipeline_e2e_test.go`
(PASS/FAIL/STALE/override/tamper/no edge with the real sandbox runner),
`deployments/paperclip/tests/test_wrapper.py`.

## SD-021 — Rooted workspace access for gates and fingerprints (2026-11-21)

**Context:** The review of 2026-11-21 (track `review-followup-2026-11-21`,
RF-007..RF-011) found that gate preparation (`cp -a` plus `chmod -R`), host
hashing and the admission fingerprint resolved workspace paths by name. A
symlink inside an agent-writable workspace could make the root-run daemon
chmod or read files outside the workspace, and a file swapped between
`stat` and `open` could bypass size limits or hang the reader (FIFO).

**Decision:**

1. All daemon reads of a workspace (gate config, snapshot copy, protected
   hashes, policy baseline, dependency manifests, fingerprint) go through
   `internal/gateexec/rooted_fs.go`. The canonical root (callers resolve it
   with `filepath.EvalSymlinks` and the allowlist, AGENTS.md rule 4) and
   every entry are opened with `openat2(RESOLVE_BENEATH |
   RESOLVE_NO_MAGICLINKS | RESOLVE_NO_SYMLINKS)` plus `O_NOFOLLOW` relative to
   an anchored directory handle. There is no path-based fallback: kernels
   without `openat2` (< 5.6) make gate runs and admission fail closed.
2. Symlinks are never followed on the host. Fingerprints and hashes cover the
   link text. The private gate snapshot recreates internal links verbatim;
   links that are absolute or lexically leave the tree follow
   `symlink_policy` (omit or abort, see residual risk).
3. FIFOs, sockets and devices are rejected (fail closed); regular files are
   opened `O_NONBLOCK` and must be the same inode as the enumerated entry
   (`os.SameFile`). A replaced root, directory or file aborts the operation.
4. Byte limits count bytes actually read, and size/mtime/ctime are rechecked
   after reading; the fingerprint limits (entries including directories,
   bytes, time) fail closed and never grant a pass. Directories are listed in
   batches of 512 entries, so a huge directory is never loaded at once.
5. The executed gate configuration, the before/after fingerprint and the
   policy baseline all come from the same private snapshot, so a PASS is
   bound to the content that actually ran (RF-010/RF-011).

**Residual risk:**

- Timeouts are cooperative (checked between reads). A read that blocks in the
  kernel, e.g. on a stalled network or FUSE filesystem, is not interrupted.
- The snapshot escape check is lexical. Chained relative links (`y -> .`,
  `x -> y/..`) pass it; they only resolve inside the unprivileged gate
  container, which has no host mounts beyond the snapshot, never on the host.
- Escaping symlinks are configurable per workspace (`symlink_policy`,
  decided 2026-11-21): `hash` (default) omits them from the sandbox, `ignored`
  omits them only in dependency/build trees, `fail` aborts. Every mode keeps
  them out of host reads; the recorded fingerprint covers their link text,
  and the snapshot is verified against the workspace minus exactly the
  omitted links. A gate therefore never sees content behind such a link.
