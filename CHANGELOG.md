# Changelog

All notable changes to DockWeave are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
[SemVer](https://semver.org/).

## [Unreleased]

### Added

- Gate pipelines (ADR 0002): a gate connector with a target agent releases
  that agent only after the gate passed on the current workspace content.
  Daemon-side edges (`PUT/GET /api/v1/pipelines`), fail-closed admission
  (`GET /api/v1/pipelines/admission`, `GET /api/v1/agents/{id}/pipeline`),
  enforcement at `POST /v1/chat/completions` and the herdr prompt
  (`409 pipeline_blocked`), workspace fingerprints on gate runs, audited
  single-use admin overrides (`POST /api/v1/pipelines/override`) and the CLI
  `dockweave pipeline list|status|override|overrides`. The web gateway pushes
  the edges on every graph save and reports `pipeline_sync`.
- Gate routing actions `allow | deny | wake:<agent> | rework` for
  `on_true`/`on_false`, verdict texts in `reason_true`/`reason_false`,
  optional `max_age_sec`.
- Gate expressions: `true`/`false` literals, static validation
  (`gateexec.Validate`, `POST /graph/validate-expression`).
- Explicit gate phases: `dockweave gate run --phase …` / API `phases`; the
  Paperclip wrapper runs `pre_run` gates before the agent and
  `after_run,post_run` after `STATUS: DONE`; `--upstream-agent` records the
  checked agent.
- Paperclip wrapper: `pipeline.py` (admission precheck, 409 handling,
  wake/rework routing with an explicit agent map), configuration file
  `~/.config/dockweave/paperclip.env`, `make test-wrapper`.
- Web UI: pipeline state on proceed edges and downstream agents, routing
  editor, inline expression validation, pipeline sync errors; Playwright
  `npm run e2e:pipeline` against a mocked backend.

### Changed

- **Breaking (Go API):** `gatesync.SyncAll(raw, previous, roots)` now takes
  the previous graph document and returns `([]Result, []string)` (results
  plus document warnings).
- Gate expressions decide the verdict instead of the exit code; `budget` is
  fail-closed. Review verdicts require the admin role.
- `blocked-check.py` is fail-closed (opt-out
  `DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN=1`); the wrapper has no user- or
  project-specific default paths any more and treats an unreadable gate
  result after `STATUS: DONE` as a failure.

- `dockweave run [AGENT] [-- CMD...]`: uses the current directory as the
  project workspace, registers and grants it, starts the selected agent and
  attaches to its terminal in one step (`--no-attach`, `--mode`, `--name`).
- `dockweave agent update NAME|ID`: change an agent's type, image, command or
  uid/gid without recreating the identity (applied at the next start).
- Credential vault and API broker (MVP): `dockweave-broker` process,
  encrypted local vault (`internal/credentialvault`), DENY-by-default
  per-agent grants, short-lived sessions, fixed-destination HTTP gateway with
  credential injection, rate/budget limits, admin unix socket and
  `secret`/`credential`/`broker`/`egress` CLI commands. See
  `docs/credential-broker.md` and `docs/adr/0001-credential-broker.md`.
- Local fake-provider demo: `examples/credential-broker/demo.sh`.
- `dockweave secret migrate`: explicit-env-file migration into the vault,
  optional grants, `--dry-run`, and `--disable-env-file` (clears the agent
  reference and renames the file only after success).
- Egress profiles `offline` and `broker-only` enforced via Docker networks;
  `dockweave egress status AGENT` / `egress profile AGENT PROFILE`.
- Read-only broker metadata via daemon API (`/api/v1/broker/*`), agent egress
  status, env-file and network-profile endpoints; TUI dashboard/detail
  integration. Secrets are never exposed.
- SSH broker design (documented only): `docs/ssh-broker-design.md`.
- Angular web UI (`ui/web`) served by `dockweave web`: agent list, browser
  PTY/chat (xterm.js), per-agent configuration, project access, memory,
  broker grant metadata, an analysis dashboard and a Foblex Flow agent graph
  (organization units/roles with 2D agent figures and direct settings, plus
  resources: workspaces, memory, credential grants, knowledge). Graph
  settings add configurable ports per node kind/side, layout cleanup
  (hierarchy/columns/grid/radial/free/mixed), a JSON schema contract and a
  connection builder for custom edges. Layout templates capture current
  positions; applying a template auto-arranges new nodes and shows missing
  nodes as red dashed placeholders at their saved positions, managed by a
  right-click context menu on every node and on the canvas. Agent connectors
  (MCP server with JSON contract, deterministic gate, HTTP API, agent chain,
  custom) are configurable per agent via right-click and rendered in the
  graph. Running nodes pulse green, inactive nodes are gray and error states
  blink red. Add a normalized MCP server contract JSON schema, import from
  `mcpServers` client config or `initialize`/`tools/list` responses, and tool
  input-schema validation. Add SQLite-backed per-agent MCP server and skill
  configuration (`agent_mcp`, `agent_skills`, migration 8), API/UI management,
  and materialization into OpenCode/Claude/generic config files before start.
  Prefill every new agent with editable per-agent copies of its type
  defaults and add an apply-type-defaults action; show assigned projects in
  the per-agent connector dialog.
  Add per-agent-type default MCP servers and skills with a scanner for
  existing OpenCode/Claude/Codex/Gemini homes and an Agent Types view in the
  web UI. Add a visible node palette, deterministic gate configuration with
  condition/true/false, 10 built-in 2D role figures and sanitized custom SVG
  import per agent. Palette nodes can be dragged onto the canvas and are
  placed at the drop position; a click also places them without opening a
  dialog. Double-click opens the node-specific configuration. New nodes are
  unconnected; a `＋` button or right-click starts connection mode (choose
  type, then click the target). Edges are selectable with a delete toolbar. Hover controls on nodes
  (`+` connect, `×` remove) and edges (`+` insert junction, `×` delete). Add a selectable main agent
  with a floating bottom-right chat panel showing its chosen avatar and
  display name. Closing the chat collapses it to a floating avatar button
  that reopens the chat on click; the main agent stays selected. Node/edge
  dialogs and the settings panel are compact, anchored popovers instead of
  full-screen blockers. Mark the main agent in the graph with a golden border and
  badge; expand the figure library to 15 role SVGs. Add a UI MCP server (`/mcp/ui`) with tools to read the
  operator UI state, select views/agents/main agent and place hints at exact
  UI locations; the Angular app bridges state via `/ws/ui`. The gateway is
  unprivileged, loopback-only and token-protected; assets are embedded via
  `make web`. See `docs/web-ui.md`.

### Security

- Provider credentials can be kept out of agent env, Docker inspect, SQLite,
  `/memory` and logs by using the broker. `broker-only` blocks external
  egress with an internal Docker network (verified by a privileged test).
  Host-gateway port filtering with nftables and `restricted-development`
  remain open. See `docs/egress-security.md` and
  `todo/archive/credential-vault.json`.

## [0.1.0] — 2026-10-23

Initial release.

### Added

- Three-mount agent architecture (`/agent`, `/memory`, `/workspace` with
  rslave propagation) and hardened agent containers.
- Privileged mount manager: allowlisted sources, canonicalization,
  openat2 TOCTOU protection, RO staging sequence, EBUSY-safe unmount,
  reconcile/recovery without blind cleanup.
- Workspace registry with grants (ro/rw, exclusive, TTL) and audit hash chain.
- REST API v1 over unix socket with SO_PEERCRED auth and SSE event stream.
- CLI (`dockweave`), TUI dashboard, interactive terminals (`agent attach`,
  separate PTY socket), SDK.
- Monitoring: docker + procfs providers, activity derivation from real
  fd/cwd evidence, multi-agent conflict detection; optional Tetragon eBPF
  provider (JSONL export + PID attribution).
- Memory snapshots with restore (traversal-hardened) and retention.
- Optional MCP server (read-only tools) and AI monitor agent
  (deterministic rule-based analysis, restricted reporting API).
- CI: build/vet/test/race/govulncheck, privileged integration tests,
  release workflow with SBOM (amd64/arm64).
