# Goal-Delegation mit Paperclip

DockWeave betreibt den Paperclip-`process`-Adapter über
`deployments/paperclip/dockweave-run.sh`. Es gibt zwei Modi.

## Worker-Modus

```
dockweave-run.sh <dockweave-agent>          # z. B. demo-opencode
```

1. Der Wrapper liest das erste zugewiesene Inbox-Item (`/api/agents/me/inbox-lite`).
2. Identifier, Titel und vollständige Beschreibung gehen an den
   OpenAI-kompatiblen Endpoint von DockWeave (`POST /v1/chat/completions` über
   den Daemon-Unix-Socket), damit der Lauf im Live-TUI sichtbar ist.
3. Die Antwort wird als Issue-Kommentar angehängt. Der Status stammt aus dem
   `STATUS:`-Vertrag (siehe unten); ohne Statuszeile bleibt das Issue
   `in_progress`.

### STATUS-Vertrag

Jeder Worker-Prompt endet mit:

```
STATUS: DONE        - umgesetzt und geprüft
STATUS: BLOCKED     - Entscheidung oder Rechte fehlen
STATUS: IN_PROGRESS - Teilarbeit erledigt, Rest offen
```

Ein Lauf markiert Teilarbeit damit nie fälschlich als fertig.

## Orchestrierung (Lead / CEO)

```
dockweave-run.sh lead orchestrate
```

1. Gleicher Inbox-Abruf, aber der Prompt verlangt ausschließlich eine
   JSON-Aufgabenliste:

   ```json
   {"tasks": [{"title": "...", "description": "...", "role": "backend", "depends_on": []}]}
   ```

2. `plan-extract.py` extrahiert den Plan (Fenced- oder Bare-JSON, Prosa erlaubt).
3. `plan-delegate.py` legt pro Aufgabe ein Child-Issue an: Firma, Parent, Goal
   und Projekt des Lead-Issues, Rollen-Agent aus
   `~/.config/dockweave/paperclip-roles.json`, Workspace-Pfad und
   Abhängigkeitsliste in der Beschreibung; danach wird der Agent geweckt.
4. Ein erneuter Lead-Heartbeat ist idempotent: bestehende Child-Issues mit
   gleichem Titel werden wiederverwendet statt dupliziert.
5. Das Eltern-Issue bleibt `in_progress` mit einem Kommentar, der die
   Child-Issues auflistet.

## Rollen-Zuordnung

`deployments/paperclip/roles.example.json` dokumentiert das Dateiformat:

```json
{ "backend": "<paperclip-agent-id>", "frontend": "...", "reviewer": "...", "platform": "..." }
```

## Betriebsregeln

- Ein Lauf pro Agent (`maxConcurrentRuns: 1`). Rollen-Agenten teilen eine native
  Session; parallele Läufe desselben Agenten würden Antworten vermischen.
- Agent-Run-Keys gelten nur für den laufenden Agenten; zum Wecken anderer
  Agenten fällt der Wrapper auf die lokale Board-Identität zurück.
- Alle Rollen arbeiten im selben registrierten Workspace
  (`DOCKWEAVE_PAPERCLIP_WORKSPACE`, Default `/workspace`). Der Wrapper wiederholt Pfad und die Regel
  „keine parallelen Schreibzugriffe auf dieselbe Datei“ in jedem Child-Issue.

## Gates

Der Wrapper setzt vier Gates durch, bevor ein Issue als erledigt gilt:

1. **Abhaengigkeiten** - `plan-delegate.py` schreibt echte `blockedByIssueIds`;
   `wake-ready.py` weckt nur Issues, deren Blocker erledigt sind.
2. **Build/Test** - `gates.py` liest `<workspace>/.paperclip/gates.json` und
   fuehrt den Projektbefehl aus. Ein fehlgeschlagenes Gate macht aus
   `STATUS: DONE` ein `in_progress` mit der Ausgabe als Kommentar.
3. **Review** - der Reviewer antwortet mit `VERDICT: PASS|FAIL`; bei `FAIL`
   entsteht ein Rework-Issue beim Autor der ersten Abhaengigkeit.
4. **Menschliche Freigabe** - `parent-close.py` schliesst ein Lead-Issue nur,
   wenn alle Kinder fertig sind, das Review `VERDICT: PASS` hat und eine
   akzeptierte `human_only`-Interaction vorliegt. Sonst bleibt das Eltern-Issue
   `in_review` mit einer offenen Karte im Paperclip-Inbox.

