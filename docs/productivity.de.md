# Produktivitätsschicht: Ziele, Tagesplan, Fokus, Nachweise

DockWeave hat eine optionale Planungsschicht für Menschen, die neben KI-Agenten
arbeiten. Sie ist deterministisch: keine Kernfunktion benötigt einen Modellaufruf.

## Architekturentscheidungen

1. **Tageswarteschlange statt Timeblocking.** Aufgaben haben einen Tag, eine
   Reihenfolge und eine Priorität, aber keine Startzeit. Fokus-Sessions messen
   die tatsächliche Zeit.
2. **Genau eine Fokus-Session pro Mensch.** Ein partieller Unique-Index
   (`status IN ('active','paused')`) erzwingt die Invariante in SQLite, sodass
   ein zweiter Client keine parallele Session öffnen kann. Agentenarbeit ist
   ausdrücklich nicht begrenzt.
3. **Messungen sind Events.** Menschliche Fokuszeit, Agentenlaufzeit,
   Containerlaufzeit, Wartezeiten, LLM- und Tool-Zeit sind getrennte
   `work_events` mit `idempotency_key`. Fehlende Werte bleiben `unknown`.
4. **Nachweise statt Behauptungen.** Eine Aufgabe wird `done`, wenn ein Mensch
   sie abschließt, aber erst `verified`, wenn ein bestandener Nachweis mit
   `verified=true` existiert. Eine Agentenaussage allein genügt nie.
5. **Budgets sind Inputs.** `input_budgets` speichern Sollwerte je Scope;
   Stunden oder Tokens gelten nie automatisch als Fortschritt.
6. **Optionale Hierarchie.** Ziele bilden einen flexiblen Baum; Aufgaben können
   mehrere Ziele referenzieren, ohne dass Aufwand doppelt gezählt wird.

## Schema (Migration 13)

`objectives`, `tasks`, `focus_sessions`, `work_events`, `evidence`,
`input_budgets`, `reviews` – Details siehe englische Fassung.

## API und CLI

`/api/v1/objectives`, `/api/v1/tasks`, `/api/v1/tasks/today`,
`/api/v1/focus/{start,pause,resume,complete,skip}`, `/api/v1/work-events`,
`/api/v1/evidence`, `/api/v1/budgets`, `/api/v1/reviews`.

Eine zweite Fokus-Session liefert `409 conflict`. Doppelte Events und Nachweise
mit gleichem Idempotency-Key liefern `200` statt eines zweiten Datensatzes.

```bash
dockweave task add "Doku schreiben" --day today --priority 70
dockweave plan
dockweave focus start tk_123
dockweave focus done --notes "fertig"
dockweave evidence add tk_123 --kind test --ref "go test ./..." --verified
```

## Sicherheit

- Keine neuen Mounts, Grants oder Container-Privilegien durch diese Schicht.
- Zieldaten liefert nur die authentifizierte API; Agenten sehen sie über
  dieselbe API mit ihrer bestehenden Rolle.
- Events und Reviews enthalten konstruktionsbedingt keine Secrets.
- Reviews und Pläne sind Daten, keine Anweisungen: der Text wird nie ausgeführt.

## Offene Punkte

Siehe `todo/productivity.json`: TUI-Fokus-Tab, Übernahme der Monitor-/Supervisor-
Events, Budget-Ist-Werte, Kalender-Logbuch, optionale Priorisierung,
Ananta-Verträge.

## Reports, Ranking und Export (implementiert)

- `GET /api/v1/work-events/derived` leitet Containerlaufzeit aus dem Audit-Log
  ab (`agent.started`/`agent.stopped`); nichts wird doppelt gespeichert.
- `POST /api/v1/work-events/batch` nimmt Monitor-Messungen idempotent an.
- `GET /api/v1/budgets/actuals` vergleicht Soll und Ist und weist verifizierte
  Ergebnisse separat aus.
- `GET /api/v1/tasks/suggest?day=` rankt deterministisch mit Begruendung.
- `GET /api/v1/calendar` liefert ein Logbuch je Tag, filterbar nach Mensch/Agent.
- `GET /api/v1/reviews/report` erzeugt den Wochen-/Monatsreport ohne LLM.
- `GET /api/v1/export/productivity` liefert das versionierte Vertragsbundle
  (`docs/api/productivity.schema.json`); Export ist nur lesend.
- TUI-Tab **Focus**: aktuelle Session, investierte Zeit, Queue, Tasten
  `s/p/c/d/x` fuer Start/Pause/Weiter/Fertig/Ueberspringen.
- Secret-artige Keys (`token`, `secret`, `password`, `api_key`, ...) werden in
  Event-Metadaten und Evidence-Payloads abgelehnt.

## Rollover, Wechsel, Aufbewahrung und Audit

- `POST /api/v1/tasks/rollover` verschiebt offene menschliche Aufgaben auf den
  Folgetag; Agentenauftraege und erledigte Aufgaben bleiben unberuehrt.
- `POST /api/v1/focus/switch` beendet die laufende Session als skipped, legt die
  alte Aufgabe zurueck in die Queue und startet die neue.
- `POST /api/v1/productivity/prune?before=YYYY-MM-DD` loescht alte Messungen
  (nach `started_at`), Nachweise, Reviews und geschlossene Fokus-Sessions.
- Jede Aenderung schreibt ein hash-verkettetes Audit-Event.
- `dockweave evidence git TASK` haengt den aktuellen HEAD-Commit idempotent an.

## Angular-Oberflaeche

Der Tab **Productivity** zeigt Tagesplan, aktive Fokus-Session mit laufender
Zeit, Anlegen von Aufgaben und Zielen, Budget-Ist, deterministischen
Review-Report und einen Rollover-Button. Er nutzt dieselben `/api/v1`-Endpunkte
wie die CLI.
