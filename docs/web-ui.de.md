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
| Graph | Foblex-Flow-Graph aus Agenten, Workspaces, Memory-Stores, Credential-Grants und dem gemeinsamen Knowledge-Store; Klick auf Agenten-Knoten öffnet den Agenten |
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
