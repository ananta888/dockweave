# Ticketing (ADR 0003)

DockWeave works on tickets through a provider-neutral port
(`internal/ticketing`). Adapters: `native` (DockWeave store, Paperclip style)
and `paperclip` (Paperclip HTTP API). Gates, pipeline admission, evidence and
monitoring apply to every provider. Design: `docs/adr/0003-ticketing-adapters.md`.

Status: port, both adapters, provider selection and the ticket API, forwarded
humans and permissions are implemented; live sync and the
dispatcher are implemented; Lead mode and UI follow
(`todo/todo.ticketing-2026-11-22.json`).

## Configuration (`/etc/dockweave/dockweaved.conf`)

| Key | Meaning |
|---|---|
| `TICKET_PROVIDER` | `native` or `paperclip`. Empty = ticketing off (default). |
| `TICKET_MIRROR` | optional live mirror (`native`, `paperclip`, `none`); never the primary |
| `PAPERCLIP_URL` | default `http://127.0.0.1:3100` |
| `PAPERCLIP_COMPANY` | Paperclip company id; empty = the instance's only company |
| `PAPERCLIP_TOKEN_FILE` | optional file with a Paperclip board API key (`local_trusted` needs none) |
| `GATEWAY_UIDS` | web gateway peers whose forwarded OIDC human becomes the ticket actor (SD-022) |

`dockweave ticket provider detect` proposes a value (Paperclip reachable →
`paperclip`, otherwise `native`); it never changes a running daemon.
`dockweave ticket provider` (and `GET /api/v1/tickets/provider`) shows
primary, mirror, health and capabilities. An unknown or unopenable provider is
reported there – DockWeave never falls back to another provider silently.

## API (`/api/v1/tickets`)

| Method | Path | |
|---|---|---|
| GET | `/api/v1/tickets?status=a,b&assignee=agent:<id\|name>&parent=&q=&updated_since=&limit=` | list (primary provider) |
| POST | `/api/v1/tickets` | create (`title`, `description`, `status`, `priority`, `assignee`, `parent`, `workspace_id`) |
| GET / PATCH | `/api/v1/tickets/{id}` | read / update; `expected_version` → 409 when stale |
| GET / POST | `/api/v1/tickets/{id}/comments` | append-only thread |
| POST | `/api/v1/tickets/{id}/checkout`, `/release` | `{"agent": "<id\|name>"}`; exclusive, 409 on conflict |

`{id}` is a bare id or `<provider>:<id>` of the primary provider. Agents may be
given by name; the API stores the stable id. Unknown JSON fields are 400.
Without a provider, or when it is unreachable: `503 ticketing_unavailable`
(fail closed). Monitor peers read, admin peers write; humans are checked per
permission (below). The actor is the OIDC human forwarded by the gateway
(`iss|sub`, SD-022), otherwise the socket peer (`peer:<name>`).

Every successful mutation – from the API, the dispatcher or the sync – is a
domain event (`ticket.created`, `.updated`, `.status_changed`, `.assigned`,
`.comment_added`, `.checked_out`, `.released`) in the hash-chained audit log
(`ticketing.Observe` decorator).

## Roles and permissions (D-6a)

Humans signed in via Keycloak are authorized by their **groups**:
`/etc/dockweave/ticket-roles.json` (`TICKET_ROLES_FILE`) maps groups to roles
and roles to permissions; example: `deployments/ticket-roles.example.json`.
Without the file the defaults apply (`dockweave-admin` → all,
`dockweave-monitor` → `ticket.read`). Unknown groups grant nothing.

Permissions: `ticket.read`, `ticket.create`, `ticket.edit`, `ticket.comment`,
`ticket.assign`, `ticket.approve`, `ticket.delete`, `gate.override`,
`agent.wake`, `terminal.view`, `terminal.takeover`, `provider.configure`,
`sync.run` (`"*"` = all). The daemon enforces them for every ticket call and
for pipeline overrides; a refusal is `403` with `"permission": "<name>"`.
Changing the assignee or claiming for an agent needs `ticket.assign`; other
field changes need `ticket.edit`.

The file is re-read when it changes (no restart); every load and every error
is audited (`ticket.roles_loaded` / `ticket.roles_invalid`). An invalid file
(unknown permission, undefined role, unknown key) makes all humans
**read-only** until it is fixed – it never keeps granting old rights.
`GET /api/v1/tickets/me` returns the caller's roles, permissions and a
configuration error for the UI. Socket peers without a forwarded human (CLI)
keep their peer role (`ADMIN_UIDS`/`MONITOR_UIDS`).

## Dispatcher (`internal/dispatch`, D-4)

The worker loop of `deployments/paperclip/dockweave-run.sh` and
`pipeline.py`, provider-neutral, as a fixed sequence per heartbeat:

1. next ticket of the agent (own partial work is resumed);
2. open or unreadable blockers (`blocked_by`) → re-queue, no turn;
3. pipeline admission; refused, unreachable or inconsistent → comment once per
   reason set, re-queue, no turn;
4. exclusive claim; one heartbeat per agent at a time;
5. `pre_run` gates (with a workspace) – failure → comment, re-queue, no turn;
6. the agent turn (prompt with the STATUS contract, the reviewer also with
   the VERDICT contract); a 409 from the endpoint is handled like a refused
   admission;
