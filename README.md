# DockWeave — Dynamic Workspaces for Isolated AI Agents

[![CI](https://github.com/ananta888/dockweave/actions/workflows/ci.yml/badge.svg)](https://github.com/ananta888/dockweave/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

DockWeave runs AI coding agents in isolated Docker containers, gives each one a
persistent long-term memory, and dynamically shares explicitly authorized
project directories at runtime — without restarting containers.

**Language:** [Deutsch](README.de.md) · English

## Why DockWeave?

- Run Codex CLI, OpenCode, Claude Code, Gemini CLI, or custom agents side by side
- Each agent gets its own `/agent`, `/memory`, and `/workspace` mount areas
- Share project directories dynamically (read-only or read-write) per agent
- Monitor which agents write which files, detect conflicts in real time
- All privileged operations isolated in a hardened daemon

## Three-Mount Architecture

| Container Path | Purpose | Host Path (default) |
|---|---|---|
| `/agent` | Agent installation (read-only by default) | `/var/lib/dockweave/agents/<id>/agent` |
| `/memory` | Persistent long-term memory (read-write) | `/var/lib/dockweave/agents/<id>/memory` |
| `/workspace` | Dynamic project shares (`rslave` propagation) | `/var/lib/dockweave/agents/<id>/workspace` |

Project directories are bind-mounted into the workspace tree at runtime.
The host `rshared` tree propagates mounts into agent containers; agent-side
mounts never propagate back to the host.

## Quick Start

### Automated install (recommended)

```bash
# One sudo prompt: build -> install -> configure -> start -> verify.
# Everything is ready to use in this very same shell afterwards.
./scripts/install.sh

# Register a workspace
dockweave workspace register myproject /srv/projects/myproject

# Create agents — examples for all supported profiles. Agents run non-root
# with HOME=/memory: npm tools install into "$HOME/.local" and therefore
# persist across container recreation.
dockweave agent create codex-01 --type codex --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @openai/codex && exec "$HOME/.local/bin/codex" --dangerously-bypass-approvals-and-sandbox'

# Claude Code (npm package @anthropic-ai/claude-code)
dockweave agent create claude-01 --type claude --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @anthropic-ai/claude-code && exec "$HOME/.local/bin/claude" --dangerously-skip-permissions'

# opencode (npm package opencode-ai)
dockweave agent create opencode-01 --type opencode --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" opencode-ai && exec "$HOME/.local/bin/opencode" --auto'

# Google Antigravity (agy) — Google's current coding agent
dockweave agent create gravity-01 --type antigravity --image node:22-bookworm \
  --exec 'curl -fsSL https://antigravity.google/cli/install.sh | bash && exec "$HOME/.local/bin/agy" --dangerously-skip-permissions'

# Gemini CLI — legacy (Google: "Migrating from Gemini CLI")
dockweave agent create gemini-01 --type gemini --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @google/gemini-cli && exec "$HOME/.local/bin/gemini" --yolo'

# API keys etc. via env file: add  --env-file FILE  (KEY=VALUE lines),
# e.g. ANTHROPIC_API_KEY=... for claude-01

# Grant read-write access
dockweave grant create myproject codex-01 --mode rw

# Start the agent
dockweave agent start codex-01

# Monitor activity
dockweave monitor activity

# Interactive TUI
dockweave tui
```

The TUI is mouse-enabled: the menu bar switches views on click, agents stay
compact — click (or `enter`) a row to expand its details. Press `t` to open
the agent terminal: inside tmux it splits the window (agent below, DockWeave
collapses to the menu bar — `esc` restores the full TUI), otherwise it
attaches full-screen.

The installer does the following (all fully reversible):

- builds and installs `dockweave`/`dockweaved` to `/usr/local/bin`
  (on `PATH` in every shell — no `command not found`)
- writes `/etc/dockweave/dockweaved.conf`: `ADMIN_UIDS` = your uid (write
  access), `SOCKET_GROUP` = your **primary group**, so the CLI connects
  immediately — **no re-login needed**
- adds you to the `dockweave` group for the future (after re-login you may
  switch `SOCKET_GROUP` to `dockweave` for shared admin setups)
- starts the daemon (systemd unit when available, otherwise background;
  logs: `/var/log/dockweaved.log` or `journalctl -u dockweaved`)
- verifies `dockweave status` as your user

### Uninstall

```bash
./scripts/uninstall.sh          # stops daemon, removes binaries + config, keeps data
./scripts/uninstall.sh --purge  # ... and deletes /var/lib/dockweave (agents, memories, snapshots)
```

The scripts also print these instructions when they finish.

### Manual installation

<details>
<summary>step by step (what the installer automates)</summary>

```bash
# Build — needs the Go toolchain (>= 1.27) on your PATH or at
# ~/.local/go/bin/go (the Makefile finds it there automatically)
make build

# Optional: install the CLI onto your PATH (~/.local/bin)
make install

# Daemon configuration: authorize your user for write access (admin role).
# Without ADMIN_UIDS only root (uid 0) may use the write API; the monitor
# role is read-only (GET/HEAD), unknown UIDs are denied entirely.
sudo groupadd -f dockweave
sudo usermod -aG dockweave "$(whoami)"   # re-login afterwards
mkdir -p /etc/dockweave
echo "ADMIN_UIDS=0:$(id -u)" | sudo tee /etc/dockweave/dockweaved.conf

# Start the daemon (requires root, CAP_SYS_ADMIN) — keep this running
sudo ./bin/dockweaved
```

The sockets are `0660 root:SOCKET_GROUP` (default group: `dockweave`).

</details>

### Troubleshooting

- `go: not found` during build: install Go ≥ 1.27 or put it at
  `~/.local/go/bin` (`export PATH="$HOME/.local/go/bin:$PATH"` and persist
  it in `~/.bashrc` / `~/.profile`) — the Makefile falls back to that path.
- `dockweave: command not found`: use `./bin/dockweave`, run `make install`,
  or install system-wide with `./scripts/install.sh`.
- `permission denied` on the socket: your shell predates the group change —
  run `newgrp dockweave` in the current shell (or re-login). With the
  installer this cannot happen (socket group = your primary group).


## Shared knowledge store (`/knowledge`)

A human-readable memory area shared by **all** agent systems (codex,
opencode, …) and by you: mounted read-write into every agent at
`/knowledge`, hosted at `/var/lib/dockweave/knowledge` (config:
`KNOWLEDGE_DIR=...`, e.g. `~/mein-wissen`). Plain markdown files with a
documented convention (see `README.md` inside the store): one file per
topic, append don't overwrite, date+source per entry. Agents deposit
facts there and look them up before assuming; you follow everything with
`ls`/`cat`/any editor. The live path shows in `dockweave agent paths`.
## Requirements

- **Linux** (Kernel ≥ 5.12, native or WSL2 with systemd)
- **Docker Engine** ≥ 24.0
- **Go** ≥ 1.27 (for building from source)
- Root / `CAP_SYS_ADMIN` for the daemon (mount operations)

Windows, macOS, and Docker Desktop are **not supported**. The daemon performs
capability detection at startup and refuses to run without required privileges.

## CLI Commands

`dockweave agent paths NAME` shows the folder assignments of an agent:
which host directory backs `/agent` (AGENTS.md, skills), `/memory`
(long-term memory) and `/workspace` (project mounts) — the TUI detail
card (click/enter on an agent) shows the same overview.

All folders are assignable in the TUI **Setup** tab (or CLI):
`dockweave agent setdir NAME PATH` registers your own settings folder as
`/agent` (empty path resets), `dockweave agent setnotes NAME PATH` sets
the per-agent knowledge folder backing `/notes`, `dockweave memory
register PATH` registers an own long-term memory folder (then `memory
attach` binds it), and projects are assigned with grants — one click per
row in Setup.

```text
dockweave agent create|start|stop|restart|remove|inspect|list
dockweave workspace register|list|unregister
dockweave grant create|list|revoke
dockweave mount list
dockweave monitor activity|conflicts
dockweave events [--follow]
dockweave status
dockweave tui
```

## API

REST API v1 available over Unix socket (`/run/dockweave/dockweaved.sock`).
See [docs/api/](docs/api/) for the OpenAPI specification.

## Security

- Mount operations only in the privileged daemon (`dockweaved`)
- Read-only enforcement via staging sequence (validated in Phase 1)
- Path allowlisting + `openat2(RESOLVE_BENEATH)` TOCTOU protection
- Agent containers: no `CAP_SYS_ADMIN`, no `docker.sock`, seccomp default
- Append-only audit log with hash chain
- [Security model](SECURITY.md)

## Documentation

- [Architecture overview](docs/architecture.md)
- [Phase-1 mount semantics validation](docs/validation/phase1-mounts.de.md)
- [Security model](SECURITY.md)
- [CLI reference](docs/cli.md)
- [Memory snapshots & backup](docs/memory-backup.md)
- [Interactive terminals](docs/terminals.md)
- [Tetragon eBPF monitoring](docs/tetragon.md)
- [MCP server](docs/mcp.md)
- [AI monitor agent](docs/ai-monitor.md)
- [Troubleshooting](docs/troubleshooting.md)

## Development

```bash
make build          # compile
make test           # unit tests
make test-race      # race detector
make test-integration  # privileged tests (in container)
make vet            # static analysis
```

## License

BSD-3-Clause. See [LICENSE](LICENSE).
