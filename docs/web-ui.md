# Web UI (Angular)

DockWeave ships a browser UI for every agent: agent list on the left, chat /
terminal, per-agent configuration, project access, memory, broker grants and
an analysis dashboard.

Language: English · [Deutsch](web-ui.de.md)

## Start

```bash
make web          # build the Angular app and embed it into the Go binary
make build
./bin/dockweave web
# prints: DockWeave web UI: http://127.0.0.1:8788/
#         Login-Token: …
```

Open the printed URL and enter the Login-Token in the login page. The
token is stored as an HttpOnly cookie; subsequent requests work without it
in the URL or history. API clients can send `Authorization: Bearer <token>`
instead of a cookie. Failed logins are rate limited (5/minute per address),
and every response carries `Cache-Control: no-store`, `X-Frame-Options: DENY`,
`X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. The gateway is **loopback-only by
default** and runs unprivileged with your UID.

Useful flags:

```bash
dockweave web --listen 127.0.0.1:8788 --open
dockweave web --web-dir ui/web/dist/dockweave-web/browser  # serve a dev build
DOCKWEAVE_WEB_TOKEN=… dockweave web                        # fixed token
```

## Features

| Area | What you can do |
|---|---|
| Agent list | All agents with live state, conflict warning, create agent |
| Chat / Terminal | Full PTY in the browser (xterm.js) using the agent's own TUI; prompt box writes to the TUI |
| Configuration | Type/image/argv, UID/GID, egress profile, env file, `/agent` and `/notes` folders, start/stop/restart/remove |
| Project access | Assign/unassign registered workspaces (ro/rw) |
| Memory | Attach/detach memory identity, create/restore/delete snapshots |
| Credentials | View broker credential grants (metadata only) |
| Analysis | Activity/CPU/memory, file conflicts, events, broker traffic, provider status |
| Graph | Foblex Flow graph in two modes: **Organization** (units → agents as 2D figures with role labels, editable in a side inspector) and **Resources** (workspaces, memory, credentials, knowledge). Ports per node kind and side are configurable; layout can be cleaned with hierarchy/columns/grid/radial/free/mixed algorithms; connection builder, JSON schema contract, layout templates/placeholders and agent connectors (MCP/gate/HTTP/agent chain) are included. Running nodes pulse green, inactive nodes are gray, errors blink red |
| Workspaces | Register/unregister host directories |

Secret values are never shown. Creating/rotating secrets and credential
grants stays on the `dockweave secret` / `credential` CLI (broker admin
socket).

### Gates and pipelines in the graph

- **Gate editor** (connector dialog and node popover): optional expression
  (empty = the exit code decides), checked inline against
  `POST /graph/validate-expression`; an invalid expression cannot be saved,
  and an expression that reads `budget` without a configured budget shows a
  warning. Routing is a choice, not free text: on PASS `allow`,
  `wake` (with an agent picker) or `deny`; on FAIL `deny` or `rework`; plus
  `reason_true`/`reason_false` and an optional maximum PASS age. Older free
  text opens as the reason with the default action.
- **proceed edge** (gate → target agent) shows the pipeline state from
  `GET /api/v1/pipelines/admission`: green `✓ ok`/`override`, red
  `✕ failed`/`error`/`tamper`/`denied`, amber dashed `stale`/`expired`/
  `no_run`/`unknown_gate`, and `nicht übertragen` when the daemon does not
  know the edge yet. The label is keyboard-focusable; its tooltip and
  accessible name list the expression, reason code, last run id and age.
- **Downstream agents** carry a `Pipeline frei` / `⛔ blockiert` badge with a
  text alternative naming the blocking gates. The status refreshes every 15
  seconds and after every save, without a reload.
- **Sync errors:** the save response's `pipeline_sync.error` appears as a
  banner on the canvas and in the settings section "Pipeline (Gate → Agent)",
  together with derivation warnings. A gate with a target agent but no shell
  command is marked with a warning (the target would stay blocked).

UI test without a daemon: `cd ui/web && npm run build && npm run e2e:pipeline`
(Playwright, mocked backend).

### Live status in the graph

Nodes and edges show what is happening right now, derived only from real
data (monitor activity, `GET /api/v1/conflicts`, `GET /api/v1/live`, pipeline
admission, gate runs and reviews). Unknown is never shown as green.

| Colour / rhythm | State | Source |
|---|---|---|
| green, calm pulse (2.5 s) | agent works (reading/writing or CPU ≥ 10 %) | monitor |
| green | endpoint job running (agent) | `/api/v1/live` `endpoint_job` |
| green | gate run in flight: checked agent, agent → gate edge, gate | `/api/v1/live` `gate_run` |
| green | pipeline flow: gate → agent edge, gate and agent while the agent works on a released edge or ≤ 60 s after the release | admission + live |
| green | two active agents on a shared workspace (grant edges, workspace node) or on an agent → agent edge (talking) | grants, monitor |
| blue | agent starting/stopping (`created`, `restarting`, `removing`) | agent state |
| yellow, slow | override active (edge + gate) · review pending (gate) | admission · gate runs/reviews |
| grey dashed | graph and enforced pipeline not in sync | revision drift |
| red, slow (2 s) | blocked: `no_run`, `stale`, `expired`, `denied` | admission |
| red, medium (1 s) | `failed`, `error`, `internal`, `unknown_gate`, `fingerprint_unavailable`, status unknown, daemon unreachable, container `dead` | admission, shell refresh |
| red, hard blink (0.4 s) | `tamper`, file conflict between agents | admission, conflicts |

The most severe state wins. Every marked node has a symbol dot
(● ◐ ⏳ ⚑ ⇄ ! ⚠ ✖) and a tooltip / accessible name with the reasons; edge
labels carry the same symbol and text. With *reduce motion* in the OS only
colour and symbol remain. A merely `running` agent no longer pulses.

`GET /api/v1/live` (admin + monitor, read-only) lists the endpoint jobs and
gate runs the daemon executes right now; entries disappear when the request
ends and are kept in memory only. Requests refused by pipeline admission
never appear.

UI test: `npm run e2e:pipeline` includes `e2e/live-status.spec.ts`.

## Architecture

```
browser ──HTTP/WebSocket──> dockweave web (unprivileged, loopback, token)
                              ├── /api/*  ──reverse proxy──> daemon unix socket
                              ├── /ws/term ──bridge──> term unix socket (PTY)
                              └── /       ──embedded Angular assets
```

The gateway does not implement privileged operations itself. It only forwards
to the daemon with your peer credentials, so the daemon's `SO_PEERCRED`
authorization still applies.

## Security notes

- **LAN access:** the gateway is loopback-only by design; to reach it from
  other machines use the TLS proxy in `deployments/lan-access` (README), not
  `--listen 0.0.0.0`.
- Listener defaults to `127.0.0.1`; binding elsewhere prints a warning.
- Every request except `/favicon.ico` needs the random token cookie. The
  token is equivalent to your admin access — do not share the URL.
- The browser UI can perform admin actions because the gateway runs as you.
  Treat the process like an admin session.
- No secrets are available through the daemon API; the UI only shows
  metadata and terminal output.
- For remote access, put the gateway behind an authenticated reverse proxy
  and TLS; do not expose it directly.

## Development

```bash
cd ui/web
npm ci
npm start            # Angular dev server (needs a proxy to the gateway)
npm run build        # production build
```

The Go binary embeds `internal/webui/dist`; `make web` rebuilds and copies
the Angular output before `make build`.