### Ausfuehrung, Attribution und Freigabe

Gates werden von `dockweave gate run <workspace>` in einem kurzlebigen
Sandbox-Container ausgefuehrt, nicht auf dem Host (siehe
`docs/gate-model.de.md`). Ist der Daemon nicht erreichbar, meldet der Wrapper
ein fehlgeschlagenes Gate; Host-Ausfuehrung ist nur mit
`DOCKWEAVE_GATE_HOST_FALLBACK=1` moeglich.

- Ein Review-PASS zaehlt nur, wenn es dem zugewiesenen Agenten des
  Review-Issues oder einem menschlichen Board-User zugeordnet ist.
  Unattributierte Kommentare werden abgelehnt, ausser
  `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` ist gesetzt.
- Menschliche Freigabe braucht eine akzeptierte Paperclip-`human_only`-
  Interaction **und** `dockweave gate approve <issue>` (admin-Rolle,
  auditiert als `gate.approval`).

Der 2D-Graph in der Angular-App speichert dieselben Gate-Contracts. Frueher
lagen sie in `localStorage`; `GET/PUT /graph` am Web-Gateway persistiert das
gesamte Dokument in `~/.local/share/dockweave/graph.json`, der Daemon bietet
dasselbe Dokument als `GET/PUT /api/v1/graph` fuer Nicht-Browser-Clients.

## Graph → Paperclip Gate-Sync

Aktivierte `kind: "gate"`-Connectoren koennen in den Projekt-Workspace
gespiegelt werden. Das Graph-Dokument traegt dazu einen `paperclip`-Block:

```json
{
  "paperclip": {
    "workspace": "/srv/projects/app",
    "syncGates": true
  }
}
```

Ein Gate-Contract kann zusaetzlich ausfuehrbare Felder tragen:

```json
{
  "gate_id": "build",
  "command": "npm run build",
  "expression": "command == 0",
  "on_true": "allow",
  "on_false": "rework",
  "reason_true": "Build gruen",
  "reason_false": "Build rot, bitte nachbessern",
  "max_age_sec": 3600,
  "cwd": ".",
  "timeout_sec": 300,
  "review_required": true,
  "human_approval_required": true
}
```

Bei jedem `PUT /graph` ruft das Web-Gateway `internal/gatesync` auf und
schreibt `<workspace>/.paperclip/gates.json` atomar:

- `command` wird ein Eintrag in `gates[]` (`when` default `after_run`, `cwd`
  default `.`, `timeout_sec` default 300)
- `review_required` setzt `review.required` (Default `issue_match: "review"`)
- `human_approval_required` setzt `human_approval.required`

Sicherheitsregeln:

- geschrieben wird nur unterhalb der Admin-Browser-Roots (`/home`, `/srv`,
  `/opt/projects`, `/var/lib/dockweave`); der Workspace muss ein existierendes
  absolutes Verzeichnis sein
- nur `<workspace>/.paperclip/gates.json` wird geschrieben, atomar
- eine handgepflegte Datei wird nie ersetzt, wenn der Graph keine
  ausfuehrbaren Gates hat; der erste Sync mit ausfuehrbaren Gates **merged**
  hinein (Gates, die der Graph nicht kennt, bleiben erhalten; bei gleicher ID
  gewinnt der Graph); von DockWeave erzeugte Dateien (`generatedBy:
  "dockweave-graph"`) werden geleert, wenn das letzte Gate entfernt wird
- ein fehlgeschlagener Sync laesst das Graph-Speichern nie scheitern: das
  Ergebnis kommt als `gates` in der `PUT /graph`-Antwort und wird in der UI
  angezeigt
- dasselbe Speichern leitet die Pipeline-Kanten ab und uebertraegt sie an den
  Daemon; das Ergebnis kommt als `pipeline_sync` (`pushed`, `edges`,
  `warnings`, `error`)

Der Sync laeuft am Web-Gateway, also auf dem Pfad der Angular-App.
`PUT /api/v1/graph` am Daemon speichert nur das Dokument.

## Pipeline: Agent A → Gate → Agent B

Ein Gate-Connector mit Ziel-Agent (`targetAgentId`, als `proceed`-Kante
gezeichnet) gibt Agent B erst frei, wenn die Arbeit von Agent A das Gate auf
dem aktuellen Workspace-Stand bestanden hat (ADR 0002,
`docs/gate-model.de.md` „Pipelines“).

