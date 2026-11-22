# Ticketing (ADR 0003)

DockWeave arbeitet über einen anbieterneutralen Port (`internal/ticketing`)
mit Tickets. Adapter: `native` (DockWeave-Speicher, im Paperclip-Stil) und
`paperclip` (Paperclip-HTTP-API). Gates, Pipeline-Admission, Nachweise und
Monitoring gelten für jeden Anbieter. Entwurf:
`docs/adr/0003-ticketing-adapters.de.md`.

Stand: Port, beide Adapter, Anbieterwahl und Ticket-API,
weitergereichte Menschen und Rechte sind umgesetzt; Dispatcher, Live-Sync und UI folgen
(`todo/todo.ticketing-2026-11-22.json`).

## Konfiguration (`/etc/dockweave/dockweaved.conf`)

| Schlüssel | Bedeutung |
|---|---|
| `TICKET_PROVIDER` | `native` oder `paperclip`. Leer = Ticketing aus (Standard). |
| `TICKET_MIRROR` | optionaler Live-Spiegel (`native`, `paperclip`, `none`); nie der führende |
| `PAPERCLIP_URL` | Standard `http://127.0.0.1:3100` |
| `PAPERCLIP_COMPANY` | Paperclip-Firmen-ID; leer = die einzige Firma der Instanz |
| `PAPERCLIP_TOKEN_FILE` | optionale Datei mit einem Paperclip-Board-API-Key (`local_trusted` braucht keinen) |
| `GATEWAY_UIDS` | Web-Gateway-Peers, deren weitergereichter OIDC-Mensch der Ticket-Akteur wird (SD-022) |

`dockweave ticket provider detect` schlägt einen Wert vor (Paperclip
erreichbar → `paperclip`, sonst `native`); ein laufender Daemon wird nie
umgestellt. `dockweave ticket provider` (und `GET /api/v1/tickets/provider`)
zeigt führenden Anbieter, Spiegel, Erreichbarkeit und Fähigkeiten. Ein
unbekannter oder nicht zu öffnender Anbieter wird dort gemeldet – DockWeave
weicht nie still auf einen anderen Anbieter aus.

## API (`/api/v1/tickets`)

| Methode | Pfad | |
|---|---|---|
| GET | `/api/v1/tickets?status=a,b&assignee=agent:<id\|name>&parent=&q=&updated_since=&limit=` | Liste (führender Anbieter) |
| POST | `/api/v1/tickets` | anlegen (`title`, `description`, `status`, `priority`, `assignee`, `parent`, `workspace_id`) |
| GET / PATCH | `/api/v1/tickets/{id}` | lesen / ändern; `expected_version` → 409, wenn veraltet |
| GET / POST | `/api/v1/tickets/{id}/comments` | nur anhängbarer Thread |
| POST | `/api/v1/tickets/{id}/checkout`, `/release` | `{"agent": "<id\|name>"}`; exklusiv, 409 bei Konflikt |

`{id}` ist eine nackte ID oder `<anbieter>:<id>` des führenden Anbieters.
Agenten dürfen per Name angegeben werden; gespeichert wird die stabile ID.
Unbekannte JSON-Felder ergeben 400. Ohne Anbieter oder wenn er nicht
erreichbar ist: `503 ticketing_unavailable` (geschlossen scheitern).
Monitor-Peers lesen, Admin-Peers schreiben; Menschen werden je Recht
geprüft (unten).
Akteur ist der vom Gateway weitergereichte OIDC-Mensch (`iss|sub`, SD-022),
sonst der Socket-Peer (`peer:<name>`).

Jede erfolgreiche Änderung – über API, Dispatcher oder Sync – ist ein
Domänenereignis (`ticket.created`, `.updated`, `.status_changed`, `.assigned`,
`.comment_added`, `.checked_out`, `.released`) in der hash-verketteten
Audit-Kette (Decorator `ticketing.Observe`).

## Rollen und Rechte (D-6a)

Per Keycloak angemeldete Menschen werden über ihre **Gruppen** berechtigt:
`/etc/dockweave/ticket-roles.json` (`TICKET_ROLES_FILE`) ordnet Gruppen
Rollen und Rollen Rechte zu; Beispiel: `deployments/ticket-roles.example.json`.
Ohne Datei gelten die Standards (`dockweave-admin` → alles,
`dockweave-monitor` → `ticket.read`). Unbekannte Gruppen geben keine Rechte.

