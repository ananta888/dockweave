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
