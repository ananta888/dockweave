# Ticketing (ADR 0003)

DockWeave arbeitet über einen anbieterneutralen Port (`internal/ticketing`)
mit Tickets. Adapter: `native` (DockWeave-Speicher, im Paperclip-Stil) und
`paperclip` (Paperclip-HTTP-API). Gates, Pipeline-Admission, Nachweise und
Monitoring gelten für jeden Anbieter. Entwurf:
`docs/adr/0003-ticketing-adapters.de.md`.

Stand: Port, beide Adapter und die Anbieterwahl sind umgesetzt; Ticket-API,
Rollen, Dispatcher, Live-Sync und UI folgen
(`todo/todo.ticketing-2026-11-22.json`).

## Konfiguration (`/etc/dockweave/dockweaved.conf`)

| Schlüssel | Bedeutung |
|---|---|
| `TICKET_PROVIDER` | `native` oder `paperclip`. Leer = Ticketing aus (Standard). |
| `TICKET_MIRROR` | optionaler Live-Spiegel (`native`, `paperclip`, `none`); nie der führende |
| `PAPERCLIP_URL` | Standard `http://127.0.0.1:3100` |
| `PAPERCLIP_COMPANY` | Paperclip-Firmen-ID; leer = die einzige Firma der Instanz |
| `PAPERCLIP_TOKEN_FILE` | optionale Datei mit einem Paperclip-Board-API-Key (`local_trusted` braucht keinen) |

`dockweave ticket provider detect` schlägt einen Wert vor (Paperclip
erreichbar → `paperclip`, sonst `native`); ein laufender Daemon wird nie
umgestellt. `dockweave ticket provider` (und `GET /api/v1/tickets/provider`)
zeigt führenden Anbieter, Spiegel, Erreichbarkeit und Fähigkeiten. Ein
unbekannter oder nicht zu öffnender Anbieter wird dort gemeldet – DockWeave
weicht nie still auf einen anderen Anbieter aus.

## Adapter

| | native | paperclip |
|---|---|---|
| Speicher | `tasks` + `ticket_comments` (Migration 21) | Paperclip-Issues/-Kommentare |
| Kennung | `DW-<n>` | die von Paperclip (`PC-12`, …) |
| Threads, Inbox, Checkout, Unteraufgaben | ja | ja |
| Wecken | – (Scheduler, D-9) | `POST /api/agents/{id}/wakeup` |
| Urheberschaft | DockWeave-Akteur gespeichert | Paperclip-API-Identität; bei Kommentaren bleibt der DockWeave-Urheber erhalten (versteckte Markierung im Text) |
| Optimistische Version | ganzzahlige Version | Digest der kanonischen Felder + `expectedStatusVersion` |

**Paperclip-Agenten:** Ein Paperclip-`process`-Agent treibt den
DockWeave-Agenten an, der in `adapterConfig.args[0]` steht (so ist
`dockweave-run.sh` verdrahtet). Andere Paperclip-Agenten erscheinen als
`paperclip-agent:<uuid>`, Paperclip-Nutzer als `paperclip-user:<id>`.
Unterstützte Paperclip-Versionen: ≥ 2026.1106.0 (ältere melden sich als nicht
gesund).

## Tests

`go test ./internal/ticketing/...` führt den gemeinsamen Vertrag
(`internal/ticketing/contracttest`) gegen memory, native und einen
Paperclip-Nachbau aus. Lesender Smoke-Test gegen ein echtes Paperclip:
`DOCKWEAVE_PAPERCLIP_SMOKE_URL=http://127.0.0.1:3100 go test ./internal/ticketing/paperclip -run Smoke`.
