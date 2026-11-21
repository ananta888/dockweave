# Gate-Modell: Paperclip und DockWeave

Dieses Dokument legt die Verantwortungsgrenze zwischen Paperclip und
DockWeave für Gates, Reviews und Freigaben fest. Es ist die Referenz für
`todo/archive/gate-enforcement.json`.

## Prinzip

- **Paperclip** besitzt die Planung: Issues, Parent/Child-Hierarchie,
  Abhängigkeiten, Zuweisung, Wake-Reihenfolge und das Board.
- **DockWeave** besitzt die Ausführung: Container-Grenzen, Mounts, Egress,
  Credentials, Budgets, Identität/Audit und vertrauenswürdige Gate-Ausführung.

Ein Gate, das wissen muss, *was ein Container getan hat*, kann Paperclip allein
nicht leisten; ein Gate, das wissen muss, *was das Team geplant hat*, gehört zu
Paperclip.

## Verantwortungsaufteilung

| Bereich | Paperclip | DockWeave |
|---|---|---|
| Issue-Hierarchie, `parentId` | ja | nein |
| `blockedByIssueIds`, Wake-Reihenfolge | ja | konsumiert nur |
| Board, Kommentare, Zuweisung | ja | nein |
| Build/Test-Gate-Definition | liegt in `.paperclip/gates.json` | kanonisch ist der 2D-Graph (gespiegelt) |
| Build/Test-Ausführung | nein (delegiert) | Sandbox-Container |
| Mounts, Read-only-Binds, Egress, Capabilities | nein | ja |
| Broker-Credentials und LLM-Budgets | nein | ja |
| Tamper-Erkennung von Gate-/Testdateien | nein | ja |
| Diff-Scope-, Dependency- und Secret-Policy | nein | ja |
| Review-PASS-Attribution | Transport (Kommentare/Interactions), Zuweisungsprüfung | Audit-Datensatz (admin-Peer), Zuständige als Metadatum |
| Rolle der menschlichen Freigabe | Transport (Interaction) | OIDC-Rolle + Audit |
| Audit-Hash-Kette | nein | ja |

## Was Paperclip-Gates nicht können

Die hier genutzte Integration (`gates.py`, `parent-close.py`,
`dockweave-run.sh`) hat diese strukturellen Grenzen:

1. `when` ist praktisch `after_run`; zur Laufzeit wird nichts erzwungen.
2. `gates.json` liegt im beschreibbaren Workspace: der Agent kann seine
   eigenen Prüfungen ändern.
3. Kommandos auf dem Host auszuführen führt agenten-kontrollierten Code mit
   den Rechten des Host-Benutzers aus.
4. Kein Wissen über Mounts, Egress, Capabilities, Broker-Credentials oder
   exklusive Workspace-Schreiber.
5. Keine LLM-Budget-/Quota-Kenntnis.
6. `VERDICT: PASS` ist Freitext; Paperclip erzwingt keine Attribution.
7. `human_only`-Interactions hängen an Paperclip-Board-Usern, nicht an
   DockWeave-Rollen.
8. Keine Integritätsnachweise (Hashes, Image-Digests, Diff-Scope).

## DockWeave-Gate-Ausführung

`dockweave gate run <workspace>` (Daemon-Endpoint `POST /api/v1/gates/run`)
führt die Workspace-Konfiguration in einer kurzlebigen Sandbox aus:

- der Workspace wird in ein privates Verzeichnis unter
  `/var/lib/dockweave/gates` **kopiert**; das Original wird nie in die Sandbox
  gemountet
- Container: kein Netz, keine Capabilities, `no-new-privileges`, read-only
  Rootfs mit `/tmp`-tmpfs, Memory-/CPU-/PID-Limits, hartes Timeout
- kein Broker-Socket, kein Docker-Socket, keine Agent-Credentials
- ein Container pro Gate, Logs auf die letzten 40 Zeilen gekürzt
- der Image-Digest wird für Reproduzierbarkeit festgehalten (`runner_image`)

### Phasen-Semantik

Der Runner akzeptiert die `when`-Werte `pre_run`, `after_run` und `post_run`
und führt sie **nacheinander in einem einzigen Durchlauf** innerhalb eines
`gate run`-Aufrufs aus – sie ordnen die Gates, sie planen keine getrennten
Auslösezeitpunkte. Im Paperclip-Wrapper ist heute nur `after_run` tatsächlich
verdrahtet (`gates.py` / `dockweave-run.sh` führen die Gates nach dem
Agentenlauf aus). Bis es einen echten Pre-/Post-Run-Hook gibt, verhalten sich
`pre_run` und `post_run` genau wie `after_run`; eine unbekannte Phase ist ein
Konfigurationsfehler.

