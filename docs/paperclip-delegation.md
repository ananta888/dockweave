# Goal-driven delegation with Paperclip

DockWeave runs the Paperclip `process` adapter through
`deployments/paperclip/dockweave-run.sh`. Two modes exist.

## Worker mode

```
dockweave-run.sh <dockweave-agent>          # e.g. demo-opencode
```

1. The wrapper reads the first assigned inbox item (`/api/agents/me/inbox-lite`).
2. The issue identifier, title and full description are sent to the DockWeave
   OpenAI-compatible endpoint (`POST /v1/chat/completions` over the daemon
   unix socket), so the run is visible in the live agent TUI.
3. The answer is appended as an issue comment. The issue status comes from the
   `STATUS:` contract (see below); without it the issue stays `in_progress`.

### STATUS contract

Every worker prompt ends with:

```
STATUS: DONE        - implemented and verified
STATUS: BLOCKED     - needs a decision or missing permissions
STATUS: IN_PROGRESS - partial work, rest open
```

A run therefore never marks partial work as done.

## Orchestration mode (Lead / CEO)

```
dockweave-run.sh lead orchestrate
```

1. Same inbox fetch, but the prompt asks for a JSON work breakdown only:

   ```json
   {"tasks": [{"title": "...", "description": "...", "role": "backend", "depends_on": []}]}
   ```

2. `plan-extract.py` extracts the plan (fenced or bare JSON, prose tolerant).
3. `plan-delegate.py` creates one child issue per task with the Lead issue's
   company, parent, goal and project, assigns the role agent from
   `~/.config/dockweave/paperclip-roles.json`, appends the shared workspace and
   dependency list, and wakes the agent.
4. Re-running the Lead heartbeat is idempotent: existing children with the same
   title are reused instead of duplicated.
5. The parent issue is left `in_progress` with a comment listing the children.

## Role mapping

`deployments/paperclip/roles.example.json` documents the file format:

```json
{ "backend": "<paperclip-agent-id>", "frontend": "...", "reviewer": "...", "platform": "..." }
```

## Operational rules

- One run per agent (`maxConcurrentRuns: 1`). Role agents share one native
  session, so parallel runs of the same agent would interleave answers.
- Agent run keys are scoped to the running agent; waking another agent falls
  back to the local board identity.
- All roles work in the same registered workspace
  (`DOCKWEAVE_PAPERCLIP_WORKSPACE`, default `/workspace`); the wrapper repeats the workspace path and the
  "no parallel writes to the same file" rule in every child issue.

## Gates

The wrapper enforces four gates before an issue counts as done:

1. **Dependencies** - `plan-delegate.py` writes real `blockedByIssueIds` edges;
   `wake-ready.py` only wakes issues whose blockers are done.
2. **Build/test** - `gates.py` reads `<workspace>/.paperclip/gates.json` and runs
   the project command. A failed gate turns `STATUS: DONE` into `in_progress`
   with the command output as comment.
3. **Review** - the reviewer answers with `VERDICT: PASS|FAIL`; `FAIL` opens a
   rework issue for the author of the first dependency.
4. **Human approval** - `parent-close.py` closes a Lead issue only when all
   children are done, the review has `VERDICT: PASS` and an accepted
   `human_only` interaction exists. Otherwise the parent stays `in_review` with
   a pending card in the Paperclip inbox.

### Execution, attribution and approval

Gates are executed by `dockweave gate run <workspace>` in a short-lived
sandbox container, not on the host (see `docs/gate-model.md`). The wrapper
reports a failed gate when the daemon is unreachable; host execution is only
possible with `DOCKWEAVE_GATE_HOST_FALLBACK=1`.

- A review PASS counts only when it is attributed to the review issue's
  assigned agent or to a human board user. Unattributed comments are rejected
  unless `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` is set.
- Human approval requires an accepted Paperclip `human_only` interaction **and**
  `dockweave gate approve <issue>` (admin role, audited as `gate.approval`).

The 2D graph in the Angular app stores the same gate contracts. Previously it
lived in `localStorage`; `GET/PUT /graph` on the web gateway persists the whole
document in `~/.local/share/dockweave/graph.json`, and the daemon exposes the
same document as `GET/PUT /api/v1/graph` for non-browser clients.

## Graph → Paperclip gate sync

Enabled `kind: "gate"` connectors can be mirrored into the project workspace.
The graph document carries a `paperclip` block:

```json
{
  "paperclip": {
    "workspace": "/srv/projects/app",
    "syncGates": true
  }
}
```

A gate contract may carry executable fields in addition to the descriptive
ones:

```json
{
  "gate_id": "build",
  "command": "npm run build",
  "expression": "command == 0",
  "on_true": "allow",
  "on_false": "rework",
  "reason_true": "build green",
  "reason_false": "build red, please fix",
  "max_age_sec": 3600,
  "cwd": ".",
  "timeout_sec": 300,
  "review_required": true,
  "human_approval_required": true
}
```

On every `PUT /graph` the web gateway calls `internal/gatesync` and writes
`<workspace>/.paperclip/gates.json` atomically:

