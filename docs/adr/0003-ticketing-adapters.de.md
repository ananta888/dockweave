# ADR 0003 — Ticketing als Port mit austauschbaren Adaptern (Paperclip, nativ)

- Status: angenommen (2026-11-22, Entscheidungen des Eigentümers unten)
- Datum: 2026-11-22
- Entscheidungen: D-1 … D-9
- Verfolgt in: `todo/todo.ticketing-2026-11-22.json`

## Kontext

DockWeave soll eine Plattform werden, auf der KI-Agenten und Menschen an
denselben Tickets arbeiten. Heute gibt es zwei Hälften:

1. **Paperclip-Anbindung** – der eigentliche Ticket-Fluss für Agenten. Der
   Paperclip-`process`-Adapter startet `deployments/paperclip/dockweave-run.sh`:
   Inbox-Eintrag holen, Pipeline prüfen, isolierten Agenten über
   `POST /v1/chat/completions` antreiben, Gates laufen lassen, Antwort als
   Kommentar schreiben, `STATUS:`-Vertrag auf den Issue-Status abbilden und
   routen (wecken, Nacharbeit, Delegation). Alles Bash/Python gegen die
   Paperclip-CLI.
2. **Produktivitätsschicht** (`internal/productivity`, Tab „Productivity“) –
   deterministische Planung für Menschen: Ziele, Aufgaben mit
   `assignee_kind` human/agent, Fokus, gemessene `work_events`, `evidence`
   (`verified` nur mit Nachweis), Budgets, Berichte, Audit. Agenten-Aufgaben
   existieren im Modell, werden aber von nichts ausgeliefert; es gibt keine
   Threads, keine Inbox, kein Checkout, und die UI blendet Agenten-Aufgaben aus.

Der **Daemon ist bereits ticket-neutral**: Gate-Läufe, Reviews, Freigaben,
der Header `X-DockWeave-Issue` und die Pipeline-Admission tragen nur eine
opake Issue-ID. Die Kopplung an Paperclip steckt in den Wrapper-Skripten und
im CLI-Adapter des Web-Gateways (`internal/webui/paperclip.go`).

Paperclip bietet schon das meiste einer Kollaborationsplattform (Issues mit
Unteraufgaben, Kommentare, Dokumente, Work Products, Interaktionen,
Freigaben, Inbox pro Agent, Checkout, Heartbeats, Routinen, Organigramm,
Ziele, Projekte, Budgets, Mitglieder). Nicht aber DockWeaves Kern:
Container-Isolation, deterministische Gates mit Manipulationserkennung,
vom Daemon erzwungene Pipeline-Admission, Live-Monitoring, Dateikonflikte,
Nachweis statt Behauptung.

## D-1: Ein Port, getrennte Schnittstellen (ISP), anbieterneutraler Kern

`internal/ticketing` definiert den Port. Kleine Schnittstellen statt einer
breiten, damit ein Adapter nur implementiert, was sein Backend kann:

| Schnittstelle | Operationen |
|---|---|
| `Tickets` | `Get`, `List(filter)`, `Create`, `Update(status, assignee, fields)`, `Children` |
| `Threads` | `Comments(ticket)`, `AddComment(ticket, author, body)` |
| `Inbox` | `Next(assignee)`, `Checkout(ticket, agent)`, `Release` (ein Bearbeiter pro Ticket) |
| `Approvals` | `Request`, `Decide(approve/reject/revise)` |
| `Waker` | `Wake(agent)` |
| `Provider` | `Name()`, `Health(ctx)`, `Capabilities()` + Zugriff auf die Schnittstellen oben oder `nil` |

Kanonische Typen (`Ticket`, `Comment`, `Assignee{kind: human|agent, id}`,
`Status`) und ein kanonischer Statussatz nah an Paperclip (`backlog, todo,
in_progress, in_review, blocked, done, cancelled`). Jeder Adapter bildet
seine Zustände selbst ab (Strategy). Der Kern hängt nur vom Port ab (DIP);
neue Backends kommen ohne Änderung am Kern dazu (OCP).

`TicketRef{provider, id}` ersetzt die nackte Issue-ID an der Kerngrenze.
Bestehende IDs bleiben als `paperclip:<id>` gültig (Migration ohne
Datenverlust).

## D-2: Adapter

