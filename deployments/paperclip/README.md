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
  Mit `DOCKWEAVE_PAPERCLIP_CLOSE=1` wird das Issue danach auf `done` gesetzt.

Adapter-Konfiguration des Agents:

```json
{
  "command": "/home/krusty/dockweave/deployments/paperclip/dockweave-run.sh",
  "args": ["demo-opencode"],
  "cwd": "/home/krusty/dockweave",
  "timeoutSec": 300
}
```

`args[0]` ist der DockWeave-Modellname (Agentenname). Der Legacy-Modus
(`args: ["dockweave-<cid>", "opencode", "/memory/.local/bin/opencode"]`) bleibt
für bestehende Konfigurationen erhalten.

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
