# Web-Integrationen: herdr & paperclip

Die Angular-UI zeigt beide Integrationen im **Integrations**-Tab und ergänzt
Runtime-Presets im Agenten-Dialog.

## Architektur (SOLID / DDD)

| Schicht | Ort | Aufgabe |
|---|---|---|
| Domain | `ui/web/src/app/core/domain/integrations.ts` | Typen für Runtime/herdr/paperclip, kein HTTP |
| Anwendungsservices | `core/services/runtime-presets.service.ts`, `herdr.service.ts`, `paperclip.service.ts` | Use Cases, je eine Verantwortung |
| Infrastruktur | `core/api.service.ts`, `HttpClient` | nur Transport |
| Präsentation | `components/integrations/*` | Orchestrierung und Darstellung |

Komponenten hängen von Anwendungsservices ab, nie direkt von Endpunkten.
Runtime-Defaults (Image/Command/Type) liegen an einer Stelle
(`RuntimePresetsService`).

## Agent anlegen

Der Dialog hat eine **Runtime**-Auswahl:

- `docker` — direkter Harness (Default)
- `herdr` — setzt Image `dockweave/herdr-agent`, Typ `codex`, argv-Command
- `paperclip` — Heartbeat-Runtime-Marker

Bei `herdr` wird der Command als **argv** gesendet (nicht `sh -c`), weil herdr
die kanonische Binary selbst startet.

## Herdr-Panel

- Agent-Auswahl (Agenten mit `runtime=herdr`)
- Live-Status aus herdr (`agent list`, `pane list`)
- **Broker-Zugang vorbereiten**: legt/wiederverwendet einen `codex-main`-Grant
  an, mintet eine 8h-`dws_`-Session, schreibt `~/.codex/config.toml` und die
  Token-Datei ins Agenten-Memory (Token wird nie zurückgegeben); danach Agent
  neu starten.
- **Prompt**: nutzt das native herdr-Backend (`herdr agent prompt --wait`) und
  liefert die Antwort aus dem Codex-Rollout.
- **Terminal**: bettet das bestehende xterm.js-Terminal ein und zeigt die
  herdr-UI (Haupt-TTY des Containers).

API: `GET/POST /api/v1/agents/{id}/herdr/{status,prompt,prepare}`.

## Paperclip-Panel

- Bettet die **Original-Paperclip-UI** per iframe ein
  (`DOCKWEAVE_PAPERCLIP_URL`, Default `http://127.0.0.1:3100`).
- Schnellaktionen über den lokalen CLI-Adapter des Web-Gateways:
  - Status (Companies/Agents/Issues)
- **Ziel-Agent**: weist dem Paperclip-Prozess-Agenten einen DockWeave-Agenten zu (`adapterConfig.args[0]`); gilt für den nächsten Wake.
  - Task anlegen und einem Agenten zuweisen
  - Agent wecken
- API (same-origin, Cookie-Auth):
  - `GET /paperclip/config`
  - `GET /paperclip/status?company=`
  - `POST /paperclip/issues`
  - `POST /paperclip/agents/{id}/wake`

Das Panel ist ein Adapter: die Original-UI bleibt die Quelle der Wahrheit, die
Schnellaktionen sind Komfort.

### DockWeave-Agenten als Paperclip-Assignees

Das Panel listet DockWeave-Agenten und legt je Agent einen Paperclip-Prozess-
Agenten `DW-<name>` an (`adapterConfig.args = ["<name>"]`). Paperclips
**Choose assignee** wählt damit 1:1 den DockWeave-Agenten. **Alle
synchronisieren** erledigt das für alle laufenden DockWeave-Agenten; bestehende
`DW-*`-Agenten werden aktualisiert.

## Credentials-Tab

Der Angular-Tab **Credentials** verwaltet Vault/Broker über Daemon-Endpunkte,
die den Broker-Admin-Socket proxyen:

- Secrets: Metadatenliste, write-only anlegen/rotieren, löschen
- Grants: Credential pro Agent, Modelle und Budgets (rpm, concurrent, Tokens/Tag, USD/Tag)
- Sessions: minten (Token wird einmal angezeigt), auflisten, widerrufen
- letzte Broker-Requests (secret-free)
- **Provider-Aktivierung**: schreibt einen OpenAI-kompatiblen DeepSeek-Provider (`/p/deepseek`) mit kurzlebigem `dws_`-Token in einen opencode-Agenten; der echte Key bleibt im Vault. Der Session-Refresh-Timer erneuert ihn stündlich.

Secret-Werte werden nie zurückgegeben; nur das frisch gemintete `dws_`-Token
wird bei der Erstellung einmal angezeigt.

## Usage-Tab

Zeigt Abo-/Plan-Verbrauch, soweit der Harness ihn lokal bereitstellt:

- **Codex**: 5-Stunden- und 7-Tage-Planfenster plus Credits, aus dem neuesten
  Rollout (`rate_limits`) gelesen.
- **Claude**: 5-Stunden- und 7-Tage-Auslastung aus dem lokalen Usage-Cache
  (aktualisiert durch `/usage` in der TUI).
- **OpenCode**: aggregierte Session-Kosten/Tokens vom lokalen OpenCode-Server;
  Abo-Credits werden von der lokalen API nicht bereitgestellt.

Der Tab aktualisiert alle 60 Sekunden; nicht verfügbare Daten werden mit
Hinweis angezeigt.

## Hinweise

- `herdr prepare` benötigt `BROKER_ADMIN_SOCKET` in `/etc/dockweave/dockweaved.conf`, damit der Daemon die Session minten kann.

- Der herdr-Prepare-Endpoint ist aktuell Codex-only (`codex-main`).
- Herdr-Prompts setzen voraus, dass der Agent über den herdr-Entrypoint
  gestartet wurde (Pane + Rollout vorhanden).
- Das Paperclip-iframe ist lokal gedacht; bei Zugriff von einem anderen Host
  `DOCKWEAVE_PAPERCLIP_URL` entsprechend setzen.
