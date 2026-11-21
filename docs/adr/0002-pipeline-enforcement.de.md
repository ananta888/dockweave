# ADR 0002 — Durchsetzung der Pipeline (Agent A → Gate → Agent B)

- Status: angenommen
- Datum: 2026-11-21
- Entscheidungen: D-1 (Durchsetzungspunkt), D-2 (Bedeutung von `on_true`/`on_false`)
- Verfolgt in: `todo/archive/gate-pipeline.json` (GP-014, GP-015)

## Kontext

Im 2D-Graphen lässt sich ein Gate-Connector mit einem Downstream-Agenten
verbinden (`proceed`-Kante, `targetAgentId`). Ziel: Agent B arbeitet nur,
wenn das deterministische Gate hinter Agent A bestanden ist. Bisher war die
Kante nur gezeichnet: Der Graph liegt beim Web-Gateway
(`~/.local/share/dockweave/graph.json`), der Daemon las ihn nie, und
`on_true`/`on_false` waren Freitext, der zum Grund des Urteils wurde.

Paperclip spricht B über `deployments/paperclip/dockweave-run.sh` an; das
Skript ruft den OpenAI-kompatiblen Endpunkt `POST /v1/chat/completions` des
Daemons auf (`internal/api/openai.go`). Bs Container läuft in der Regel
weiter; Paperclip-Arbeit geht nicht über `agent start`.

## D-1: Wo wird ein blockierter Downstream-Agent gestoppt?

| Option | Beschreibung | Bewertung |
|---|---|---|
| **A** | Der Daemon verweigert am Ausführungspunkt `POST /v1/chat/completions` (und am herdr-Prompt). `dockweave-run.sh` fragt vorher zusätzlich `GET /api/v1/pipelines/admission` ab | Nicht umgehbar durch direkten Endpunkt-Aufruf; die Vorabprüfung spart einen Modell-Turn und liefert Paperclip eine saubere Meldung |
| B | Nur der Wrapper prüft | Schnell, aber umgehbar, sobald jemand den Endpunkt direkt nutzt |
| C | Verweigerung beim Container-Start (`agent start`) | Wirkungslos: Pipeline-Arbeit läuft nicht über den Container-Start |

**Entscheidung: A.**

- **Blockiert:** jeder programmatische Prompt an einen Agenten mit
  eingehenden Pipeline-Kanten: `POST /v1/chat/completions` (mit und ohne
  Streaming) und `POST /api/v1/agents/{id}/herdr/prompt`. Die Prüfung läuft,
  nachdem der Agent aufgelöst ist und bevor irgendetwas an ihn geht. Eine
  blockierte Anfrage erhält `409` mit einem OpenAI-kompatiblen Fehlerobjekt
  (`code`/`type` `pipeline_blocked`, `gate_id`, `action`, `reasons[]`) und
  die vollständige Admission.
- **Nicht blockiert:** interaktive Terminal-Sitzungen (`/ws/term`, TUI,
  Web-Terminal) — ein Mensch am Terminal ist keine Pipeline-Arbeit. Auch
  Start/Stopp von Agenten wird nicht blockiert. Agenten ohne eingehende
  Kanten sind nie betroffen.
- **Admin-Override:** `POST /api/v1/pipelines/override` (admin,
  Pflichtbegründung, kurze Gültigkeit, standardmäßig einmalig, auditiert als
  `pipeline.override` und `pipeline.override_used`). Ein Override hebt nie
  ein `tamper`-Urteil auf.
- **Fail-closed:** eine nicht lesbare Datenbank, ein unbekanntes Gate, ein
  fehlender Lauf, ein fehlender oder nicht berechenbarer
  Workspace-Fingerabdruck oder jeder andere interne Fehler blockiert mit
  strukturiertem Grund, nie wird durchgelassen. Der Wrapper behandelt einen
  nicht erreichbaren Daemon oder eine ungültige Antwort als blockiert.

### Admission-Regel

Für Agent B muss jede Kante `Gate → B` erfüllt sein. Pro Kante entscheidet
der neueste *relevante* Gate-Lauf im Workspace der Kante (ein Lauf, der das
Gate ausgeführt hat, ein Tamper-Lauf oder ein Lauf über alle Phasen):

| Code | Bedeutung |
|---|---|
| `ok` | Gate bestanden, Lauf bestanden, Fingerabdruck passt, nicht abgelaufen |
| `no_run` | noch kein relevanter Lauf |
| `failed` | Gate oder Lauf fehlgeschlagen (auch Policy-Verstöße) |
| `error` | Gate oder Lauf mit Fehler (auch Setup-Fehler) |
| `stale` | Workspace hat sich seit dem bestandenen Lauf geändert, oder der Lauf hat keinen Fingerabdruck (Läufe vor Migration 17) |
| `expired` | der bestandene Lauf ist älter als `max_age_sec` |
| `tamper` | geschützte Gate-Dateien wurden während des Laufs geändert (nie per Override aufhebbar) |
| `fingerprint_unavailable` | der aktuelle Fingerabdruck ließ sich nicht berechnen (Limits, Rechte) |
| `unknown_gate` | ein vollständiger Lauf enthielt das Gate nicht (nicht konfiguriert) |
| `denied` | Gate bestanden, aber `on_true` gibt nicht frei (`deny`/`rework`); nie per Override aufhebbar |
| `internal` | Fehler im Evaluator |
| `override` | durch einen aktiven Admin-Override freigegeben |