7. `STATUS: DONE` only counts after passing `after_run,post_run` gates
   (without a workspace it stays open); then routing: wake released
   downstream agents (`allow` / `wake:<agent>`), exactly one rework ticket per
   gate run for `rework` (never for tamper/unavailable), report `deny`;
   a routing failure never discards the verdict and names the retry;
8. `VERDICT: FAIL` → rework ticket for the author of the reviewed dependency,
   verdict mirrored into DockWeave;
9. answer + notes as comment, resulting status; the claim is kept for partial
   work, otherwise released.

Side effects (blocked comment, wake, rework creation) are idempotent through
durable markers (`dispatch_actions`, migration 22): a confirmed effect is
never repeated, an unknown outcome blocks a retry for the reconcile window
(600 s), a lost creation response is reconciled through the title tag.
`go test ./internal/dispatch` runs the ported wrapper test matrix against an
in-memory provider; `internal/api` runs the same scenario end to end for the
native provider and a Paperclip fake. The Lead/orchestration mode is TK-014.

### Running it

| | native | paperclip |
|---|---|---|
| Heartbeat | daemon scheduler every `DISPATCH_HEARTBEAT_SEC` (30) for running agents, at most `DISPATCH_CONCURRENCY` (4) at once, one per agent | Paperclip's heartbeat runs `dockweave-run.sh`; with `DOCKWEAVE_DISPATCH=daemon` it only calls `dockweave ticket run <agent>` |
| Wake (routing, rework) | scheduler kick | Paperclip `wakeup` |

`dockweave ticket run AGENT` (`POST /api/v1/tickets/run`) runs one heartbeat
(exit 3 = pipeline blocked, 1 = agent turn failed). `dockweave ticket route
TICKET --agent AGENT` retries an incomplete routing. Both need `agent.wake`
for humans.

Further `dockweaved.conf` keys: `DISPATCH_DEFAULT_WORKSPACE` (gate workspace
for tickets without a DockWeave workspace, like `DOCKWEAVE_HOST_WORKSPACE`),
`TICKET_REVIEWERS` (comma-separated agents with the VERDICT contract),
`DISPATCH_PROMPT_TIMEOUT_SEC` (240).

The driver runs the consuming pipeline admission right before the turn (as
the execution endpoint does) and shows the turn as live endpoint work in the
graph. Without `DOCKWEAVE_DISPATCH=daemon` the wrapper keeps its own logic;
the default switches only after the acceptance on a real installation (TK-015).

## Live sync (D-8, `internal/ticketing/tsync`)

With `TICKET_MIRROR` set, the daemon keeps primary and mirror in sync:

- **Links** (`ticket_links`, migration 23) pair each ticket with its mirror;
  the last synced state (`base`) enables a field-level three-way merge of
  title, description, status, priority and assignee.
- **Conflicts:** a field changed on both sides → the newer change wins, a tie
  goes to the primary; each conflict is an audit event
  `ticket.sync_conflict` with both values.
- **Comments** are append-only and merged by origin; a comment never travels
  back to the side it came from; each copy is guarded by an idempotency
  marker.
- **Triggers:** domain events (outbox, also for comment-only changes) and a
  reconcile pass every 30 s over tickets changed since the last successful
  pass. Failures are reported in the status and never block the primary.
- **Structure:** parent and `blocked_by` are translated through the links;
  DockWeave workspaces stay on the native side. A value the other side cannot
  represent (e.g. a DockWeave human as Paperclip assignee) is reported and
  left unset there.
- **Agents are driven only from the primary.**

**Initial import** of existing Paperclip tickets:

```bash
dockweave ticket sync --initial --dry-run   # plan: what would be created
dockweave ticket sync --initial             # import (idempotent)
dockweave ticket sync                       # status: linked, lag, conflicts, last report
```

Gate runs, reviews and approvals keep their keys: Paperclip tickets use the
bare issue id the wrapper always stored (`dispatch.IssueKey`), so the existing
history needs no re-keying.

## Adapters

| | native | paperclip |
|---|---|---|
| Storage | `tasks` + `ticket_comments` (migration 21) | Paperclip issues/comments |
| Identifier | `DW-<n>` | Paperclip's (`PC-12`, …) |
| Threads, inbox, checkout, children | yes | yes |
| Wake | – (scheduler, D-9) | `POST /api/agents/{id}/wakeup` |
| Authorship | DockWeave actor stored | Paperclip API identity; DockWeave author kept for comments (hidden body marker) |
| Optimistic version | integer version | digest of canonical fields + `expectedStatusVersion` |

**Paperclip agents:** a Paperclip `process` agent drives the DockWeave agent
named in `adapterConfig.args[0]` (as `dockweave-run.sh` is wired). Other
Paperclip agents appear as `paperclip-agent:<uuid>`, Paperclip users as
`paperclip-user:<id>`. Supported Paperclip versions: ≥ 2026.1106.0 (older
versions report unhealthy).

## Tests

`go test ./internal/ticketing/...` runs the shared contract
(`internal/ticketing/contracttest`) against memory, native and a Paperclip
fake. Read-only smoke against a real Paperclip:
`DOCKWEAVE_PAPERCLIP_SMOKE_URL=http://127.0.0.1:3100 go test ./internal/ticketing/paperclip -run Smoke`.