| Adapter | Backend | Hinweise |
|---|---|---|
| `paperclip` | Paperclip-**HTTP-API** (dessen OpenAPI), nicht die CLI | Die CLI ändert sich schnell und ist langsam (Node-Start je Aufruf); die API ist versioniert |
| `native` | DockWeave-SQLite, aufgebaut auf der bestehenden `tasks`-Tabelle | „Paperclip-Stil“: Threads, Zuweisung Mensch/Agent, Inbox, Checkout, Unteraufgaben, Freigaben; behält Nachweise, Work Events, Fokus, Budgets |
| später | GitHub Issues, Jira, Linear, … | Nur `Tickets` + `Threads` nötig; Capabilities blenden den Rest aus |

Anti-Corruption-Layer: adapterspezifische Felder gelangen nie in den Kern;
unbekannte Zusatzfelder liegen in `Ticket.Extra` (nur lesend für die UI).

## D-3: Auswahl – ein führender Anbieter, optional Live-Sync mit einem Spiegel

Die Daemon-Konfiguration nennt den **führenden** Anbieter
(`TICKET_PROVIDER=native|paperclip`) und optional einen **Spiegel**
(`TICKET_MIRROR=paperclip|native|none`). `auto` ist nur ein Vorschlag bei der
Einrichtung (Paperclip erreichbar → `paperclip`, sonst `native`) und wird
einmal in die Konfiguration geschrieben. Der führende Anbieter wechselt zur
Laufzeit nie.

- **Agenten werden nur aus dem führenden System angetrieben.** Der Spiegel
  wird nie ausgeführt, kein Ticket läuft doppelt.
- Ist der führende Anbieter nicht erreichbar, scheitern Ticket-Operationen
  geschlossen mit `ticketing_unavailable`, die UI zeigt das, und Agenten
  werden nicht angetrieben.
- Der Live-Sync zwischen führendem System und Spiegel ist D-8; die einmalige
  Übernahme bestehender Paperclip-Tickets gehört dazu.

## D-4: Die Arbeitsschleife wandert in den Daemon (Template Method)

Die Logik aus `dockweave-run.sh`/`pipeline.py` wird zu `internal/dispatch`,
einer festen Abfolge anbieterneutraler Schritte:

```
nächstes Ticket → Checkout → Pipeline-Admission → pre_run-Gates → Agent antreiben
→ STATUS-Vertrag → after_run/post_run-Gates → Kommentar + Status → Routing → Freigabe
```

Routing-Aktionen (`allow`, `wake`, `deny`, `rework`) werden Strategien. Der
Paperclip-`process`-Adapter ruft dann nur noch einen dünnen Befehl auf
(`dockweave ticket run <agent>`); den nativen Anbieter treibt der eigene
Scheduler des Daemons (Heartbeat je Agent). Bis die Gleichwertigkeit durch die
bestehenden Wrapper-Tests belegt ist, bleibt der heutige Wrapper als
Legacy-Pfad.

## D-5: Ereignisse (Observer)

Ticket-Änderungen sind Domänenereignisse (`ticket.created`,
`ticket.status_changed`, `ticket.comment_added`, `ticket.assigned`,
`ticket.checked_out`) auf dem bestehenden Audit-/Event-Bus. Abnehmer:
Audit-Kette, UI-WebSocket, Live-Status im Graphen (`docs/web-ui.de.md`),
Produktivitäts-Work-Events.

## D-6: Menschen sind Akteure, nicht nur Peers

Der Daemon autorisiert per Peer-UID (`SO_PEERCRED`); jeder Web-Nutzer kommt
als UID des Gateways an. Für menschliche Urheberschaft (Kommentare,
Freigaben, Checkout) reicht das Gateway den angemeldeten Principal
(`iss`+`sub`, Rolle) in einem Header weiter, den der Daemon **nur vom
Admin-Peer des Gateways** annimmt und als `actor` speichert.
Monitor-Nutzer bleiben nur lesend. Das ist eine Sicherheitsentscheidung
(neuer Eintrag in `security-decisions.md`).

## D-7: UI

Angular bekommt einen anbieterneutralen `TicketService` gegen
`/api/v1/tickets/*` (Daemon, vom Gateway durchgereicht) und eine Ansicht
„Tickets“: Liste/Board, Detail mit Thread, Zuweisung Mensch/Agent,
Freigaben, Gate-Status, Nachweise und Live-Status je Ticket. Capabilities des
Anbieters blenden aus, was das Backend nicht kann. Das Paperclip-Panel
schrumpft auf Anbieter-Einstellungen. Der Productivity-Tab bleibt der
persönliche Tagesplan; eine Aufgabe kann auf ein Ticket verweisen.

