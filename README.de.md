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

# Agenten anlegen — Beispiele für alle unterstützten Profile. Agenten
# laufen non-root mit HOME=/memory: npm-Tools installieren nach
# "$HOME/.local" und überleben damit die Container-Neuanlage.
# Codex CLI (npm-Paket @openai/codex)
dockweave agent create codex-01 --type codex --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @openai/codex && exec "$HOME/.local/bin/codex" --dangerously-bypass-approvals-and-sandbox'

# Claude Code (npm-Paket @anthropic-ai/claude-code)
dockweave agent create claude-01 --type claude --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @anthropic-ai/claude-code && exec "$HOME/.local/bin/claude" --dangerously-skip-permissions'

# opencode (npm-Paket opencode-ai)
dockweave agent create opencode-01 --type opencode --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" opencode-ai && exec "$HOME/.local/bin/opencode" --auto'

# Google Antigravity (agy) — Googles aktueller Coding-Agent
dockweave agent create gravity-01 --type antigravity --image node:22-bookworm \
  --exec 'curl -fsSL https://antigravity.google/cli/install.sh | bash && exec "$HOME/.local/bin/agy" --dangerously-skip-permissions'

# Gemini CLI — Legacy (Google: "Migrating from Gemini CLI")
dockweave agent create gemini-01 --type gemini --image node:22-bookworm \
  --exec 'npm install -g --prefix "$HOME/.local" @google/gemini-cli && exec "$HOME/.local/bin/gemini" --yolo'

# API-Keys o. Ä. über Env-File:  --env-file DATEI  (KEY=VALUE-Zeilen),
# z. B. ANTHROPIC_API_KEY=... für claude-01

dockweave grant create meinprojekt codex-01 --mode rw
dockweave agent start codex-01
dockweave tui
```

Die TUI ist mausfähig: die Menüleiste wechselt die Ansicht per Klick,
Agenten bleiben kompakt — Klick (oder `enter`) auf eine Zeile blendet die
Details ein. `t` öffnet das Agent-Terminal: innerhalb von tmux als Split
(Agent unten, DockWeave schrumpft auf die Menüleiste — `esc` stellt die
volle TUI wieder her), sonst Vollbild-Attach.

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


## Gemeinsamer Wissensspeicher (`/knowledge`)

Ein menschenlesbarer Memory-Bereich für **alle** Agentensysteme (codex,
opencode, …) und für dich: in jedem Agenten als `/knowledge` rw
eingehängt. Klartext-Markdown mit dokumentierter Konvention (siehe
`README.md` im Speicher): eine Datei pro Thema, anhängen statt
überschreiben, Datum+Quelle pro Eintrag. Der aktuelle Pfad steht in
`dockweave agent paths`.

Konfiguration (wirkt beim nächsten Agenten-Start):

```bash
# /etc/dockweave/dockweaved.conf
KNOWLEDGE_DIR=/home/krusty/mein-wissen     # Standard: /var/lib/dockweave/knowledge
# oder per Umgebung: DOCKWEAVE_KNOWLEDGE_DIR=...
```

Verdrahtung: `KNOWLEDGE_DIR` (Config) → Daemon → Read-Write-Bind-Mount
nach `/knowledge` in jeden Agenten-Container.

**Ist ein Agent-Skill nötig?** Nicht zwingend — die `README.md` im
Speicher ist die Konvention, lesbar für Menschen und Agenten. Für
verlässliche *Nutzung* (Agenten schlagen von selbst nach) ergänze
folgenden Abschnitt in die `AGENTS.md` deines Projekts (oder den
gemeinsamen `/agent`-Ordner via `[set folder]`):

```markdown
## Wissen (/knowledge)
Gemeinsamer Speicher aller Agenten. Vor Annahmen in `/knowledge`
nachschlagen (Konvention: /knowledge/README.md). Dauerhafte Fakten,
Entscheidungen und Glossareinträge dort ablegen — mit Datum + Quelle
anhängen, fremde Einträge nie überschreiben.
```
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

## Ordnerzuordnung

`dockweave agent paths NAME` zeigt die Ordnerzuordnung eines Agenten:
welches Host-Verzeichnis hinter `/agent` (AGENTS.md, Skills), `/memory`
(Langzeitgedächtnis) und `/workspace` (Projekt-Mounts) liegt — die
TUI-Detailcard (Klick/`enter` auf einen Agenten) zeigt dasselbe.

Alle drei Ordner sind im TUI-Tab **Setup** zuweis- und registrierbar
(oder per CLI): `dockweave agent setdir NAME PATH` legt einen eigenen
Settings-Ordner als `/agent` fest (leerer Pfad setzt zurück),
`dockweave memory register PATH` registriert einen eigenen
Langzeitgedächtnis-Ordner (`memory attach` bindet ihn), Projekte
werden per Grant zugeordnet — in Setup ein Klick pro Zeile.

## Lizenz

BSD-3-Clause. Siehe [LICENSE](LICENSE).
