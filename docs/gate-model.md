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
| Review PASS attribution | transport (comments/interactions) | audit record, identity check |
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

### Tamper and policy checks

- protected files (`gates.json`, manifests, lockfiles, test configs, plus
  `protected`) are hashed before and after every gate. A change fails the run.
- after the run the original and the copy are diffed (dependency/build trees
  ignored). Optional policies:
  - `allow_paths`: changed files must match the list
  - `deny_new_dependencies`: new entries in `package.json` fail the run
  - `secret_scan`: changed files are scanned for common credential patterns
- every run is stored in `gate_runs` and appended to the audit hash chain
  (`gate.run`).

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
`docs/paperclip-delegation.md`.

## Review and approval

- A review PASS counts only when it is attributed to the review issue's
  assigned agent or to a human board user. Unattributed board comments are
  rejected unless `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` is set explicitly.
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
- Tamper, scope, dependency or secret violation: the run fails even if the
  command exited 0.