## Konsequenzen

- Ein Codepfad für Agenten unabhängig vom Backend; Gates, Admission,
  Nachweise und Monitoring gelten für jeden Anbieter.
- Paperclip bleibt erstklassig, aber optional; DockWeave läuft auch allein.
- Mehr Code im Daemon (Dispatch, nativer Speicher); der Wrapper schrumpft.
- Risiko: Gleichwertigkeit der Arbeitsschleife. Gegenmaßnahme: die
  Wrapper-Testmatrix (`deployments/paperclip/tests`,
  `tests/integration/wrapper_e2e_test.go`) auf den Go-Dispatcher übertragen,
  bevor der Standard umgestellt wird.

## D-6a: Rollen und Rechte sind konfigurierbar

Konten kommen aus Keycloak; **Gruppen werden Rollen zugeordnet**, Rollen
**Rechten** – beides in einer Konfigurationsdatei
(`/etc/dockweave/ticket-roles.json`), nicht im Code. Rechte (Startumfang):
`ticket.read`, `ticket.create`, `ticket.edit`, `ticket.comment`,
`ticket.assign`, `ticket.approve`, `ticket.delete`, `gate.override`,
`agent.wake`, `terminal.view`, `terminal.takeover`, `provider.configure`,
`sync.run`. Standardrollen: `admin` (alles), `approver`, `author`, `reader`
(nur `ticket.read`). Der **Daemon** setzt die Rechte mit dem vom Gateway
weitergereichten Akteur durch (D-6); die UI blendet nur aus, was der Akteur
nicht darf. Unbekannte Gruppen geben keine Rechte; die Peer-UID-Rollen
(`ADMIN_UIDS`/`MONITOR_UIDS`) bleiben für CLI und lokale Werkzeuge.

## D-8: Live-Sync zwischen führendem System und Spiegel

- **Zuordnungstabelle** `ticket_links(primary_ref, mirror_ref,
  primary_version, mirror_version, synced_at)`; jedes Ticket und jeder
  Kommentar trägt seine Herkunft.
- **Auslöser:** Domänenereignisse (D-5) über eine Outbox für zeitnahe
  Übertragung, dazu alle 30 s ein Abgleich, der Änderungen
  (`updated_since`) von beiden Seiten holt. Idempotent: eine wiederholte
  Änderung bewirkt nichts.
- **Konflikte:** feldweise; die neuere Änderung gewinnt, bei Gleichstand das
  führende System; Kommentare werden nur angehängt und nach Herkunfts-ID
  zusammengeführt, nie umgeschrieben. Jeder aufgelöste Konflikt ist ein
  Audit-Ereignis `ticket.sync_conflict` mit beiden Werten.
- **Erstübernahme:** `dockweave ticket sync --initial` (auch bei der
  Einrichtung angeboten) übernimmt alle bestehenden Paperclip-Tickets,
  Kommentare und Unteraufgaben; vorhandene Gate-Läufe, Reviews und Freigaben
  werden ohne Datenverlust auf `TicketRef` (`paperclip:<id>`) umgeschlüsselt.
  Probelauf mit `--dry-run`.
- **Status:** `GET /api/v1/tickets/sync` (letzter Lauf, Verzug, Konflikte,
  Fehler); Sync-Fehler blockieren nie das führende System.

## D-9: Nativer Scheduler

Heartbeat alle **30 s** je Agent (konfigurierbar), standardmäßig **ein
Ticket gleichzeitig** je Agent (Parallelität konfigurierbar). Ein Heartbeat
ohne Arbeit bewirkt nichts; eine blockierte Admission kostet keinen Turn.

## Entscheidungen des Eigentümers (2026-11-22)

1. Mehrere Konten, Rollen über Keycloak-Gruppen → D-6a.
2. Alles, was ein Mensch tun darf, ist je Rolle konfigurierbar → D-6a.
3. Nativer Heartbeat 30 s → D-9.
4. Bestehende Paperclip-Tickets übernehmen und beide im laufenden Betrieb
   synchron halten → D-3 (führend + Spiegel) und D-8.