- `command` becomes an entry in `gates[]` (`when` defaults to `after_run`,
  `cwd` to `.`, `timeout_sec` to 300)
- `review_required` sets `review.required` (default `issue_match: "review"`)
- `human_approval_required` sets `human_approval.required`

Safety rules:

- writes are limited to the admin browser roots (`/home`, `/srv`,
  `/opt/projects`, `/var/lib/dockweave`); the workspace must be an existing
  absolute directory
- only `<workspace>/.paperclip/gates.json` is written, atomically
- a hand-maintained file is never replaced when the graph has no executable
  gates; the first sync with executable gates **merges** into it (gates the
  graph does not define are kept, graph entries win on the same id); files
  written by DockWeave (`generatedBy: "dockweave-graph"`) are cleared when the
  last gate is removed
- a failed sync never fails the graph save: the result is returned as `gates`
  in the `PUT /graph` response and shown in the UI
- the same save derives the pipeline edges and pushes them to the daemon; the
  outcome is returned as `pipeline_sync` (`pushed`, `edges`, `warnings`,
  `error`)

The sync runs on the web gateway, which is the path used by the Angular app.
`PUT /api/v1/graph` on the daemon only stores the document.

## Pipeline: agent A → gate → agent B

A gate connector with a target agent (`targetAgentId`, drawn as a `proceed`
edge) releases agent B only after agent A's work passed the gate on the
current workspace content (ADR 0002, `docs/gate-model.md` "Pipelines").

1. **A works.** Before every turn `dockweave-run.sh` runs `pre_run` gates
   (if any) and asks `pipeline.py precheck <agent>`; A has no incoming edge
   and is allowed.
2. **A reports `STATUS: DONE`.** The wrapper runs the `after_run`/`post_run`
   gates in the sandbox with A as upstream (`gates.py run … --phase
   after_run,post_run`, `DOCKWEAVE_UPSTREAM_AGENT`). The run stores the
   workspace fingerprint.
3. **Routing.** `pipeline.py route --upstream <A>` evaluates the admission of
   every downstream agent of A:
   - allowed (`allow`) → wake B in Paperclip; `wake:<agent>` wakes the named
     agent instead
   - failed with `rework` → exactly one rework issue for A per gate run
     (title tag `[gate <id> / run <run-id>]`, idempotent)
   - `deny` or another reason (`stale`, `no_run` …) → reported in the comment,
     nobody is woken
4. **B is woken.** Its wrapper asks the admission first; if the workspace
   changed since the PASS (`stale`) or the gate failed, B is reported as
   blocked on its issue (once per reason set; the issue goes back to `todo`,
   never `blockedBy`) and no model turn is spent. If the precheck is skipped,
   the daemon still answers `409 pipeline_blocked` and the wrapper handles it
   the same way.

The Paperclip ↔ DockWeave agent mapping for waking is explicit:
`DOCKWEAVE_PAPERCLIP_AGENT_MAP` points to a JSON file
`{"<dockweave-agent-id-or-name>": "<paperclip-agent-id>"}`. Unmapped agents
are reported as `UNMAPPED` and never guessed.

Admin escape hatch: `dockweave pipeline override <B> --gate <id> --reason …`
(single use, 15 minutes, audited). Overrides bridge missing, failed or stale
evidence only; `tamper` and a deliberate `deny`/`rework` after a PASS
(`denied`) are never released.

### Protection per wrapper mode

Which steps `dockweave-run.sh` actually runs depends on its first two
arguments. Tests: `deployments/paperclip/tests/test_wrapper_modes.py`.

| Step | Worker (`<agent>`) | Lead (`<agent> orchestrate`) | Legacy (`dockweave-<id> …`) |
|---|---|---|---|
| Open blockers (`blocked-check.py`) | yes | no | yes |
| Pipeline admission (`pipeline.py precheck`) | yes | yes | **no** |
| `pre_run` gates | yes, if `DOCKWEAVE_HOST_WORKSPACE` is set | no | **no** |
| Daemon enforcement at the endpoint (`409`) | yes | yes | **no** (`docker exec` bypasses the daemon) |
| `after_run`/`post_run` gates on `STATUS: DONE` | yes; without `DOCKWEAVE_HOST_WORKSPACE` the gate fails and the issue stays open | no (children are gated) | **no** |
| Pipeline routing | yes, if `DOCKWEAVE_HOST_WORKSPACE` is set; a routing failure keeps the gate verdict and adds a retry command to the issue | no | **no** |

The legacy path is **not pipeline-protected** and prints a warning on every
run. It is kept only for compatibility. Migration: the container name is
`dockweave-<agent-id>`, so replace the first argument `dockweave-<agent-id>`
with `<agent-id>` (or the agent's name) and drop the harness arguments; the
agent is then driven through the endpoint like any worker.

## Daemon dispatcher (ADR 0003)

With `TICKET_PROVIDER=paperclip` in `dockweaved.conf` and
`DOCKWEAVE_DISPATCH=daemon` in the wrapper environment, `dockweave-run.sh`
only calls `dockweave ticket run <agent>`; the worker loop described here runs
in the daemon (`docs/ticketing.md`). Orchestration mode keeps the script
logic until TK-014.