1. **A arbeitet.** Vor jedem Turn führt `dockweave-run.sh` die
   `pre_run`-Gates aus (falls vorhanden) und fragt
   `pipeline.py precheck <agent>`; A hat keine eingehende Kante und ist frei.
2. **A meldet `STATUS: DONE`.** Der Wrapper lässt die
   `after_run`/`post_run`-Gates in der Sandbox mit A als Upstream laufen
   (`gates.py run … --phase after_run,post_run`,
   `DOCKWEAVE_UPSTREAM_AGENT`). Der Lauf speichert den
   Workspace-Fingerabdruck.
3. **Routing.** `pipeline.py route --upstream <A>` wertet die Admission
   jedes Downstream-Agenten von A aus:
   - frei (`allow`) → B in Paperclip wecken; `wake:<agent>` weckt stattdessen
     den genannten Agenten
   - fehlgeschlagen mit `rework` → genau ein Nacharbeit-Issue für A pro
     Gate-Lauf (Titel-Kennung `[gate <id> / run <lauf-id>]`, idempotent)
   - `deny` oder ein anderer Grund (`stale`, `no_run` …) → im Kommentar
     gemeldet, niemand wird geweckt
4. **B wird geweckt.** Sein Wrapper fragt zuerst die Admission ab; hat sich
   der Workspace seit dem PASS geändert (`stale`) oder ist das Gate
   gescheitert, wird B am Issue als gesperrt gemeldet (einmal pro Grund-Satz;
   das Issue geht zurück auf `todo`, nie `blockedBy`) und kein Modell-Turn
   verbraucht. Wird die Vorabprüfung übersprungen, antwortet der Daemon
   trotzdem mit `409 pipeline_blocked`, und der Wrapper behandelt das genauso.

Die Zuordnung Paperclip ↔ DockWeave-Agent für das Wecken ist explizit:
`DOCKWEAVE_PAPERCLIP_AGENT_MAP` zeigt auf eine JSON-Datei
`{"<dockweave-agent-id-oder-name>": "<paperclip-agent-id>"}`. Nicht
zugeordnete Agenten werden als `UNMAPPED` gemeldet und nie geraten.

Notausgang für Admins: `dockweave pipeline override <B> --gate <id> --reason …`
(einmalig, 15 Minuten, auditiert). Overrides überbrücken nur fehlende,
gescheiterte oder veraltete Nachweise; `tamper` und ein bewusstes
`deny`/`rework` nach einem PASS (`denied`) werden nie freigegeben.

### Schutz je Wrapper-Modus

Welche Schritte `dockweave-run.sh` tatsächlich ausführt, hängt von den ersten
beiden Argumenten ab. Tests: `deployments/paperclip/tests/test_wrapper_modes.py`.

| Schritt | Worker (`<agent>`) | Lead (`<agent> orchestrate`) | Legacy (`dockweave-<id> …`) |
|---|---|---|---|
| Offene Blocker (`blocked-check.py`) | ja | nein | ja |
| Pipeline-Admission (`pipeline.py precheck`) | ja | ja | **nein** |
| `pre_run`-Gates | ja, wenn `DOCKWEAVE_HOST_WORKSPACE` gesetzt ist | nein | **nein** |
| Daemon-Durchsetzung am Endpoint (`409`) | ja | ja | **nein** (`docker exec` umgeht den Daemon) |
| `after_run`/`post_run`-Gates bei `STATUS: DONE` | ja; ohne `DOCKWEAVE_HOST_WORKSPACE` scheitert das Gate und das Issue bleibt offen | nein (Kinder werden gegated) | **nein** |
| Pipeline-Routing | ja, wenn `DOCKWEAVE_HOST_WORKSPACE` gesetzt ist; ein Routing-Fehler lässt das Gate-Ergebnis stehen und schreibt einen Wiederholbefehl ins Issue | nein | **nein** |

Der Legacy-Pfad ist **nicht pipeline-geschützt** und gibt bei jedem Lauf eine
Warnung aus. Er bleibt nur aus Kompatibilitätsgründen erhalten. Migration: Der
Containername ist `dockweave-<agent-id>`; das erste Argument
`dockweave-<agent-id>` durch `<agent-id>` (oder den Agentennamen) ersetzen und
die Harness-Argumente weglassen. Der Agent läuft dann wie jeder Worker über
den Endpoint.

