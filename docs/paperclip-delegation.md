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
- All roles work in the same registered workspace (default
  `/workspace/crystal-run`); the wrapper repeats the workspace path and the
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
    "workspace": "/home/krusty/projects/crystal-run",
    "syncGates": true
  }
}
```

A gate contract may carry executable fields in addition to the descriptive
ones:

```json
{
  "expression": "npm run build == 0",
  "on_true": "STATUS: DONE",
  "on_false": "in_progress",
  "command": "npm run build",
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

The sync runs on the web gateway, which is the path used by the Angular app.
`PUT /api/v1/graph` on the daemon only stores the document.
