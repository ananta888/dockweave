# ADR 0003 — Ticketing als Port mit austauschbaren Adaptern (Paperclip, nativ)

- Status: vorgeschlagen
- Datum: 2026-11-22
- Entscheidungen: D-1 … D-7 (offene Fragen am Ende)
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

## D-3: Auswahl – explizit, kein automatisches Umschalten

Die Daemon-Konfiguration nennt den Anbieter (`TICKET_PROVIDER=native|paperclip`).
`auto` ist nur ein **Vorschlag bei der Einrichtung** (Paperclip erreichbar →
`paperclip`, sonst `native`) und wird einmal in die Konfiguration
geschrieben. Zur Laufzeit wird nie umgeschaltet: stilles Umschalten würde
Tickets auf zwei Speicher verteilen. Ist der gewählte Anbieter nicht
erreichbar, scheitern Ticket-Operationen geschlossen mit
`ticketing_unavailable`, die UI zeigt das, und Agenten werden nicht
angetrieben.

Ein späterer, ausdrücklicher Migrationsbefehl kann native Tickets nach
Paperclip kopieren oder zurück (`dockweave ticket migrate --to paperclip`).

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

## Offene Fragen

1. Menschen: nur der Eigentümer oder mehrere Konten mit Rollen (Autor,
   Freigeber, Leser) – über Keycloak-Gruppen?
2. Was darf ein Mensch an einem Agenten-Ticket: zuweisen, im Thread
   antworten, freigeben, ein Gate überstimmen (gibt es schon, nur Admin), das
   Terminal übernehmen?
3. Nativer Scheduler: Heartbeat-Intervall und Parallelität je Agent.
4. Soll `native` bestehende Paperclip-Tickets einmalig übernehmen
   (Migration) oder leer starten?
