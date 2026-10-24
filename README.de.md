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

### Automatische Installation (empfohlen)

```bash
# Ein sudo-Prompt: bauen -> installieren -> konfigurieren -> starten ->
# prüfen. Danach ist alles sofort in genau dieser Shell nutzbar.
./scripts/install.sh

dockweave workspace register meinprojekt /srv/projekte/meinprojekt

# Agenten anlegen — Beispiele für alle unterstützten Profile. Jedes Image
# mit der Agent-CLI funktioniert; der Daemon setzt HOME=/memory, damit
# Tool-State persistiert und die Session-Adapter (codex|opencode|claude|
# gemini) ihre Marker finden:
dockweave agent create codex-01 --type codex --image ghcr.io/openai/codex:latest

# Claude Code (npm-Paket @anthropic-ai/claude-code)
dockweave agent create claude-01 --type claude --image node:22-bookworm \
  --cmd "sh,-c,npm install -g @anthropic-ai/claude-code && exec claude"

# opencode (npm-Paket opencode-ai)
dockweave agent create opencode-01 --type opencode --image node:22-bookworm \
  --cmd "sh,-c,npm install -g opencode-ai && exec opencode"

# Gemini CLI (npm-Paket @google/gemini-cli)
dockweave agent create gemini-01 --type gemini --image node:22-bookworm \
  --cmd "sh,-c,npm install -g @google/gemini-cli && exec gemini"

# API-Keys o. Ä. über Env-File:  --env-file DATEI  (KEY=VALUE-Zeilen),
# z. B. ANTHROPIC_API_KEY=... für claude-01

dockweave grant create meinprojekt codex-01 --mode rw
dockweave agent start codex-01
dockweave tui
```

Das Skript erledigt (alles vollständig reversibel):

- baut und installiert `dockweave`/`dockweaved` nach `/usr/local/bin`
  (in jeder Shell im `PATH` — kein `command not found`)
- schreibt `/etc/dockweave/dockweaved.conf`: `ADMIN_UIDS` = deine UID
  (Schreibzugriff), `SOCKET_GROUP` = deine **primäre Gruppe**, damit die
  CLI sofort verbindet — **ohne Neuanmeldung**
- nimmt dich zusätzlich in die Gruppe `dockweave` auf (für später; nach
  Neuanmeldung kannst du `SOCKET_GROUP=dockweave` setzen, sinnvoll bei
  mehreren Admins)
- startet den Daemon (systemd-Unit falls vorhanden, sonst Hintergrund;
  Logs: `/var/log/dockweaved.log` bzw. `journalctl -u dockweaved`)
- prüft `dockweave status` als dein User

### Deinstallieren

```bash
./scripts/uninstall.sh          # Daemon stoppen, Binaries + Config entfernen, Daten bleiben
./scripts/uninstall.sh --purge  # ... und /var/lib/dockweave löschen (Agenten, Memories, Snapshots)
```

Beide Skripte geben diese Hinweise am Ende auch selbst aus.

### Manuelle Installation

<details>
<summary>Schritt für Schritt (was das Skript automatisiert)</summary>

```bash
# Bauen — benötigt die Go-Toolchain (>= 1.27) im PATH oder unter
# ~/.local/go/bin/go (das Makefile findet sie dort automatisch)
make build

# Optional: CLI dauerhaft in den PATH installieren (~/.local/bin)
make install

# Daemon-Konfiguration: eigenen User für Schreibzugriffe freigeben
# (Rolle admin). Ohne ADMIN_UIDS darf nur root schreiben; die Rolle
# monitor ist ausschließlich lesend (GET/HEAD), unbekannte UIDs gesperrt.
sudo groupadd -f dockweave
sudo usermod -aG dockweave "$(whoami)"   # danach neu anmelden
mkdir -p /etc/dockweave
echo "ADMIN_UIDS=0:$(id -u)" | sudo tee /etc/dockweave/dockweaved.conf

# Daemon starten (braucht root, CAP_SYS_ADMIN) — laufen lassen
sudo ./bin/dockweaved
```

Die Sockets sind `0660 root:SOCKET_GROUP` (Standard-Gruppe: `dockweave`).

</details>

### Problemlösung

- `go: not found` beim Bauen: Go ≥ 1.27 installieren oder nach
  `~/.local/go/bin` legen (`export PATH="$HOME/.local/go/bin:$PATH"`,
  dauerhaft in `~/.bashrc` / `~/.profile`) — das Makefile greift auf
  diesen Pfad zurück.
- `dockweave: command not found`: `./bin/dockweave` nutzen, `make install`
  ausführen oder systemweit mit `./scripts/install.sh` installieren.
- `permission denied` am Socket: die Shell stammt von vor dem Gruppen-
  wechsel — `newgrp dockweave` im aktuellen Terminal (oder neu anmelden).
  Mit dem Install-Skript kann das nicht auftreten (Socket-Gruppe = deine
  primäre Gruppe).

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
