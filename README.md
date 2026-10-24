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

```bash
# Build — needs the Go toolchain (>= 1.27) on your PATH or at
# ~/.local/go/bin/go (the Makefile finds it there automatically)
make build

# Optional but recommended: install the CLI onto your PATH
# (~/.local/bin — make sure it is on your PATH, e.g. via ~/.profile)
make install

# Daemon configuration: authorize your user for write access (admin role).
# Without this, only root (uid 0) may use the write API; the monitor role
# is read-only (GET/HEAD). Unknown UIDs are denied entirely.
mkdir -p /etc/dockweave
echo "ADMIN_UIDS=0:$(id -u)" | sudo tee /etc/dockweave/dockweaved.conf

# Start the daemon (requires root, CAP_SYS_ADMIN) — keep this running
sudo ./bin/dockweaved

# Register a workspace
dockweave workspace register myproject /srv/projects/myproject

# Create an agent
dockweave agent create codex-01 --type codex --image ghcr.io/openai/codex:latest

# Grant read-write access
dockweave grant create myproject codex-01 --mode rw

# Start the agent
dockweave agent start codex-01

# Monitor activity
dockweave monitor activity

# Interactive TUI
dockweave tui
```

If `go` is not on your `PATH` after installing it under `~/.local/go`:
`export PATH="$HOME/.local/go/bin:$PATH"` (persist it in `~/.bashrc` /
`~/.profile`) — or just rely on the Makefile fallback. If `dockweave` is
not found, use `./bin/dockweave` instead of `make install`.

## Requirements

- **Linux** (Kernel ≥ 5.12, native or WSL2 with systemd)
- **Docker Engine** ≥ 24.0
- **Go** ≥ 1.27 (for building from source)
- Root / `CAP_SYS_ADMIN` for the daemon (mount operations)

Windows, macOS, and Docker Desktop are **not supported**. The daemon performs
capability detection at startup and refuses to run without required privileges.

## CLI Commands

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
