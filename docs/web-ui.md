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
# prints: DockWeave web UI: http://127.0.0.1:8788/?token=…
```

Open the printed URL. The token is stored as an HttpOnly cookie; subsequent
requests work without it in the URL. The gateway is **loopback-only by
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
| Graph | Foblex Flow graph in two modes: **Organization** (units → agents as 2D figures with role labels, editable in a side inspector) and **Resources** (workspaces, memory, credentials, knowledge) |
| Workspaces | Register/unregister host directories |

Secret values are never shown. Creating/rotating secrets and credential
grants stays on the `dockweave secret` / `credential` CLI (broker admin
socket).

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
