# ADR 0002 — Pipeline enforcement (Agent A → gate → Agent B)

- Status: accepted
- Date: 2026-11-21
- Decisions: D-1 (enforcement point), D-2 (meaning of `on_true`/`on_false`)
- Tracked in: `todo/archive/gate-pipeline.json` (GP-014, GP-015)

## Context

The 2D graph can wire a gate connector to a downstream agent (`proceed`
edge, `targetAgentId`). The goal: agent B only works when the deterministic
gate behind agent A passed. Up to now the edge was only drawn: the graph
lives with the web gateway (`~/.local/share/dockweave/graph.json`), the
daemon never read it, and `on_true`/`on_false` were free text that became the
verdict reason.

Paperclip drives B through `deployments/paperclip/dockweave-run.sh`, which
calls the daemon's OpenAI-compatible `POST /v1/chat/completions`
(`internal/api/openai.go`). B's container usually keeps running; Paperclip
work does not go through `agent start`.

## D-1: Where is a blocked downstream agent stopped?

| Option | Description | Assessment |
|---|---|---|
| **A** | The daemon refuses at the execution point `POST /v1/chat/completions` (and the herdr prompt). `dockweave-run.sh` additionally asks `GET /api/v1/pipelines/admission` first | Cannot be bypassed by calling the endpoint directly; the wrapper's pre-check avoids a model turn and gives Paperclip a clean message |
| B | Only the wrapper checks | Fast, but bypassed by anyone who uses the endpoint directly |
| C | Refuse at container start (`agent start`) | Ineffective: pipeline work does not go through container start |

**Decision: A.**

- **Blocked:** every programmatic prompt to an agent with incoming pipeline
  edges: `POST /v1/chat/completions` (streaming and non-streaming) and
  `POST /api/v1/agents/{id}/herdr/prompt`. The check runs after the agent is
  resolved and before anything is sent to it; a blocked request gets
  `409` with an OpenAI-compatible error object (`code`/`type`
  `pipeline_blocked`, `gate_id`, `action`, `reasons[]`) and the full
  admission.
- **Not blocked:** interactive terminal sessions (`/ws/term`, TUI, web
  terminal) — a human at the terminal is not pipeline work. Agent start/stop
  is not blocked either. Agents without incoming edges are never affected.
- **Admin override:** `POST /api/v1/pipelines/override` (admin, mandatory
  reason, short validity, single use by default, audited as
  `pipeline.override` and `pipeline.override_used`). An override never
  releases a `tamper` verdict.
- **Fail-closed:** an unreadable database, an unknown gate, a missing run, a
  missing or uncomputable workspace fingerprint, or any other internal error
  blocks with a structured reason; it never allows. The wrapper treats an
  unreachable daemon or an invalid admission answer as blocked.

### Admission rule

For agent B, every edge `gate → B` must be satisfied. Per edge the newest
*relevant* gate run of the edge workspace decides (a run that executed the
gate, a tamper run, or a run over all phases):

| Code | Meaning |
|---|---|
| `ok` | gate passed, run passed, fingerprint matches, not expired |
| `no_run` | no relevant run yet |
| `failed` | gate or run failed (includes policy violations) |
| `error` | gate or run errored (also setup errors) |
| `stale` | the workspace changed since the passing run, or the run has no fingerprint (runs before migration 17) |
| `expired` | the passing run is older than `max_age_sec` |
| `tamper` | protected gate files changed during the run (never overridable) |
| `fingerprint_unavailable` | the current fingerprint could not be computed (limits, permissions) |
| `unknown_gate` | a full run did not contain the gate (not configured) |
| `denied` | the gate passed but `on_true` does not release (`deny`) |
| `internal` | evaluator error |
| `override` | released by an active admin override |

**Freshness** is proven by a workspace fingerprint: a SHA-256 over the
sorted relative paths and content hashes of the workspace, with the same
ignored trees as the gate diff (`.git`, `node_modules`, `dist`, `target` …).
The runner fingerprints the sandbox copy before the first gate; the admission
fingerprints the live workspace. Limits (100 000 files, 2 GiB, 60 s) bound
the cost; exceeding them means `fingerprint_unavailable` (blocked).

### Data flow

1. The gateway saves the graph and derives the edges (`gatesync.DerivePipeline`).
2. It pushes them with the new graph revision: `PUT /api/v1/pipelines`
   (admin). The daemon validates agent ids and canonical workspaces under
   the allowed roots (AGENTS.md rule 4), replaces the edge set atomically and
   never lets an older revision overwrite a newer one (a recreated graph file
   is pushed with `reset`). A failed push does not lose the graph; the save
   response carries `pipeline_sync.error` and the UI shows it.
3. `dockweave-run.sh` runs A, then the gates (`gate run --upstream-agent`).
4. Before B's turn the wrapper asks the admission; the daemon enforces it
   again at the endpoint.

## D-2: What do `on_true`/`on_false` mean?

| Option | Description |
|---|---|
| **A** | Actions with a fixed vocabulary; texts move to `reason_true`/`reason_false` |
| B | Plain reason text (old state); routing only implicit via the proceed edge |

**Decision: A.**

| Action | Effect |
|---|---|
| `allow` | B may work (default for `on_true`) |
| `deny` | B stays blocked (default for `on_false`); on `on_true` the edge reports `denied` |
| `wake:<agent-id>` | allow, and the wrapper wakes the named agent in Paperclip |
| `rework` | B stays blocked; the wrapper opens exactly one rework issue for the upstream agent per gate run |

- `reason_true`/`reason_false` are the verdict reasons of the gate result.
- **Allowed combinations (2026-11-21, RF-004):** `on_true` accepts every
  action. `on_false` accepts only `deny` (default) and `rework`: a failed gate
  never releases work, so `allow` and `wake:` are rejected by the daemon API
  (400) and treated as `deny` with a warning by the graph sync. Admission
  returns action and reason for both branches, including a fresh PASS with
  `on_true=rework` or `deny` (edge code `denied`).
- **Migration:** existing contracts keep working without changes. A value
  that is not an action is treated as legacy reason text (it becomes the
  reason unless `reason_*` is set) and the default action applies; the graph
  sync reports a warning. The daemon API rejects non-action values.
- **Who executes:** the daemon only decides and returns the applicable
  action in the admission (`action` per edge and overall). Paperclip-side
  actions (`wake`, `rework`) are executed by the wrapper, which knows the
  Paperclip ids via an explicit mapping (`DOCKWEAVE_PAPERCLIP_AGENT_MAP`).

## Consequences

- New tables `pipeline_edges`, `pipeline_overrides`; `gate_runs` gains
  `workspace_fingerprint`, `upstream_agent_id`, `phases` (migrations 17/18).
- A gated agent pays one workspace fingerprint per prompt. Large workspaces
  should keep build output in ignored directories.
- The web gateway must run as an admin peer for the push; otherwise the save
  reports `pipeline_sync.error` and the daemon keeps the previous edges
  (enforcement never silently weakens because of a failed push).
- Security decision recorded as SD-020 in `docs/security-decisions.md`.
