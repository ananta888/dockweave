# Web-UI (Angular)

DockWeave bringt eine Browser-Oberfläche für alle Agenten mit: Agentenliste
links, Chat/Terminal, Konfiguration pro Agent, Projektzugriff, Memory,
Broker-Grants und ein Analyse-Dashboard.

Sprache: Deutsch · [English](web-ui.md)

## Start

```bash
make web          # Angular bauen und in das Go-Binary einbetten
make build
./bin/dockweave web
# gibt aus: DockWeave web UI: http://127.0.0.1:8788/
#            Login-Token: …
```

Die ausgegebene URL öffnen und den Login-Token auf der Anmeldeseite eingeben.
Das Token wird als HttpOnly-Cookie gespeichert; weitere Requests brauchen es
nicht mehr in URL oder Verlauf. API-Clients können stattdessen
`Authorization: Bearer <token>` senden. Fehlversuche werden begrenzt
(5/Minute je Adresse), und jede Antwort trägt `Cache-Control: no-store`,
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` und
`Referrer-Policy: no-referrer`. Das Gateway lauscht
standardmäßig **nur auf Loopback** und läuft unprivilegiert mit deiner UID.

Flags:

```bash
dockweave web --listen 127.0.0.1:8788 --open
dockweave web --web-dir ui/web/dist/dockweave-web/browser  # Dev-Build ausliefern
DOCKWEAVE_WEB_TOKEN=… dockweave web                        # festes Token
```

## Funktionen

| Bereich | Funktion |
|---|---|
| Agentenliste | Alle Agenten mit Live-Status, Konflikt-Warnung, Agent anlegen |
| Chat / Terminal | Volles PTY im Browser (xterm.js) mit dem Agenten-TUI; Prompt-Feld schreibt ins TUI |
| Konfiguration | Type/Image/argv, UID/GID, Egress-Profil, EnvFile, `/agent`- und `/notes`-Ordner, Start/Stop/Restart/Remove |
| Projektzugriff | Workspaces zuweisen/entziehen (ro/rw) |
| Memory | Memory-Identität anhängen/lösen, Snapshots anlegen/restoren/löschen |
| Credentials | Broker-Credential-Grants ansehen (nur Metadaten) |
| Analyse | Aktivität/CPU/Memory, Dateikonflikte, Events, Broker-Traffic, Provider-Status |
| Graph | Foblex-Flow-Graph in zwei Modi: **Organisation** (Einheiten → Agenten als 2D-Figuren mit Rollenlabels, direkt im Seiten-Inspektor editierbar) und **Ressourcen** (Workspaces, Memory, Credentials, Knowledge). Ports pro Node-Art und Seite konfigurierbar; Layout-Bereinigung mit Hierarchie/Spalten/Grid/Radial/Frei/Gemischt; Connection-Builder, JSON-Schema-Contract, Layout-Templates/Platzhalter und Agent-Connectoren (MCP/Gate/HTTP/Agentenkette). Laufende Nodes pulsieren grün, inaktive sind grau, Fehler blinken rot |
| Workspaces | Host-Verzeichnisse registrieren/entfernen |

Secret-Werte werden nie angezeigt. Anlegen/Rotieren von Secrets und
Credential-Grants bleibt bei `dockweave secret` / `credential` (Broker-Admin-
Socket).

## Architektur

```
Browser ──HTTP/WebSocket──> dockweave web (unprivilegiert, Loopback, Token)
                              ├── /api/*  ──Reverse Proxy──> Daemon-Unix-Socket
                              ├── /ws/term ──Bridge──> Term-Unix-Socket (PTY)
                              └── /       ──eingebettete Angular-Assets
```

Das Gateway implementiert keine privilegierten Operationen selbst. Es leitet
mit deinen Peer-Credentials an den Daemon weiter; dessen `SO_PEERCRED`-
Autorisierung gilt weiterhin.

## Sicherheitshinweise

- Listener standardmäßig `127.0.0.1`; andere Bind-Adressen warnen.
- Jede Anfrage außer `/favicon.ico` braucht das zufällige Token-Cookie. Das
  Token entspricht deinem Admin-Zugang — URL nicht teilen.
- Die UI kann Admin-Aktionen ausführen, weil das Gateway als du läuft.
- Keine Secrets über die Daemon-API; die UI zeigt nur Metadaten und
  Terminal-Ausgabe.
- Für Fernzugriff das Gateway hinter einen authentifizierten Reverse-Proxy
  mit TLS legen; nicht direkt exponieren.

## Entwicklung

```bash
cd ui/web
npm ci
npm start            # Angular Dev-Server (braucht Proxy zum Gateway)
npm run build        # Produktions-Build
```

Das Go-Binary bettet `internal/webui/dist` ein; `make web` baut Angular neu
und kopiert die Assets vor `make build`. Die Graph-Ansicht nutzt
`@foblex/flow` (dasselbe Framework wie das ObjectGraph-Policy-Frontend).

### Gates und Pipelines im Graphen

- **Gate-Editor** (Connector-Dialog und Node-Popover): optionaler Ausdruck
  (leer = Exit-Code entscheidet), inline gegen
  `POST /graph/validate-expression` geprüft; ein ungültiger Ausdruck lässt
  sich nicht speichern, und ein Ausdruck, der `budget` liest, ohne dass ein
  Budget konfiguriert ist, zeigt eine Warnung. Routing ist eine Auswahl statt
  Freitext: bei PASS `allow`, `wake` (mit Agentenauswahl) oder `deny`; bei
  FAIL `deny` oder `rework`; dazu `reason_true`/`reason_false` und optional
  ein maximales PASS-Alter. Alter Freitext öffnet sich als Begründung mit der
  Default-Aktion.
- **proceed-Kante** (Gate → Ziel-Agent) zeigt den Pipeline-Zustand aus
  `GET /api/v1/pipelines/admission`: grün `✓ ok`/`override`, rot
  `✕ failed`/`error`/`tamper`/`denied`, gelb gestrichelt `stale`/`expired`/
  `no_run`/`unknown_gate` und `nicht übertragen`, solange der Daemon die
  Kante nicht kennt. Das Label ist per Tastatur fokussierbar; Tooltip und
  zugänglicher Name nennen Ausdruck, Grund-Code, letzte Lauf-ID und Alter.
- **Downstream-Agenten** tragen ein Badge `Pipeline frei` / `⛔ blockiert`
  mit Textalternative, die die sperrenden Gates nennt. Der Status
  aktualisiert sich alle 15 Sekunden und nach jedem Speichern, ohne Reload.
- **Sync-Fehler:** `pipeline_sync.error` aus der Speicher-Antwort erscheint
  als Banner auf der Canvas und im Einstellungsabschnitt „Pipeline (Gate →
  Agent)“, zusammen mit den Ableitungs-Warnungen. Ein Gate mit Ziel-Agent,
  aber ohne Shell-Befehl wird mit einer Warnung markiert (das Ziel bliebe
  gesperrt).

UI-Test ohne Daemon: `cd ui/web && npm run build && npm run e2e:pipeline`
(Playwright, gemocktes Backend).

### Live-Status im Graphen

Knoten und Kanten zeigen, was gerade passiert – nur aus echten Daten
(Monitor-Aktivität, `GET /api/v1/conflicts`, `GET /api/v1/live`,
Pipeline-Admission, Gate-Läufe und Reviews). Unbekanntes wird nie grün.

| Farbe / Rhythmus | Zustand | Quelle |
|---|---|---|
| grün, ruhiges Auf- und Abblenden (2,5 s) | Agent arbeitet (liest/schreibt oder CPU ≥ 10 %) | Monitor |
| grün | Endpoint-Auftrag läuft (Agent) | `/api/v1/live` `endpoint_job` |
| grün | Gate-Lauf läuft: geprüfter Agent, Kante Agent → Gate, Gate | `/api/v1/live` `gate_run` |
| grün | Pipeline-Fluss: Kante Gate → Agent, Gate und Agent, solange der Agent auf einer freigegebenen Kante arbeitet oder ≤ 60 s nach der Freigabe | Admission + Live |
| grün | zwei aktive Agenten im selben Workspace (Grant-Kanten, Workspace-Knoten) oder auf einer Agent → Agent-Kante („reden“) | Grants, Monitor |
| blau | Agent startet/stoppt (`created`, `restarting`, `removing`) | Agent-Zustand |
| gelb, langsam | Override aktiv (Kante + Gate) · Review ausstehend (Gate) | Admission · Gate-Läufe/Reviews |
| grau gestrichelt | Graph und erzwungene Pipeline nicht synchron | Revisions-Drift |
| rot, langsam (2 s) | blockiert: `no_run`, `stale`, `expired`, `denied` | Admission |
| rot, mittel (1 s) | `failed`, `error`, `internal`, `unknown_gate`, `fingerprint_unavailable`, Status unbekannt, Daemon nicht erreichbar, Container `dead` | Admission, Shell-Refresh |
| rot, hartes Blinken (0,4 s) | `tamper`, Dateikonflikt zwischen Agenten | Admission, Konflikte |

Der schwerste Zustand gewinnt. Jeder markierte Knoten hat einen Symbolpunkt
(● ◐ ⏳ ⚑ ⇄ ! ⚠ ✖) und einen Tooltip bzw. zugänglichen Namen mit den Gründen;
Kanten-Labels tragen dasselbe Symbol und denselben Text. Bei „Bewegung
reduzieren“ im Betriebssystem bleiben nur Farbe und Symbol. Ein lediglich
laufender (`running`) Agent pulsiert nicht mehr.

`GET /api/v1/live` (Admin + Monitor, nur lesend) listet die Endpoint-Aufträge
und Gate-Läufe, die der Daemon gerade ausführt; Einträge verschwinden mit
Ende der Anfrage und liegen nur im Speicher. Von der Pipeline-Admission
abgelehnte Anfragen erscheinen nie.

UI-Test: `npm run e2e:pipeline` enthält `e2e/live-status.spec.ts`.

### Graph-Einstellungen

- **Layout**: Hierarchie, Spalten, Grid, Radial, Frei oder Gemischt;
  **Aufräumen** wendet den gewählten Algorithmus an. Im Frei-Modus bleiben
  gezogene Node-Positionen erhalten.
- **Ports**: Inputs/Outputs pro Seite (`left`, `right`, `top`, `bottom`) für
  jede Node-Art, gespeichert in `localStorage`.
- **JSON-Schema-Contract**: `dockweave.graphSchema` enthält Node-Ports,
  Verbindungstypen und Verbindungs-Templates. Editierbar, downloadbar, aus
  Datei ladbar und auf Default zurücksetzbar.
- **Connection-Builder**: Template, Quell-Node/Port, Ziel-Node/Port, Typ und
  Label wählen; eigene Kanten werden lokal gespeichert und im Graph
  dargestellt.
- **Layout-Templates**: aktuelle Node-Positionen als benanntes Template
  speichern. Beim Anwenden werden passende Nodes positioniert, neue Nodes
  automatisch aufgeräumt und fehlende Template-Nodes als rote, gestrichelte,
  deaktivierte Platzhalter an ihrer gespeicherten Position dargestellt.
  Rechtsklick auf einen Platzhalter aktiviert/deaktiviert oder entfernt ihn.
- **Node-Palette**: immer sichtbare Node-Typen für MCP, Skill, deterministisches Gate, HTTP, Agentenkette und Custom; Workspace/Memory/Credential/Knowledge kommen aus den Agentendaten. Die Palette ist drag-and-drop-fähig: Node auf die Canvas ziehen erzeugt sie an dieser Position; ein Klick platziert sie in der aktuellen Ansicht. Beim Erzeugen öffnet sich kein Dialog. Doppelklick auf einen Node öffnet die passende Konfiguration als kompaktes Popover direkt neben der Node (kein bildschirmfüllender Dialog). Kanten-Aktionen und die Löschen-Toolbar erscheinen neben der angeklickten Kante.
- **Verbindungen**: Neue Nodes werden unverbunden platziert. Beim Hovern zeigt eine Node rechts kleine Buttons: `＋` startet den Verbindungsmodus (Typ wählen, dann Ziel-Node anklicken), `×` entfernt die Node. Beim Hovern einer Kante erscheinen mittig `＋` (Zwischen-Node einfügen) und `×` (Kante löschen). Klick auf eine Verbindung zeigt eine Toolbar mit Löschen; Rechtsklick bietet ebenfalls Löschen.
- **Deterministische Gates** zeigen Bedingung und Routing (`PASS→…` / `FAIL→…`) direkt im Node und werden im Connector-Dialog konfiguriert (siehe „Gates und Pipelines im Graphen“).
- **2D-Agentenfiguren**: 15 eingebaute Rollenfiguren (CEO, CTO, Developer, Reviewer, QA, DevOps, Researcher, Designer, Analyst, Operator, Product, Architect, Security, Data, Support) plus eigener SVG-Import. Eigene SVGs werden bereinigt (keine Scripts/Event-Handler/foreignObject/JS-URLs, max. 64 KiB) und pro Agent gespeichert.
- **Rechtsklick-Menü** auf jedem Node und auf der Canvas: Layout als Template
  speichern, Template anwenden, Node-Position zurücksetzen, Agent öffnen,
  Connectoren konfigurieren, eigene Kanten löschen, Platzhalter
  aktivieren/entfernen oder Layout aufräumen.
- **Agent-Connectoren**: MCP-Server (JSON-Contract), deterministisches Gate,
  HTTP-API, Agentenkette und Custom. Pro Agent gespeichert und als verbundene
  Nodes/Kanten dargestellt; der Dialog validiert JSON-Contracts und erlaubt
  Enable/Disable pro Connector.
- **MCP-Contract**: `docs/mcp-server-contract.schema.json` beschreibt den
  normalisierten DockWeave-Server-Contract (`name`, `transport`,
  `command`/`url`, `envKeys`, `capabilities`, `tools[].inputSchema`,
  Resources, Prompts). Der Connector-Dialog importiert eine
  `mcpServers`-Client-Konfiguration oder eine `initialize`-/`tools/list`-
  Antwort und validiert die Tool-Input-Schemas. Das Default-Graph-Schema
  enthält ein Beispiel unter `contracts.mcpServer`.
- **UI-MCP-Server**: `dockweave web --mcp-listen 0.0.0.0:8791` stellt einen Streamable-HTTP-MCP-Endpunkt unter `/mcp/ui` bereit (Bearer-Token per `?token=` oder `Authorization`). Tools: `ui_get_state`, `ui_show_hint`, `ui_select_agent`, `ui_select_view`, `ui_set_main_agent`. Die Operator-UI verbindet sich über `/ws/ui` und zeigt Hinweise an CSS-Selektoren, Agentenzeilen oder Graph-Nodes. Typ-Defaults `dockweave-ui` für opencode/claude können auf den Endpunkt zeigen.
- **Hauptagent**: über `☆ Hauptagent` im Agent-Header oder per Rechtsklick im Graph festlegen. Ein schwebendes Chat-Fenster rechts unten zeigt das gewählte SVG, den Anzeigenamen und ein Live-Terminal/Chat mit diesem Agenten. Beim Schließen bleibt der Hauptagent als schwebender Avatar-Button sichtbar; Klick auf den Avatar öffnet den Chat wieder.
- **Agent Types** (oberes Menü): Default-MCP-Server und -Skills pro Agententyp,
  mit **Standardwerte einlesen**, das bestehende Agenten-Homes und übliche
  Host-Homes für OpenCode, Claude, Codex, Gemini und Antigravity scannt.
  Defaults gelten für alle Agenten des Typs; Per-Agent-Einträge mit gleichem
  Namen überschreiben sie. Neue Agenten werden mit den Typ-Defaults
  vorbefüllt und haben eigene editierbare MCP-/Skill-Einträge; der
  Connector-Dialog zeigt die Projekte des Agenten und bietet
  *Typ-Defaults übernehmen*, um fehlende Typ-Einträge zu kopieren. Die Ansicht erlaubt Anlegen/Enable/Disable/Löschen.
- **MCP-/Skill-Speicherung**: MCP-Server und Skills werden pro Agent in SQLite
  persistiert (`agent_mcp`, `agent_skills`, Migration 8) und über
  `/api/v1/agents/{id}/mcp` bzw. `/skills` verwaltet. Vor dem Agentenstart
  materialisiert dockweaved sie im Memory-Home: OpenCode
  `~/.config/opencode/opencode.json`, Claude `~/.claude.json`, zusätzlich
  generisch `~/.dockweave/config.json` und Skill-`SKILL.md`-Dateien. Es werden
  nur Environment-Variablennamen gespeichert; Werte bleiben im Credential-Vault.

Der Hauptagent ist im Graph mit goldener Umrandung und `★ HAUPTAGENT`-Badge markiert; sein gewähltes SVG wird normal angezeigt.

Der Organisations-Modus speichert `display_name`, `org_unit` und `role` am
Agenten (`PUT /api/v1/agents/{id}/organization`). Läuft noch ein älterer
Daemon ohne diesen Endpunkt, hält die Web-UI die Werte in localStorage und
zeigt „Saved locally (daemon update pending)“; nach Daemon-Update/-Neustart
werden sie serverseitig persistiert.