**Frische** belegt ein Workspace-Fingerabdruck: SHA-256 über die sortierten
relativen Pfade und Inhalts-Hashes des Workspaces, mit denselben ignorierten
Bäumen wie der Gate-Diff (`.git`, `node_modules`, `dist`, `target` …). Der
Runner berechnet ihn über die Sandbox-Kopie vor dem ersten Gate, die
Admission über den echten Workspace. Grenzen (100 000 Dateien, 2 GiB, 60 s)
begrenzen den Aufwand; bei Überschreitung gilt `fingerprint_unavailable`
(blockiert).

### Datenfluss

1. Das Gateway speichert den Graphen und leitet die Kanten ab
   (`gatesync.DerivePipeline`).
2. Es überträgt sie mit der neuen Graph-Revision per `PUT /api/v1/pipelines`
   (admin). Der Daemon prüft Agent-IDs und kanonische Workspaces unter den
   erlaubten Wurzeln (AGENTS.md-Regel 4), ersetzt die Kanten atomar und lässt
   nie eine ältere Revision eine neuere überschreiben (eine neu angelegte
   Graph-Datei wird mit `reset` übertragen). Ein Fehlschlag verliert den
   Graphen nicht; die Antwort enthält `pipeline_sync.error`, die UI zeigt ihn.
3. `dockweave-run.sh` lässt A arbeiten, danach die Gates
   (`gate run --upstream-agent`).
4. Vor Bs Turn fragt der Wrapper die Admission ab; der Daemon setzt sie am
   Endpunkt nochmals durch.

## D-2: Was bedeuten `on_true`/`on_false`?

| Option | Beschreibung |
|---|---|
| **A** | Aktionen mit festem Vokabular; Texte wandern in `reason_true`/`reason_false` |
| B | Reiner Begründungstext (alter Stand); Weiterleiten nur implizit über die proceed-Kante |

**Entscheidung: A.**

| Aktion | Wirkung |
|---|---|
| `allow` | B darf arbeiten (Default für `on_true`) |
| `deny` | B bleibt blockiert (Default für `on_false`); bei `on_true` meldet die Kante `denied` |
| `wake:<agent-id>` | freigeben, und der Wrapper weckt den genannten Agenten in Paperclip |
| `rework` | B bleibt blockiert; der Wrapper legt pro Gate-Lauf genau ein Nacharbeit-Issue für den Upstream-Agenten an |

- `reason_true`/`reason_false` sind die Begründungen im Gate-Ergebnis.
- **Zulässige Kombinationen (21.11.2026, RF-004):** `on_true` akzeptiert jede
  Aktion. `on_false` akzeptiert nur `deny` (Standard) und `rework`: Ein
  gescheitertes Gate gibt nie Arbeit frei, daher lehnt die Daemon-API `allow`
  und `wake:` ab (400), und der Graph-Sync behandelt sie mit Warnung als
  `deny`. Die Admission liefert Aktion und Begründung für beide Zweige, auch
  für einen frischen PASS mit `on_true=rework` oder `deny` (Kanten-Code
  `denied`).
- **Migration:** bestehende Contracts laufen ohne Änderung weiter. Ein Wert,
  der keine Aktion ist, gilt als alter Begründungstext (er wird zum Grund,
  sofern `reason_*` nicht gesetzt ist), und die Default-Aktion greift; der
  Graph-Sync meldet eine Warnung. Die Daemon-API lehnt Nicht-Aktionen ab.
- **Wer führt aus:** Der Daemon entscheidet nur und liefert die zutreffende
  Aktion in der Admission (`action` je Kante und gesamt). Paperclip-seitige
  Aktionen (`wake`, `rework`) führt der Wrapper aus; die Paperclip-IDs kennt
  er über eine explizite Zuordnung (`DOCKWEAVE_PAPERCLIP_AGENT_MAP`).

## Konsequenzen

- Neue Tabellen `pipeline_edges`, `pipeline_overrides`; `gate_runs` erhält
  `workspace_fingerprint`, `upstream_agent_id`, `phases` (Migrationen 17/18).
- Ein gegateter Agent kostet pro Prompt einen Workspace-Fingerabdruck. Große
  Workspaces sollten Build-Ausgaben in ignorierten Verzeichnissen halten.
- Das Web-Gateway muss für die Übertragung als admin-Peer laufen; sonst
  meldet das Speichern `pipeline_sync.error`, und der Daemon behält die
  bisherigen Kanten (die Durchsetzung wird durch eine fehlgeschlagene
  Übertragung nie still schwächer).
- Sicherheitsentscheidung als SD-020 in `docs/security-decisions.md`.
