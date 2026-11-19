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
| Review-PASS-Attribution | Transport (Kommentare/Interactions) | Audit-Datensatz, Identitätsprüfung |
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

### Tamper- und Policy-Prüfungen

- geschützte Dateien (`gates.json`, Manifeste, Lockfiles, Test-Configs plus
  `protected`) werden vor und nach jedem Gate gehasht. Eine Änderung lässt den
  Lauf fehlschlagen.
- nach dem Lauf werden Original und Kopie verglichen (Dependency-/Build-Bäume
  ignoriert). Optionale Policies:
  - `allow_paths`: geänderte Dateien müssen dazu passen
  - `deny_new_dependencies`: neue Einträge in `package.json` lassen den Lauf
    fehlschlagen
  - `secret_scan`: geänderte Dateien werden auf gängige Credential-Muster
    geprüft
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
`docs/paperclip-delegation.de.md`.

## Review und Freigabe

- Ein Review-PASS zählt nur, wenn es dem zugewiesenen Agenten des
  Review-Issues oder einem menschlichen Board-User zugeordnet ist.
  Unattributierte Board-Kommentare werden abgelehnt, außer
  `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1` ist explizit gesetzt.
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
- Tamper-, Scope-, Dependency- oder Secret-Verstoß: der Lauf schlägt fehl,
  auch wenn das Kommando mit 0 endete.
