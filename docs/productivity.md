# Productivity layer: goals, daily plan, focus, evidence

DockWeave gained an optional planning layer for humans working next to AI
agents. It is deterministic: no model call is required for any core feature.

## Design decisions

1. **Daily queue, not time blocking.** Tasks carry a `day`, an `order_index`
   and a priority, but no start time. Focus sessions measure real time.
2. **One focus session per human.** A partial unique index
   (`status IN ('active','paused')`) enforces the invariant in SQLite, so a
   second client or process cannot open a parallel session for the same human.
   Agent work is explicitly not limited by this rule.
3. **Measurements are events, not fields.** Human focus, agent runtime,
   container runtime, waits, LLM and tool time are separate `work_events` with
   `idempotency_key`. Missing values stay `unknown`; nothing is estimated.
4. **Evidence beats statements.** A task is `done` when a human completes it,
   but becomes `verified` only when a passing, `verified=true` evidence row
   exists. An agent message alone never promotes a task.
5. **Budgets are inputs.** `input_budgets` store targets per scope; elapsed
   hours or tokens are never treated as progress.
6. **Optional hierarchy.** Objectives form a flexible tree; tasks may
   reference several objectives, but the same work event is counted once.

## Schema (migration 13)

| Table | Purpose |
|---|---|
| `objectives` | vision/year/quarter/month/week/project/operative nodes |
| `tasks` | daily queue and agent orders (assignee_kind human/agent) |
| `focus_sessions` | one active/paused session per human |
| `work_events` | measured intervals/costs, unique idempotency key |
| `evidence` | commits, tests, builds, reviews, artifacts, acceptance |
| `input_budgets` | targets per scope/kind/period |
| `reviews` | weekly/monthly JSON snapshots |

## API

`/api/v1/objectives`, `/api/v1/tasks`, `/api/v1/tasks/today`,
`/api/v1/focus/{start,pause,resume,complete,skip}`, `/api/v1/work-events`,
`/api/v1/evidence`, `/api/v1/budgets`, `/api/v1/reviews`.

A second focus start returns `409 conflict`. Duplicate work events and evidence
with the same idempotency key return `200` instead of creating a second row.

## CLI

```bash
dockweave objective add "Ship v1" --kind quarter --start 2026-10-01 --end 2026-12-31
dockweave task add "Write the docs" --day today --priority 70
dockweave plan
dockweave focus start tk_123
dockweave focus pause|resume|skip
dockweave focus done --notes "shipped"
dockweave evidence add tk_123 --kind test --ref "go test ./..." --verified
dockweave budget set --scope global --kind human_hours --target 10
dockweave review add --kind weekly --start 2026-11-11 --end 2026-11-18 --body '{"wins":3}'
```

## Security

- No new mounts, grants or container privileges are created by this layer.
- Objective data is only returned by the authenticated API; agents see it only
  through the same API with their existing role.
- Events and reviews contain no secrets by construction; the CLI accepts only
  the documented fields.
- Reviews and plans are data, not instructions: the system never executes text
  from a review or objective.

## Open items

See `todo/productivity.json`: TUI focus tab, ingestion of monitor/supervisor
events, budget actuals, calendar logbook, optional ranking, Ananta contracts.

## Reporting, ranking and export (implemented)

- `GET /api/v1/work-events/derived` derives container runtime from the audit
  log (`agent.started`/`agent.stopped`). Nothing is stored twice.
- `POST /api/v1/work-events/batch` ingests monitor measurements idempotently.
- `GET /api/v1/budgets/actuals` compares targets with measured events and
  reports verified results separately.
- `GET /api/v1/tasks/suggest?day=` ranks the queue deterministically and
  explains every score.
- `GET /api/v1/calendar` returns a logbook grouped by day, filterable by human
  and agent.
- `GET /api/v1/reviews/report` builds the weekly/monthly report without an LLM.
- `GET /api/v1/export/productivity` returns the versioned contract bundle
  (`docs/api/productivity.schema.json`); export is read-only.
- TUI tab **Focus**: current session, invested time, queue, keys
  `s/p/c/d/x` for start/pause/continue/done/skip.
- Secret-looking keys (`token`, `secret`, `password`, `api_key`, ...) are
  rejected in event metadata and evidence payloads.

## Rollover, switch, retention and audit

- `POST /api/v1/tasks/rollover` moves unfinished human tasks to the next day;
  agent orders and finished tasks stay untouched.
- `POST /api/v1/focus/switch` skips the current session, returns the old task
  to the queue and starts the new one.
- `POST /api/v1/productivity/prune?before=YYYY-MM-DD` deletes old measurements
  (by `started_at`), evidence, reviews and closed focus sessions.
- Every mutation writes a hash-chained audit event (`objective.created`,
  `task.created`, `task.updated`, `focus.started`, `budget.updated`,
  `review.created`, `productivity.pruned`).
- `dockweave evidence git TASK` attaches the current HEAD commit idempotently.

## Angular UI

The **Productivity** tab in the web app shows the daily plan, the active focus
session with live elapsed time, task and objective creation, budget actuals, a
deterministic review report and a rollover button. It calls the same
`/api/v1` endpoints as the CLI.
