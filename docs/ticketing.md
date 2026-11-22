# Ticketing (ADR 0003)

DockWeave works on tickets through a provider-neutral port
(`internal/ticketing`). Adapters: `native` (DockWeave store, Paperclip style)
and `paperclip` (Paperclip HTTP API). Gates, pipeline admission, evidence and
monitoring apply to every provider. Design: `docs/adr/0003-ticketing-adapters.md`.

Status: port, both adapters and provider selection are implemented; the
ticket API, roles, dispatcher, live sync and UI follow
(`todo/todo.ticketing-2026-11-22.json`).

## Configuration (`/etc/dockweave/dockweaved.conf`)

| Key | Meaning |
|---|---|
| `TICKET_PROVIDER` | `native` or `paperclip`. Empty = ticketing off (default). |
| `TICKET_MIRROR` | optional live mirror (`native`, `paperclip`, `none`); never the primary |
| `PAPERCLIP_URL` | default `http://127.0.0.1:3100` |
| `PAPERCLIP_COMPANY` | Paperclip company id; empty = the instance's only company |
| `PAPERCLIP_TOKEN_FILE` | optional file with a Paperclip board API key (`local_trusted` needs none) |

`dockweave ticket provider detect` proposes a value (Paperclip reachable →
`paperclip`, otherwise `native`); it never changes a running daemon.
`dockweave ticket provider` (and `GET /api/v1/tickets/provider`) shows
primary, mirror, health and capabilities. An unknown or unopenable provider is
reported there – DockWeave never falls back to another provider silently.

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