### Retention

`dockweave gate prune` (Daemon-Endpoint `POST /api/v1/gates/prune`, nur admin)
löscht `gate_runs`, die älter als ein Stichtag sind (Default 30 Tage), behält
aber immer die neuesten Läufe pro Workspace (Default 20) und begrenzt so das
Wachstum der Audit-Tabelle. Die neuesten Läufe werden über *alle* Läufe eines
Workspaces bestimmt: gelöscht wird nur, was älter als der Stichtag **und**
nicht unter den neuesten N ist. Ein Workspace ohne aktuelle Läufe behält
daher seine neuesten N alten Läufe.

### Gate-Ausdrücke

Ein Gate kann eine `expression` sowie optional `on_true`/`on_false`-Texte
tragen. Nach dem Kommando wertet der Runner den Ausdruck deterministisch aus:

| Variable | Typ | Bedeutung |
|---|---|---|
| `command`, `exit` | int | Exit-Code des Gate-Kommandos |
| `budget` | float | Tageskosten in USD aus dem Usage-Snapshot des Brokers |

Operatoren: `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `||`, `!`, unäres `-` und
Klammern; ein einzelner Wert gilt als wahr, wenn er ungleich 0 ist.

- Ohne Ausdruck entscheidet der Exit-Code (0 = bestanden).
- Mit Ausdruck **ersetzt dessen Ergebnis das Exit-Code-Urteil**:
  `command != 0` kann ein erwartetes Scheitern ausdrücken. `on_true` wird bei
  PASS zum Grund, `on_false` bei FAIL.
- Ein ungültiger Ausdruck beendet das Gate mit Status `error`.
- `budget` ist fail-closed: Ist für den Workspace kein Budget konfiguriert
  oder war der Broker nicht abfragbar, endet ein Ausdruck, der `budget`
  verwendet, mit Status `error`, statt gegen 0 zu vergleichen.
- Ein Ausdruck ohne `command` erzeugt kein Gate; der Graph-Sync gibt eine
  Warnung aus.

### Budget

`budget` (`grant_id` plus `max_requests_day`/`max_tokens_day`/
`max_cost_day_usd`) wird vor und nach den Gates gegen den Credential-Broker
geprüft. Ist der Broker nicht konfiguriert oder nicht erreichbar, **scheitert**
der Lauf standardmäßig. `"on_unavailable": "warn"` stuft diesen Fall zu einer
Warnung herab; der Usage-Snapshot wird dann als `unavailable` markiert und die
Ausdrucksvariable `budget` bleibt unbenutzbar.

### Tamper- und Policy-Prüfungen

- geschützte Dateien (`gates.json`, Manifeste, Lockfiles, Test-Configs plus
  `protected`) werden vor und nach jedem Gate gehasht. Eine Änderung lässt den
  Lauf fehlschlagen.
- nach dem Lauf werden Original und Kopie verglichen (Dependency-/Build-Bäume
  ignoriert). Optionale Policies:
  - `allow_paths`: geänderte Dateien müssen dazu passen
  - `deny_new_dependencies`: eine neue Abhängigkeit in `package.json`,
    `go.mod`, `requirements.txt`, `pyproject.toml` (PEP 621 und Poetry) oder
    `Cargo.toml` (inklusive `[dependencies.<name>]` und `target`-Tabellen)
    lässt den Lauf fehlschlagen. Python-Namen werden nach PEP 503
    normalisiert, sodass Versionssprünge, Extras und Environment-Marker
    nicht als neue Abhängigkeit zählen.
  - `secret_scan`: geänderte Dateien werden auf bekannte Credential-Formen
    geprüft (AWS, GitHub, OpenAI, Slack, Google, Stripe, GitLab, npm, JWT,
    Bearer-Tokens, Private Keys …). Passt keine, markiert eine
    Shannon-Entropie-Prüfung kompakte Tokens mit hoher Entropie. Lockfiles
    (`go.sum`, `package-lock.json`, `yarn.lock`, `Cargo.lock`, …) und
    Content-Hash-Notationen (`h1:`, `sha512-` …) sind von der
    Entropie-Prüfung ausgenommen, nicht vom Muster-Scan.
- jeder Lauf landet in `gate_runs` und in der Audit-Hash-Kette (`gate.run`).

## Konfiguration

`<workspace>/.paperclip/gates.json`:

```json
{
  "version": 1,
  "review": {"required": true, "issue_match": "review"},
  "human_approval": {"required": true},
  "runner_image": "node:22-bookworm",
  "protected": ["vitest.config.ts"],
  "allow_paths": ["src/", "tests/"],
  "deny_new_dependencies": true,
  "secret_scan": true,
  "budget": {"grant_id": "…", "max_tokens_day": 200000},
  "gates": [
    {"id": "tests", "when": "after_run", "command": "npm test", "cwd": ".", "timeout_sec": 300}
  ]
}
```

Dieselben Felder leitet `internal/gatesync` aus dem 2D-Graphen ab; siehe
`docs/paperclip-delegation.de.md`. Ein Gate-Connector kann im Contract einen
eigenen `workspace` setzen; das Web-Gateway schreibt dann eine `gates.json`
pro Ziel-Workspace. Ziele werden nach kanonischem Pfad gruppiert (Symlinks
und verschiedene Schreibweisen desselben Verzeichnisses werden
zusammengeführt). Entfernt ein Speichervorgang alle Gates aus einem
Workspace, den der vorherige Graph-Stand bedient hat, wird dessen von
DockWeave erzeugte `gates.json` geleert; handgepflegte Configs werden nie
angefasst. Gates ohne Ziel-Workspace werden als Warnung gemeldet. Eine
stabile `gate_id` im Contract hält die Gate-Identität über Umbenennungen
hinweg.

## Review und Freigabe

- Ein Review-PASS zählt nur, wenn es dem zugewiesenen Agenten des
  Review-Issues oder einem menschlichen Board-User zugeordnet ist.
  Unattributierte Board-Kommentare werden abgelehnt, außer
  `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` ist explizit gesetzt. Diese
  Zuweisungsprüfung erfolgt im Paperclip-Wrapper.
- Das Festhalten des Verdicts in DockWeave (`dockweave gate review`,
  `POST /api/v1/gates/reviews`) erfordert die admin-Rolle; der
  authentifizierte Peer wird als `actor` gespeichert. Der Wrapper kann die
  zuständige Identität mitgeben (`--assignee` / `assigned_reviewer`);
  DockWeave kann die Paperclip-Zuweisung nicht prüfen und speichert sie als
  **unverifiziertes Metadatum**.
- Jedes akzeptierte Verdict wird genau einmal als Review-Evidence
  (`kind=review`, ein Idempotency-Key pro Issue) festgehalten. Ein späteres
  widersprüchliches Verdict wird als Konflikt (`409`) abgelehnt und als
  `gate.review` auditiert.
- Menschliche Freigabe braucht **beides**: eine akzeptierte
  `human_only`-Interaction in Paperclip **und** eine DockWeave-Freigabe:
  `dockweave gate approve <issue>` (admin-Rolle, auditiert als
  `gate.approval`). Die Paperclip-Interaction bleibt Transport/Sichtbarkeit.

## Status-Semantik

- `blockedBy` (Paperclip) ist eine Abhängigkeitskante.
- DockWeave-Laufzeitfehler sind **keine** Abhängigkeiten und dürfen nicht als
  `blockedBy` geschrieben werden; sie gehören zum Gate-Lauf/Agentenstatus.
- Ein Paperclip-Status `blocked` kann beides bedeuten; Reports dürfen sie
  nicht vermischen.

## Non-Goals

- DockWeave führt keine zweite Dependency-Datenbank. Issue-Reihenfolge bleibt
  bei Paperclip.
- DockWeave baut kein Issue-Board. Es liefert nur aggregierte Gate-Health und
  Evidence.

## Fail-closed-Regeln

- Fehlende `dockweave`-CLI oder nicht erreichbarer Daemon: der Wrapper meldet
  ein fehlgeschlagenes Gate; er führt **nicht** still Kommandos auf dem Host
  aus. Host-Ausführung erfordert `DOCKWEAVE_GATE_HOST_FALLBACK=1`.
- Fehlendes Image: der Lauf schlägt fehl; kein Host-Fallback.
- Budget konfiguriert, aber Broker fehlt oder ist nicht erreichbar: der Lauf
  schlägt fehl, außer `on_unavailable` ist `warn`.
- Ungültiger Gate-Ausdruck oder ein Ausdruck, der ein nicht verfügbares
  `budget` braucht: das Gate endet mit Status `error`.
- Tamper-, Scope-, Dependency- oder Secret-Verstoß: der Lauf schlägt fehl,
  auch wenn das Kommando mit 0 endete.
