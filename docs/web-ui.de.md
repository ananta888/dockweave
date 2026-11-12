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
# gibt aus: DockWeave web UI: http://127.0.0.1:8788/?token=…
```

Die ausgegebene URL öffnen. Das Token wird als HttpOnly-Cookie gespeichert;
weitere Requests brauchen es nicht mehr in der URL. Das Gateway lauscht
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
- **Agent Types** (oberes Menü): Default-MCP-Server und -Skills pro Agententyp,
  mit **Standardwerte einlesen**, das bestehende Agenten-Homes und übliche
  Host-Homes für OpenCode, Claude, Codex, Gemini und Antigravity scannt.
  Defaults gelten für alle Agenten des Typs; Per-Agent-Einträge mit gleichem
  Namen überschreiben sie. Die Ansicht erlaubt Anlegen/Enable/Disable/Löschen.
- **MCP-/Skill-Speicherung**: MCP-Server und Skills werden pro Agent in SQLite
  persistiert (`agent_mcp`, `agent_skills`, Migration 8) und über
  `/api/v1/agents/{id}/mcp` bzw. `/skills` verwaltet. Vor dem Agentenstart
  materialisiert dockweaved sie im Memory-Home: OpenCode
  `~/.config/opencode/opencode.json`, Claude `~/.claude.json`, zusätzlich
  generisch `~/.dockweave/config.json` und Skill-`SKILL.md`-Dateien. Es werden
  nur Environment-Variablennamen gespeichert; Werte bleiben im Credential-Vault.

Der Organisations-Modus speichert `display_name`, `org_unit` und `role` am
Agenten (`PUT /api/v1/agents/{id}/organization`). Läuft noch ein älterer
Daemon ohne diesen Endpunkt, hält die Web-UI die Werte in localStorage und
zeigt „Saved locally (daemon update pending)“; nach Daemon-Update/-Neustart
werden sie serverseitig persistiert.