Rechte: `ticket.read`, `ticket.create`, `ticket.edit`, `ticket.comment`,
`ticket.assign`, `ticket.approve`, `ticket.delete`, `gate.override`,
`agent.wake`, `terminal.view`, `terminal.takeover`, `provider.configure`,
`sync.run` (`"*"` = alle). Der Daemon setzt sie bei jedem Ticket-Aufruf und
bei Pipeline-Overrides durch; eine Ablehnung ist `403` mit
`"permission": "<name>"`. Zuständige ändern oder für einen Agenten sperren
braucht `ticket.assign`, andere Feldänderungen `ticket.edit`.

Die Datei wird bei Änderung neu gelesen (kein Neustart); jedes Laden und
jeder Fehler wird auditiert (`ticket.roles_loaded` / `ticket.roles_invalid`).
Eine ungültige Datei (unbekanntes Recht, undefinierte Rolle, unbekannter
Schlüssel) macht alle Menschen **nur lesend**, bis sie korrigiert ist – alte
Rechte gelten nie weiter. `GET /api/v1/tickets/me` liefert Rollen, Rechte und
einen Konfigurationsfehler für die UI. Socket-Peers ohne weitergereichten
Menschen (CLI) behalten ihre Peer-Rolle (`ADMIN_UIDS`/`MONITOR_UIDS`).

## Dispatcher (`internal/dispatch`, D-4)

Die Arbeitsschleife aus `deployments/paperclip/dockweave-run.sh` und
`pipeline.py`, anbieterneutral, als feste Abfolge je Heartbeat:

1. nächstes Ticket des Agenten (eigene Teilarbeit wird fortgesetzt);
2. offene oder unlesbare Blocker (`blocked_by`) → zurück in die Warteschlange,
   kein Turn;
3. Pipeline-Admission; abgelehnt, nicht erreichbar oder widersprüchlich →
   einmal je Grund-Satz kommentieren, zurück in die Warteschlange, kein Turn;
4. exklusiver Claim; ein Heartbeat je Agent gleichzeitig;
5. `pre_run`-Gates (mit Workspace) – Fehlschlag → Kommentar, zurück in die
   Warteschlange, kein Turn;
6. der Agenten-Turn (Prompt mit STATUS-Vertrag, beim Reviewer zusätzlich mit
   VERDICT-Vertrag); ein 409 des Endpunkts wird wie eine abgelehnte
   Admission behandelt;
7. `STATUS: DONE` zählt erst nach bestandenen `after_run,post_run`-Gates
   (ohne Workspace bleibt es offen); danach Routing: freigegebene
   Folge-Agenten wecken (`allow` / `wake:<agent>`), genau ein
   Nacharbeits-Ticket je Gate-Lauf bei `rework` (nie bei tamper/unavailable),
   `deny` melden; ein Routing-Fehler verwirft das Urteil nie und nennt die
   Wiederholung;
8. `VERDICT: FAIL` → Nacharbeits-Ticket für den Autor der geprüften
   Abhängigkeit, Urteil nach DockWeave gespiegelt;
9. Antwort + Hinweise als Kommentar, resultierender Status; der Claim bleibt
   bei Teilarbeit bestehen, sonst wird er freigegeben.

Nebenwirkungen (Sperr-Kommentar, Wecken, Nacharbeits-Ticket) sind über
dauerhafte Markierungen idempotent (`dispatch_actions`, Migration 22): Eine
bestätigte Wirkung wird nie wiederholt, ein unbekannter Ausgang sperrt eine
Wiederholung für das Abgleichsfenster (600 s), eine verlorene
Anlage-Antwort wird über das Titel-Tag abgeglichen. `go test
./internal/dispatch` führt die übertragene Wrapper-Testmatrix gegen einen
In-Memory-Anbieter aus. Anbindung an den Daemon, der 30-s-Scheduler und
`dockweave ticket run` folgen (TK-009); der Lead-/Orchestrierungsmodus ist TK-014.

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
