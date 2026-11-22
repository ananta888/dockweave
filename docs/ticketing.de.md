# Ticketing (ADR 0003)

DockWeave arbeitet über einen anbieterneutralen Port (`internal/ticketing`)
mit Tickets. Adapter: `native` (DockWeave-Speicher, im Paperclip-Stil) und
`paperclip` (Paperclip-HTTP-API). Gates, Pipeline-Admission, Nachweise und
Monitoring gelten für jeden Anbieter. Entwurf:
`docs/adr/0003-ticketing-adapters.de.md`.

Stand: Port, beide Adapter, Anbieterwahl und Ticket-API,
weitergereichte Menschen und Rechte sind umgesetzt; Live-Sync,
Dispatcher, Lead-Modus und die Ansicht Tickets (`docs/web-ui.de.md`) sind
umgesetzt; die Abnahme auf einer echten Installation ist TK-015
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
In-Memory-Anbieter aus; `internal/api` prüft dasselbe Szenario Ende zu Ende
für den nativen Anbieter und einen Paperclip-Nachbau.

### Betrieb

| | native | paperclip |
|---|---|---|
| Heartbeat | Daemon-Scheduler alle `DISPATCH_HEARTBEAT_SEC` (30) für laufende Agenten, höchstens `DISPATCH_CONCURRENCY` (4) gleichzeitig, einer je Agent | Paperclips Heartbeat startet `dockweave-run.sh`; mit `DOCKWEAVE_DISPATCH=daemon` ruft es nur `dockweave ticket run <agent>` auf |
| Wecken (Routing, Nacharbeit) | Scheduler-Anstoß | Paperclip-`wakeup` |

`dockweave ticket run AGENT` (`POST /api/v1/tickets/run`) führt einen
Heartbeat aus (Exit 3 = Pipeline gesperrt, 1 = Agenten-Turn fehlgeschlagen).
`dockweave ticket route TICKET --agent AGENT` wiederholt ein unvollständiges
Routing. Beides braucht für Menschen `agent.wake`.

Weitere `dockweaved.conf`-Schlüssel: `DISPATCH_DEFAULT_WORKSPACE`
(Gate-Workspace für Tickets ohne DockWeave-Workspace, wie
`DOCKWEAVE_HOST_WORKSPACE`), `TICKET_REVIEWERS` (kommagetrennte Agenten mit
VERDICT-Vertrag), `DISPATCH_PROMPT_TIMEOUT_SEC` (240).

Der Treiber führt die verbrauchende Pipeline-Admission direkt vor dem Turn
aus (wie der Ausführungsendpunkt) und zeigt den Turn als laufende
Endpoint-Arbeit im Graphen. Ohne `DOCKWEAVE_DISPATCH=daemon` behält der
Wrapper seine eigene Logik; der Standard wechselt erst nach der Abnahme auf
einer echten Installation (TK-015).

### Lead-Modus (TK-014)

In `TICKET_LEADS` genannte Agenten planen statt umzusetzen:

1. Ohne Kinder: Der Lead bekommt das Goal plus den Plan-Vertrag (Rollen aus
   `TICKET_ROLE_AGENTS`, z. B.
   `backend=demo-opencode,frontend=researcher,reviewer=reviewer,platform=platform`,
   gemeinsames Verzeichnis `DISPATCH_CONTAINER_WORKSPACE`, Standard
   `/workspace`) und antwortet mit einem JSON-Plan (im Codeblock oder frei).
   Je Aufgabe ein Kind (gleicher Titel → wiederverwendet), dem Agenten der
   Rolle zugewiesen, mit `blocked_by` aus `depends_on` und einer
   `DEPENDS_ON:`-Markierung; unbekannte Rollen werden als `SKIP` gemeldet. Nur
   nicht blockierte Kinder werden geweckt.
2. Ein Lead-Ticket mit Kindern wird nie erneut geplant: jeder Heartbeat
   schließt es nur ab. Ein fertiges Kind weckt die nächsten freien
   Geschwister, das letzte weckt den Lead.
3. Das Eltern-Ticket schließt, wenn alle Kinder fertig sind, das Review-Kind
   (Titel enthält `review.issue_match`, Standard „review“; `review.required`
   in `.paperclip/gates.json` standardmäßig wahr) ein `VERDICT: PASS` seines
   zugewiesenen Agenten oder eines benannten Menschen trägt (System- oder
   nicht zuordenbares PASS zählt nicht; das Urteil wird nach DockWeave
   gespiegelt) und – bei `human_approval.required` – eine DockWeave-Freigabe
   vorliegt. Das Warten darauf setzt das Eltern-Ticket auf `in_review` mit
   einem Kommentar; `dockweave gate approve <key>` (Recht `ticket.approve`,
   mit dem angemeldeten Menschen erfasst) setzt es fort und weckt den Lead.

