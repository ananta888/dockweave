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
- Alle Rollen arbeiten im selben registrierten Workspace (Default
  `/workspace/crystal-run`). Der Wrapper wiederholt Pfad und die Regel
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

Der 2D-Graph in der Angular-App speichert dieselben Gate-Contracts. Frueher
lagen sie in `localStorage`; `GET/PUT /graph` am Web-Gateway persistiert das
gesamte Dokument in `~/.local/share/dockweave/graph.json`, der Daemon bietet
dasselbe Dokument als `GET/PUT /api/v1/graph` fuer Nicht-Browser-Clients.
