# ADR 0003 — Ticketing as a port with exchangeable adapters (Paperclip, native)

- Status: proposed
- Date: 2026-11-22
- Decisions: D-1 … D-7 (open questions at the end)
- Tracked in: `todo/todo.ticketing-2026-11-22.json`

## Context

DockWeave wants to become a platform where AI agents and humans work on the
same tickets. Two halves exist today:

1. **Paperclip integration** – the actual ticket flow for agents. The
   Paperclip `process` adapter runs `deployments/paperclip/dockweave-run.sh`,
   which fetches the inbox item, checks the pipeline, drives the isolated agent
   through `POST /v1/chat/completions`, runs gates, writes the answer as a
   comment, maps the `STATUS:` contract to the issue status and routes
   (wake, rework, delegation). All of this is Bash/Python talking to the
   Paperclip CLI.
2. **Productivity layer** (`internal/productivity`, tab "Productivity") –
   deterministic planning for humans: objectives, tasks with `assignee_kind`
   human/agent, focus sessions, measured `work_events`, `evidence`
   (`verified` only with proof), budgets, reports, audit. Agent tasks exist
   in the model but nothing dispatches them, there are no threads, no inbox,
   no checkout and the UI hides agent tasks.

The **daemon is already ticket-system neutral**: gate runs, reviews,
approvals, the `X-DockWeave-Issue` header and pipeline admission only carry an
opaque issue id. The coupling to Paperclip lives in the wrapper scripts and in
the web gateway's CLI adapter (`internal/webui/paperclip.go`).

Paperclip already offers most of a collaboration platform (issues with
children, comments, documents, work products, interactions, approvals,
per-agent inbox, checkout, heartbeats, routines, org chart, goals, projects,
budgets, members). It does not offer DockWeave's core: container isolation,
deterministic gates with tamper detection, daemon-enforced pipeline
admission, live monitoring, file conflicts, evidence over claims.

## D-1: One port, segregated interfaces (ISP), provider-neutral core

`internal/ticketing` defines the port. Small interfaces instead of one wide
one, so an adapter implements only what its backend can do:

| Interface | Operations |
|---|---|
| `Tickets` | `Get`, `List(filter)`, `Create`, `Update(status, assignee, fields)`, `Children` |
| `Threads` | `Comments(ticket)`, `AddComment(ticket, author, body)` |
| `Inbox` | `Next(assignee)`, `Checkout(ticket, agent)`, `Release` (one worker per ticket) |
| `Approvals` | `Request`, `Decide(approve/reject/revise)` |
| `Waker` | `Wake(agent)` |
| `Provider` | `Name()`, `Health(ctx)`, `Capabilities()` + accessors returning the interfaces above or `nil` |

Canonical types (`Ticket`, `Comment`, `Assignee{kind: human|agent, id}`,
`Status`) and a canonical status set close to Paperclip's
(`backlog, todo, in_progress, in_review, blocked, done, cancelled`). Each
adapter maps its own states (Strategy). Core code depends only on the port
(DIP); new backends are added without touching it (OCP).

`TicketRef{provider, id}` replaces the bare issue id at the core boundary.
Existing ids stay valid as `paperclip:<id>` (migration, no data loss).

## D-2: Adapters

| Adapter | Backend | Notes |
|---|---|---|
| `paperclip` | Paperclip **HTTP API** (its OpenAPI), not the CLI | The CLI is fast-moving and slow (Node start per call); the API is versioned |
| `native` | DockWeave SQLite, built on the existing `tasks` table | "Paperclip style": threads, assignee human/agent, inbox, checkout, children, approvals; keeps evidence, work events, focus, budgets |
| later | GitHub Issues, Jira, Linear, … | Only `Tickets` + `Threads` required; capabilities hide the rest |

Anti-corruption layer: adapter-specific fields never leak into the core;
unknown extras stay in `Ticket.Extra` (read-only for the UI).

## D-3: Selection – explicit, not automatic switching

The daemon config names the provider (`TICKET_PROVIDER=native|paperclip`).
`auto` is only a **setup-time default** (Paperclip reachable → `paperclip`,
otherwise `native`) and is written to the config once. The provider never
flips at runtime: switching silently would split tickets across two stores.
If the selected provider is down, ticket operations fail closed with a clear
`ticketing_unavailable` and the UI shows it; agents are not driven.

A later, explicit migration command can copy native tickets to Paperclip or
back (`dockweave ticket migrate --to paperclip`).

## D-4: The worker loop moves into the daemon (Template Method)

The logic in `dockweave-run.sh`/`pipeline.py` becomes
`internal/dispatch`, a fixed sequence with provider-neutral steps:

```
next ticket → checkout → pipeline admission → pre_run gates → drive agent
→ STATUS contract → after_run/post_run gates → comment + status → routing → release
```

Routing actions (`allow`, `wake`, `deny`, `rework`) become strategies. The
Paperclip `process` adapter then calls one thin command
(`dockweave ticket run <agent>`); the native provider is driven by the
daemon's own scheduler (heartbeat per agent). Until parity is proven by the
existing wrapper tests, the current wrapper stays as the legacy path.

## D-5: Events (Observer)

Ticket changes are domain events (`ticket.created`, `ticket.status_changed`,
`ticket.comment_added`, `ticket.assigned`, `ticket.checked_out`) on the
existing audit/event bus. Consumers: audit chain, UI WebSocket, live status
in the graph (`docs/web-ui.md`), productivity work events.

## D-6: Humans are actors, not only peers

The daemon authorizes by peer UID (`SO_PEERCRED`); every web user reaches it
as the gateway's UID. For human authorship (comments, approvals, checkout)
the gateway forwards the authenticated principal (`iss`+`sub`, role) in a
header that the daemon accepts **only from the admin peer that is the
gateway**, and records it as `actor`. Monitor users stay read-only. This is a
security decision (new entry in `security-decisions.md`).

## D-7: UI

Angular gets a provider-neutral `TicketService` against
`/api/v1/tickets/*` (daemon, proxied by the gateway) and a "Tickets" view:
list/board, detail with thread, assignment human/agent, approvals, gate
status, evidence and live status per ticket. Capabilities from the provider
hide what the backend cannot do. The Paperclip panel shrinks to provider
settings. The Productivity tab stays the personal daily plan; a task can
reference a ticket.

## Consequences

- One code path for agents regardless of the backend; gates, admission,
  evidence and monitoring apply to every provider.
- Paperclip remains first-class but optional; DockWeave runs stand-alone.
- More code in the daemon (dispatch, native store); the wrapper shrinks.
- Risk: parity of the worker loop. Mitigation: port the wrapper test matrix
  (`deployments/paperclip/tests`, `tests/integration/wrapper_e2e_test.go`)
  to the Go dispatcher before switching the default.

## Open questions

1. Humans: only the owner, or several accounts with roles (author, approver,
   reader) – via Keycloak groups?
2. What may a human do on an agent ticket: assign, reply in thread, approve,
   override a gate (already exists, admin only), take over the terminal?
3. Native scheduler: heartbeat interval and concurrency per agent.
4. Should `native` also import existing Paperclip tickets once (migration) or
   start empty?
