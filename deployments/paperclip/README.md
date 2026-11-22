# paperclip ↔ DockWeave Integration

Verschachteltes Modell: **paperclip** (Orchestrierung, extern) treibt einen
Agenten, der **in der DockWeave-Docker-Sandbox** läuft. Der Container bleibt
die Sicherheitsgrenze; paperclip entscheidet, *wer was wann* macht.

## Wie es verdrahtet ist

paperclip → `process`-Adapter → `dockweave-run.sh` → `docker exec` → Container.

`dockweave-run.sh` ist der Entrypoint des `process`-Adapters. paperclip
injiziert die `PAPERCLIP_*`-Umgebung (Agent-/Company-/Run-ID, API-URL + Key),
übergibt den Task aber **nicht** direkt — der Prozess holt ihn selbst:

1. `GET /api/agents/me/inbox-lite` (mit `PAPERCLIP_API_KEY`) → erster zugewiesener Task.
2. `docker exec -i <container> <harness> "<task>"` → Ausführung in der Sandbox.

## Setup (Kurzfassung)

```bash
# 1. paperclip installieren + starten
paperclipai install --yes
paperclipai onboard --yes
paperclipai service install --no-start-on-login

# 2. Company + Agent mit process-Adapter
CID=$(paperclipai company create --payload-json '{"name":"DockWeave Demo"}' --json | jq -r .id)
AG=$(paperclipai agent create --company-id "$CID" --payload-json \
  '{"name":"DockWeave-Sandbox-Agent","adapterType":"process","adapterConfig":{
     "command":"/path/to/dockweave-run.sh","args":["<dockweave-container>"],
     "cwd":"/path/to/dockweave","timeoutSec":60}}' --json | jq -r .id)

# 3. Task zuweisen + wecken
paperclipai issue create --company-id "$CID" --title "…" --description "…" \
  --assignee-agent-id "$AG"
paperclipai agent wake "$AG" --company-id "$CID" --reason "…"
```

## DockWeave Engineering team

The current Paperclip company is **DockWeave Engineering**. Assignees map to
DockWeave agents (not 1:1 to containers — Paperclip agents are the process
bridge):

| Paperclip assignee | Role | DockWeave target | Runtime / model |
|---|---|---|---|
| Lead | Lead / Architect | `developer` | codex (ChatGPT OAuth) |
| Backend | Backend Developer | `demo-opencode` | opencode (DeepSeek) |
| Frontend | Frontend Developer | `researcher` | opencode (DeepSeek) |
| Reviewer | Reviewer / QA | `claude-test` | Claude |
| Platform | Platform / DevOps | `herdr-codex` | herdr + codex |

DockWeave display names mirror the roles. The old demo agents (`demo-codex`,
`reviewer`, `DockWeave-Sandbox-Agent`, `DW-*`) were removed.

## Standardmodus: DockWeave-Endpoint

`dockweave-run.sh` ruft standardmäßig den OpenAI-kompatiblen Endpoint des
DockWeave-Daemons über den Unix-Socket auf:

```
paperclip → dockweave-run.sh → POST /v1/chat/completions (unix socket)
          → native Harness-API oder TUI-Fallback → Antwort
```

- Kein Web-Token nötig: der Daemon autorisiert den Socket-Peer per
  `SO_PEERCRED`.
- Der Task bleibt in der **live TUI** sichtbar (Mode 2), weil der Endpoint
  dieselbe Session treibt.
- Der Wrapper gibt die Antwort aus; paperclip speichert sie als Run-Output.
- Best effort: die Antwort wird zusätzlich als Kommentar am Issue abgelegt.
  Der Wrapper setzt das Issue danach auf `DOCKWEAVE_PAPERCLIP_STATUS` (Default `done`) und hängt die Antwort als Kommentar an – sonst markiert Paperclip den Task als `blocked`/„recovery needed" (`unchanged_source_state`).

Adapter-Konfiguration des Agents:

```json
{
  "command": "/path/to/dockweave/deployments/paperclip/dockweave-run.sh",
  "args": ["demo-opencode"],
  "cwd": "/path/to/dockweave",
  "timeoutSec": 300
}
```

`args[0]` ist der DockWeave-Modellname (Agentenname). Der Legacy-Modus
(`args: ["dockweave-<cid>", "opencode", "/memory/.local/bin/opencode"]`) bleibt
für bestehende Konfigurationen erhalten.

## Konfiguration (Umgebungsvariablen)

Es gibt keine benutzer- oder projektspezifischen Defaults. Der Wrapper lädt
optional `${DOCKWEAVE_PAPERCLIP_ENV:-~/.config/dockweave/paperclip.env}`
(`KEY=VALUE`-Zeilen) und übernimmt die Werte in die Umgebung; alle Helfer
nutzen dieselben Variablen.

