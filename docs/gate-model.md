# Gate model: Paperclip and DockWeave

This document fixes the responsibility boundary between Paperclip and
DockWeave for gates, reviews and approvals. It is the reference for
`todo/archive/gate-enforcement.json`.

## Principle

- **Paperclip** owns planning: issues, parent/child hierarchy, dependencies,
  assignment, wake ordering and the board.
- **DockWeave** owns execution: container boundaries, mounts, egress,
  credentials, budgets, identity/audit and trustworthy gate execution.

A gate that needs to know *what a container did* cannot be implemented in
Paperclip alone; a gate that needs to know *what the team planned* belongs to
Paperclip.

## Responsibility split

| Concern | Paperclip | DockWeave |
|---|---|---|
| Issue hierarchy, `parentId` | yes | no |
| `blockedByIssueIds`, wake ordering | yes | consumes only |
| Issue board, comments, assignment | yes | no |
| Build/test gate definition | stored in `.paperclip/gates.json` | canonical source is the 2D graph (synced) |
| Build/test execution | no (delegates) | sandbox container |
| Mounts, read-only binds, egress, capabilities | no | yes |
| Broker credentials and LLM budgets | no | yes |
| Tamper detection of gate/test files | no | yes |
| Diff scope, dependency and secret policy | no | yes |
| Review PASS attribution | transport (comments/interactions), assignment check | audit record (admin peer), assignee stored as metadata |
| Pipeline gate → agent (admission, freshness, override) | wake/rework transport (wrapper) | edges, admission, enforcement at the endpoint, audit |
| Human approval role | transport (interaction) | OIDC role + audit |
| Audit chain | no | yes |

## What Paperclip gates cannot do

The integration used here (`gates.py`, `parent-close.py`,
`dockweave-run.sh`) has these structural limits:

1. `when` is effectively `after_run`; nothing is enforced during a run.
2. `gates.json` lives inside the writable workspace: the agent can edit its
   own checks.
3. Running the commands on the host executes agent-controlled code with the
   host user's permissions.
4. No knowledge of mounts, egress, capabilities, broker credentials or
   exclusive workspace writers.
5. No LLM budget/quota awareness.
6. `VERDICT: PASS` is free text; attribution is not enforced by Paperclip.
7. `human_only` interactions are Paperclip board users, not DockWeave roles.
8. No integrity evidence (hashes, image digests, diff scope).

## DockWeave gate execution

`dockweave gate run <workspace>` (daemon endpoint `POST /api/v1/gates/run`)
executes the workspace config in a short-lived sandbox:

- the workspace is **copied** to a private directory under
  `/var/lib/dockweave/gates`; the original is never mounted into the sandbox
- container: no network, no capabilities, `no-new-privileges`, read-only
  root filesystem with a `/tmp` tmpfs, memory/CPU/PID limits, hard timeout
- no broker socket, no Docker socket, no agent credentials
- one container per gate, logs truncated to the last 40 lines
- the image digest is recorded for reproducibility (`runner_image`)

### Phase semantics

The runner accepts `when` values `pre_run`, `after_run` and `post_run`.
Without a phase selection one `gate run` executes all phases sequentially
(`pre_run`, then `after_run`, then `post_run`). A caller can restrict a run
to explicit phases (`dockweave gate run --phase pre_run`, API field
`phases`); the run records them in `phases`. An unknown phase is a
configuration error.

The Paperclip wrapper uses real trigger points:

- `pre_run` gates run **before** the agent gets its task. A failing `pre_run`
  gate prevents the agent run (fail-closed) and re-queues the issue. Without
  `pre_run` gates the run is skipped.
- `after_run` and `post_run` gates run together **after** an agent reported
  `STATUS: DONE`, with the agent recorded as upstream.

### Retention

`dockweave gate prune` (daemon endpoint `POST /api/v1/gates/prune`, admin only)
deletes `gate_runs` older than a cutoff (default 30 days) while always keeping
the newest runs per workspace (default 20), bounding the audit table's growth.
The newest runs are ranked over *all* runs of a workspace: a run is deleted
only if it is older than the cutoff **and** not among the newest N. A
workspace without recent runs therefore keeps its newest N old runs.

### Gate expressions

A gate may carry an `expression`, routing actions `on_true`/`on_false` and
the verdict texts `reason_true`/`reason_false`. After the command has run,
the runner evaluates the expression deterministically:

| Variable | Type | Meaning |
|---|---|---|
| `command`, `exit` | int | exit code of the gate command |
| `budget` | float | the day's cost in USD from the broker usage snapshot |

