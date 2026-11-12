# Changelog

All notable changes to DockWeave are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
[SemVer](https://semver.org/).

## [Unreleased]

### Added

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
  type, then click the target). Edges are selectable with a delete toolbar. Add a selectable main agent
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
  `todo/credential-vault.json`.

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
