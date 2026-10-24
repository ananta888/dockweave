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
# Bauen — benötigt die Go-Toolchain (>= 1.27) im PATH oder unter
# ~/.local/go/bin/go (das Makefile findet sie dort automatisch)
make build

# Optional, aber empfohlen: CLI dauerhaft in den PATH installieren
# (~/.local/bin — muss im PATH stehen, z. B. über ~/.profile)
make install

# Daemon-Konfiguration: eigenen User für Schreibzugriffe freigeben
# (Rolle admin) und in die Socket-Gruppe aufnehmen. Ohne ADMIN_UIDS darf
# nur root (uid 0) schreiben; die Rolle monitor ist ausschließlich lesend
# (GET/HEAD), unbekannte UIDs sind gesperrt. Die Sockets sind 0660
# root:SOCKET_GROUP (Standard-Gruppe: "dockweave").
sudo groupadd -f dockweave
sudo usermod -aG dockweave "$(whoami)"   # danach neu anmelden
mkdir -p /etc/dockweave
echo "ADMIN_UIDS=0:$(id -u)" | sudo tee /etc/dockweave/dockweaved.conf

# Daemon starten (braucht root, CAP_SYS_ADMIN) — laufen lassen
sudo ./bin/dockweaved

dockweave workspace register meinprojekt /srv/projekte/meinprojekt
dockweave agent create codex-01 --type codex --image ghcr.io/openai/codex:latest
dockweave grant create meinprojekt codex-01 --mode rw
dockweave agent start codex-01
dockweave tui
```

Falls `go` nach einer Installation unter `~/.local/go` nicht gefunden wird:
`export PATH="$HOME/.local/go/bin:$PATH"` (dauerhaft in `~/.bashrc` /
`~/.profile`) — oder einfach den Makefile-Fallback nutzen. Falls
`dockweave` nicht gefunden wird: `./bin/dockweave` statt `make install`
verwenden.

Falls die CLI `permission denied` am Socket meldet: der User ist noch
nicht in der Socket-Gruppe (`sudo usermod -aG dockweave $USER`, danach
neu anmelden) — oder als Übergangslösung ohne Gruppen-Setup:
`sudo chmod 666 /run/dockweave/dockweaved.sock` (die UID→Rollen-Policy
gilt weiterhin).

## Voraussetzungen

- Linux (Kernel ≥ 5.12), Docker Engine ≥ 24.0, Go ≥ 1.27
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
