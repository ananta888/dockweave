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

## Einschränkung (Harness)

Der Harness im Container ist aktuell ein deterministischer Platzhalter
(echoet den Task + Container-Hostname). Für einen echten Harness-Lauf
`codex exec` verwenden — dafür muss codex im Container **authentifiziert**
sein (gültiger OpenAI-Key). Siehe Zeile `docker exec` in `dockweave-run.sh`.