Literals: numbers, `true` (1) and `false` (0). Operators: `==`, `!=`, `<`,
`<=`, `>`, `>=`, `&&`, `||`, `!`, unary `-` and parentheses; a bare value
counts as true when non-zero. Both sides of `&&`/`||` are always evaluated,
so an unavailable `budget` fails the expression even where short-circuiting
would skip it.

- Without an expression the exit code decides (0 = passed). New gates in the
  editor start without an expression; documents with the old default
  `expression: "true"` keep working.
- With an expression, **its result replaces the exit-code verdict**:
  `command != 0` can express an expected failure. `reason_true` becomes the
  reason on pass, `reason_false` on failure.
- `on_true`/`on_false` are routing actions (ADR 0002): `allow`, `deny`,
  `wake:<agent-id>`, `rework`; defaults `allow`/`deny`. A legacy free-text
  value still works as the reason (unless `reason_*` is set), the default
  action applies and the graph sync warns.
- The editor validates expressions before saving
  (`POST /graph/validate-expression`, `gateexec.Validate`) and warns when an
  expression reads `budget` without a configured budget.
- An invalid expression ends the gate with status `error`.
- `budget` is fail-closed: if no budget is configured for the workspace, or
  the broker could not be queried, an expression that references `budget`
  ends with status `error` instead of comparing against 0.
- An expression without a `command` generates no gate; the graph sync emits a
  warning.

### Budget

`budget` (`grant_id` plus `max_requests_day`/`max_tokens_day`/
`max_cost_day_usd`) is checked against the credential broker before and after
the gates. If the broker is not configured or unreachable, the run **fails**
by default. `"on_unavailable": "warn"` downgrades that case to a warning; the
usage snapshot is then marked `unavailable` and the `budget` expression
variable stays unusable.

### Tamper and policy checks

- protected files (`gates.json`, manifests, lockfiles, test configs, plus
  `protected`) are hashed before and after every gate. A change fails the run.
- after the run the original and the copy are diffed (dependency/build trees
  ignored). Optional policies:
  - `allow_paths`: changed files must match the list
  - `deny_new_dependencies`: a dependency that is new in `package.json`,
    `go.mod`, `requirements.txt`, `pyproject.toml` (PEP 621 and Poetry) or
    `Cargo.toml` (including `[dependencies.<name>]` and `target` tables)
    fails the run. Python names are PEP 503 normalised, so version bumps,
    extras and environment markers do not count as new dependencies.
  - `secret_scan`: changed files are scanned for known credential shapes
    (AWS, GitHub, OpenAI, Slack, Google, Stripe, GitLab, npm, JWT, bearer
    tokens, private keys …). If none matches, a Shannon-entropy check flags
    compact high-entropy tokens. Lockfiles (`go.sum`, `package-lock.json`,
    `yarn.lock`, `Cargo.lock`, …) and content-hash notations (`h1:`,
    `sha512-` …) are excluded from the entropy check, not from the pattern
    scan.
- every run is stored in `gate_runs` and appended to the audit hash chain
  (`gate.run`).

## Pipelines (agent A → gate → agent B)

A gate connector with a target agent (`targetAgentId`, `proceed` edge in the
2D graph) makes the target a **downstream agent**: it only takes pipeline
work while the gate's latest relevant run passed on exactly the current
workspace content. Decision record: `docs/adr/0002-pipeline-enforcement.md`.

- **Model:** the web gateway derives the edges on every save
  (`gatesync.DerivePipeline`) and pushes them with the graph revision to the
  daemon (`PUT /api/v1/pipelines`, admin). The daemon stores them in
  `pipeline_edges`, validates agents and canonical workspaces (allowed
  roots), replaces the set atomically and never lets an older revision win.
  A failed push keeps the graph and shows `pipeline_sync.error` in the UI.
- **Admission:** `GET /api/v1/pipelines/admission?agent=<id|name>` (also
  `GET /api/v1/agents/{id}/pipeline`, `dockweave pipeline status`). Every
  incoming edge must be satisfied; an agent without edges is always allowed.
- **Reasons:** `ok`, `override`, `no_run`, `failed`, `error`, `stale`,
  `expired`, `tamper`, `fingerprint_unavailable`, `unknown_gate`, `denied`,
  `internal` — each with gate id, run id and message.
- **Freshness:** every run stores a `workspace_fingerprint` (SHA-256 over
  paths and contents of the sandbox copy, same ignored trees as the diff).
  The admission recomputes it on the live workspace; any change means
  `stale`. Runs from before migration 17 have no fingerprint and are never
  fresh. Limits: 100 000 files, 2 GiB, 60 s — beyond that
  `fingerprint_unavailable` (blocked). Optional `max_age_sec` per gate.
