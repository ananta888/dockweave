# Ticketing (ADR 0003)

DockWeave works on tickets through a provider-neutral port
(`internal/ticketing`). Adapters: `native` (DockWeave store, Paperclip style)
and `paperclip` (Paperclip HTTP API). Gates, pipeline admission, evidence and
monitoring apply to every provider. Design: `docs/adr/0003-ticketing-adapters.md`.

Status: port, both adapters, provider selection and the ticket API, forwarded
humans and permissions are implemented; dispatcher, live sync and UI follow
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
