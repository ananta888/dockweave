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
  - Task anlegen und einem Agenten zuweisen
  - Agent wecken
- API (same-origin, Cookie-Auth):
  - `GET /paperclip/config`
  - `GET /paperclip/status?company=`
  - `POST /paperclip/issues`
  - `POST /paperclip/agents/{id}/wake`

Das Panel ist ein Adapter: die Original-UI bleibt die Quelle der Wahrheit, die
Schnellaktionen sind Komfort.

## Hinweise

- `herdr prepare` benötigt `BROKER_ADMIN_SOCKET` in `/etc/dockweave/dockweaved.conf`, damit der Daemon die Session minten kann.

- Der herdr-Prepare-Endpoint ist aktuell Codex-only (`codex-main`).
- Herdr-Prompts setzen voraus, dass der Agent über den herdr-Entrypoint
  gestartet wurde (Pane + Rollout vorhanden).
- Das Paperclip-iframe ist lokal gedacht; bei Zugriff von einem anderen Host
  `DOCKWEAVE_PAPERCLIP_URL` entsprechend setzen.