- **Enforcement:** `POST /v1/chat/completions` and the herdr prompt check the
  admission before the agent is driven; a blocked agent gets `409` with
  `code: pipeline_blocked`, the reasons and the action. Interactive terminal
  sessions are not affected. The Paperclip wrapper asks the admission before
  each turn as well and reports a block on the issue.
- **Routing:** the admission returns the applicable action per edge (`on_true`
  when passed, `on_false` when failed, `deny` otherwise). The wrapper wakes
  released agents (`allow`, `wake:<agent>`) and opens one rework issue per
  gate run for `rework`.
- **Override:** `dockweave pipeline override <agent> --gate <id> --reason …`
  (`POST /api/v1/pipelines/override`, admin): mandatory reason, 15 minutes by
  default (max. 24 h), single use unless `--reusable`. Audited as
  `pipeline.override` and `pipeline.override_used`. Never applies to
  `tamper`.
- **Audit:** `pipeline.update` (push), `pipeline.blocked` (rate-limited to
  one event per agent, issue and reason set per minute).

## Configuration

`<workspace>/.paperclip/gates.json`:

```json
{
  "version": 1,
  "review": {"required": true, "issue_match": "review"},
  "human_approval": {"required": true},
  "runner_image": "node:22-bookworm",
  "protected": ["vitest.config.ts"],
  "allow_paths": ["src/", "tests/"],
  "deny_new_dependencies": true,
  "secret_scan": true,
  "budget": {"grant_id": "…", "max_tokens_day": 200000},
  "gates": [
    {"id": "tests", "when": "after_run", "command": "npm test", "cwd": ".", "timeout_sec": 300}
  ]
}
```

The same fields are derived from the 2D graph by `internal/gatesync`; see
`docs/paperclip-delegation.md`. A gate connector may set its own `workspace`
in the contract; the web gateway then writes one `gates.json` per target
workspace. Targets are grouped by canonical path (symlinks and different
spellings of the same directory are merged). When a save removes all gates
from a workspace that the previous graph state fed, its DockWeave-generated
`gates.json` is emptied; hand-maintained configs are never touched. Gates
without any target workspace are reported as a warning. A stable `gate_id`
in the contract keeps the gate identity across renames.

## Review and approval

- A review PASS counts only when it is attributed to the review issue's
  assigned agent or to a human board user. Unattributed board comments are
  rejected unless `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` is set explicitly.
  This assignment check happens in the Paperclip wrapper.
- Recording the verdict in DockWeave (`dockweave gate review`,
  `POST /api/v1/gates/reviews`) requires the admin role; the authenticated
  peer is stored as `actor`. The wrapper may pass the assignee
  (`--assignee` / `assigned_reviewer`); DockWeave cannot verify Paperclip's
  assignment and stores it as **unverified metadata**.
- Every accepted verdict is recorded exactly once as review evidence
  (`kind=review`, one idempotency key per issue). A later contradictory verdict
  is rejected as a conflict (`409`) and audited as `gate.review`.
- Human approval requires **both** an accepted Paperclip `human_only`
  interaction **and** a DockWeave approval:
  `dockweave gate approve <issue>` (admin role, audited as `gate.approval`).
  The Paperclip interaction remains the transport/visibility layer.

## Status semantics

- `blockedBy` (Paperclip) is a dependency edge.
- DockWeave runtime failures are **not** dependencies and must not be written
  as `blockedBy`; they belong to the gate run / agent state.
- A Paperclip `blocked` status may mean either; reports must not merge them.

## Non-goals

- DockWeave does not keep a second dependency database. Issue ordering stays
  in Paperclip.
- DockWeave does not build an issue board. It provides aggregated gate health
  and evidence only.

## Fail-closed rules

- Missing `dockweave` CLI or unreachable daemon: the wrapper reports a failed
  gate; it does **not** silently execute commands on the host. Host execution
  requires `DOCKWEAVE_GATE_HOST_FALLBACK=1`.
- Missing image: the run fails; it does not fall back to the host.
- Configured budget but broker missing or unreachable: the run fails unless
  `on_unavailable` is `warn`.
- Invalid gate expression, or an expression that needs an unavailable
  `budget`: the gate ends with status `error`.
- Tamper, scope, dependency or secret violation: the run fails even if the
  command exited 0.
- Pipeline admission: an unreadable database, missing run, missing or
  uncomputable fingerprint, unknown gate or any internal error blocks the
  downstream agent; it is never allowed by default. The wrapper treats an
  unreachable daemon or an invalid answer as blocked, and an unconfirmed gate
  result after `STATUS: DONE` keeps the issue open.
- `blocked-check.py`: a failing or unreadable Paperclip lookup counts as
  blocked unless `DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN=1` is set.
