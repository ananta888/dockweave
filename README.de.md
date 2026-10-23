# DockWeave — Dynamische Workspaces für isolierte KI-Agenten

DockWeave führt KI-Coding-Agenten in isolierten Docker-Containern aus, weist
jedem ein persistentes Langzeitgedächtnis zu und stellt zur Laufzeit
beliebige freigegebene Projektverzeichnisse bereit — ohne Container-Neustart.

**Sprache:** Deutsch · [English](README.md)

## Kernkonzept

Jeder Agent erhält drei Bereiche:

| Container-Pfad | Zweck | Standard (Host) |
|---|---|---|
| `/agent` | Agenteninstallation (bevorzugt read-only) | `/var/lib/dockweave/agents/<id>/agent` |
| `/memory` | Persistentes Langzeitgedächtnis (rw) | `/var/lib/dockweave/agents/<id>/memory` |
| `/workspace` | Dynamische Projektfreigaben (`rslave`) | `/var/lib/dockweave/agents/<id>/workspace` |

## Schnellstart

```bash
make build
sudo ./bin/dockweaved

dockweave workspace register meinprojekt /srv/projekte/meinprojekt
dockweave agent create codex-01 --type codex --image ghcr.io/openai/codex:latest
dockweave grant create meinprojekt codex-01 --mode rw
dockweave agent start codex-01
dockweave tui
```

## Voraussetzungen

- Linux (Kernel ≥ 5.12), Docker Engine ≥ 24.0, Go ≥ 1.24
- Root / `CAP_SYS_ADMIN` für den Daemon

Windows, macOS und Docker Desktop werden **nicht unterstützt**.

## Sicherheit

- Mount-Operationen ausschließlich im Daemon
- Read-only-Erzwingung via Staging-Sequenz (validiert)
- Pfad-Allowlisting + `openat2` TOCTOU-Schutz
- Audit-Log mit Hash-Kette
- Details: [SECURITY.md](SECURITY.md)

## Lizenz

BSD-3-Clause. Siehe [LICENSE](LICENSE).