Unterschied zum alten Skript: Die Paperclip-Interaktionskarte ist neben der
DockWeave-Freigabe nicht mehr nötig – die auditierte DockWeave-Freigabe ist
maßgeblich. `DOCKWEAVE_DISPATCH=daemon` übergibt jetzt auch die
Orchestrierung an den Daemon.

## Live-Sync (D-8, `internal/ticketing/tsync`)

Mit gesetztem `TICKET_MIRROR` hält der Daemon führendes System und Spiegel
synchron:

- **Zuordnungen** (`ticket_links`, Migration 23) verbinden jedes Ticket mit
  seinem Spiegel; der zuletzt synchronisierte Stand (`base`) ermöglicht einen
  feldweisen Drei-Wege-Abgleich von Titel, Beschreibung, Status, Priorität und
  Zuständigem.
- **Konflikte:** auf beiden Seiten geändertes Feld → die neuere Änderung
  gewinnt, bei Gleichstand das führende System; jeder Konflikt ist ein
  Audit-Ereignis `ticket.sync_conflict` mit beiden Werten.
- **Kommentare** werden nur angehängt und nach Herkunft zusammengeführt; ein
  Kommentar wandert nie zur Seite zurück, von der er kam; jede Kopie ist über
  eine Idempotenz-Markierung abgesichert.
- **Auslöser:** Domänenereignisse (Outbox, auch für reine
  Kommentar-Änderungen) und alle 30 s ein Abgleich über Tickets, die seit dem
  letzten erfolgreichen Lauf geändert wurden. Fehler stehen im Status und
  blockieren nie das führende System.
- **Struktur:** Eltern und `blocked_by` werden über die Zuordnungen übersetzt;
  DockWeave-Workspaces bleiben auf der nativen Seite. Ein Wert, den die andere
  Seite nicht darstellen kann (z. B. ein DockWeave-Mensch als
  Paperclip-Zuständiger), wird gemeldet und dort nicht gesetzt.
- **Agenten werden nur aus dem führenden System angetrieben.**

**Erstübernahme** bestehender Paperclip-Tickets:

```bash
dockweave ticket sync --initial --dry-run   # Plan: was angelegt würde
dockweave ticket sync --initial             # Übernahme (idempotent)
dockweave ticket sync                       # Status: verknüpft, Verzug, Konflikte, letzter Bericht
```

Gate-Läufe, Reviews und Freigaben behalten ihre Schlüssel: Paperclip-Tickets
verwenden die nackte Issue-ID, die der Wrapper immer gespeichert hat
(`dispatch.IssueKey`); die bestehende Historie braucht keine Umschlüsselung.

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

## Konfigurationsübersicht (`dockweaved.conf`)

| Schlüssel | Standard | Bedeutung |
|---|---|---|
| `TICKET_PROVIDER` | – (aus) | `native` oder `paperclip` |
| `TICKET_MIRROR` | `none` | Live-Spiegel, nie der führende |
| `PAPERCLIP_URL` / `PAPERCLIP_COMPANY` / `PAPERCLIP_TOKEN_FILE` | `http://127.0.0.1:3100` / einzige Firma / – | Paperclip-Verbindung |
| `TICKET_ROLES_FILE` | `/etc/dockweave/ticket-roles.json` | Gruppen → Rollen → Rechte |
| `GATEWAY_UIDS` | – | Peers, deren weitergereichter OIDC-Mensch beachtet wird |
| `DISPATCH_DEFAULT_WORKSPACE` | – | Gate-Workspace für Tickets ohne DockWeave-Workspace |
| `TICKET_REVIEWERS` | – | Agenten mit VERDICT-Vertrag |
| `TICKET_LEADS` / `TICKET_ROLE_AGENTS` / `DISPATCH_CONTAINER_WORKSPACE` | – / – / `/workspace` | Lead-Modus |
| `DISPATCH_HEARTBEAT_SEC` / `DISPATCH_CONCURRENCY` / `DISPATCH_PROMPT_TIMEOUT_SEC` | 30 / 4 / 240 | nativer Scheduler und Turn-Grenze |

CLI: `dockweave ticket provider [detect]`, `ticket run AGENT`, `ticket route
TICKET --agent AGENT`, `ticket sync [--initial] [--dry-run]`.