| Variable | Pflicht | Bedeutung |
|---|---|---|
| `PAPERCLIPAI_BIN` | ja, falls `paperclipai` nicht im `PATH` des Prozess-Adapters liegt | Pfad zur Paperclip-CLI (alle Helfer) |
| `DOCKWEAVE_DISPATCH` | nein | `daemon`: das Skript ruft nur `dockweave ticket run <agent>` auf; Arbeitsschleife, Gates, Routing und Lead-Modus laufen im Daemon (`docs/ticketing.de.md`, braucht `TICKET_PROVIDER=paperclip`). Dann gelten die Daemon-Schlüssel (`DISPATCH_DEFAULT_WORKSPACE`, `TICKET_LEADS`, `TICKET_ROLE_AGENTS`, …) statt der Skript-Variablen unten |
| `DOCKWEAVE_CLI` | ja, falls `dockweave` nicht im `PATH` liegt | Pfad zur DockWeave-CLI (Gates, Pipeline, Reviews) |
| `DOCKWEAVE_HOST_WORKSPACE` | ja für das DONE-Gate | Host-Pfad des Projekt-Workspaces; ohne ihn schlägt das DONE-Gate fail-closed fehl und `pre_run`/Routing entfallen |
| `DOCKWEAVE_PAPERCLIP_WORKSPACE` | nein (Default `/workspace`) | Container-Pfad des gemeinsamen Workspaces im Lead-Prompt |
| `DOCKWEAVE_PAPERCLIP_ROLES` | für Orchestrierung | Rollen-Datei (Default `~/.config/dockweave/paperclip-roles.json`, siehe `roles.example.json`) |
| `DOCKWEAVE_PAPERCLIP_AGENT_MAP` | für wake/rework | JSON `{"<dockweave-agent-id-oder-name>": "<paperclip-agent-id>"}`; nicht zugeordnete Agenten werden als `UNMAPPED` gemeldet, nie geraten |
| `DOCKWEAVE_SOCKET` | nein (Default `/run/dockweave/dockweaved.sock`) | Daemon-Socket für `/v1/chat/completions` |
| `DOCKWEAVE_TIMEOUT` / `PAPERCLIP_PROCESS_TIMEOUT` | nein (240 s, Orchestrierung 540 s) | Timeout des Endpoint-Aufrufs |
| `DOCKWEAVE_PAPERCLIP_HELPER_DIR` | nein (Verzeichnis des Skripts) | Ort der Python-Helfer |
| `DOCKWEAVE_PAPERCLIP_STATUS` | nein | erzwingt den Issue-Status nach dem Lauf statt der `STATUS:`-Zeile |
| `DOCKWEAVE_DEFAULT_TASK` | nein | Aufgabe, wenn kein Issue im Inbox liegt (Tests) |
| `DOCKWEAVE_GATE_TIMEOUT` | nein (900 s) | Gesamt-Timeout eines Gate-Laufs |
| `DOCKWEAVE_GATE_HOST_FALLBACK` | nein | `1` erlaubt Host-Ausführung der Gates (unsicher, nur explizit) |
| `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW` | nein | `1` akzeptiert unattributierte Review-Kommentare |
| `DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN` | nein | `1` lässt `blocked-check.py` bei CLI-/JSON-Fehlern durch (Default: blockiert) |
| `DOCKWEAVE_PIPELINE_BLOCKED_STATUS` | nein (Default `todo`) | Paperclip-Status eines durch die Pipeline gesperrten Issues (nie `blockedBy`) |
| `DOCKWEAVE_PIPELINE_STATE_DIR` | nein (Default `$XDG_STATE_HOME/dockweave/pipeline`) | Idempotenz-Marker für Sperr-Kommentare und Rework-Issues |
| `DOCKWEAVE_PIPELINE_RECONCILE_SEC` | nein (Default `600`) | Abgleichfenster für Aktionen mit unbekanntem Ausgang (Timeout/Absturz): solange wird weder neu geweckt noch neu angelegt oder kommentiert. Danach wird ein Rework-Issue nach erneuter Tag-Suche angelegt, Wake und Sperr-Kommentar werden wiederholt (ggf. ein Duplikat statt dauerhafter Blockade). Der Status-Re-Queue gesperrter Issues läuft auch innerhalb des Fensters weiter. |
| `PAPERCLIP_API_URL`, `PAPERCLIP_API_KEY`, `PAPERCLIP_AGENT_ID`, `PAPERCLIP_COMPANY_ID` | von Paperclip gesetzt | Inbox, Agent- und Company-Kontext |

## Pipeline (Gate → Agent)

Siehe `docs/paperclip-delegation.de.md` („Pipeline“) und ADR 0002. Kurz:

- vor jedem Turn: `pipeline.py precheck <agent>` (fail-closed); gesperrt →
  Kommentar am Issue (einmal je Grund), Status `todo`, kein Modell-Turn
- `409 pipeline_blocked` vom Endpoint wird genauso behandelt
- `pre_run`-Gates vor dem Lauf; ein Fehlschlag verhindert den Lauf
- nach `STATUS: DONE`: `after_run`/`post_run`-Gates mit diesem Agenten als
  Upstream, danach `pipeline.py route` (wake / rework / deny)
- Legacy-Ziele `dockweave-<cid>` umgehen den Daemon und damit die Pipeline

Tests: `make test-wrapper` (gefakte `dockweave`/`paperclipai`/`curl`).

## E2E-Test

```bash
paperclipai issue create -C <company> --title "Antworte nur mit X." \
  --assignee-agent-id <agent> --standalone
paperclipai agent wake <agent> -C <company> --source assignment
```

Danach: `paperclipai agent runtime-state <agent> --json` (lastRunStatus) und
die DockWeave-TUI des Agenten prüfen.

## Was noch fehlt

- paperclip läuft aktuell als manueller Prozess (`paperclipai run`); für
  Dauerbetrieb als systemd-User-Service installieren.
- Der Daemon muss den aktuellen Build laufen haben, damit die Native-Backends
  aktiv sind; sonst nutzt der Endpoint den TUI-Fallback (funktioniert ebenfalls).
- Der Prozess-Adapter schließt Issues nicht selbst; Status/Workflow bleibt
  paperclip-Aufgabe.
