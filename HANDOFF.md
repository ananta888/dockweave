# DockWeave — Session Handoff

Stand: 2026-11-19, letzter Commit `3b8ce2c` (gepusht nach `origin/main`).
Working Tree sauber, `main` == `origin/main`.

Diese Datei fasst den Stand so zusammen, dass ein anderer Agent nahtlos
weiterarbeiten kann. Sie wurde nach der Graph-/Gate-Session neu geschrieben.

---

## 1. Was das ist

`/home/krusty/dockweave` — Go-Backend + Angular-Web-UI + TUI. DockWeave führt
KI-Agenten in isolierten Docker-Containern aus und verwaltet Mounts,
Credentials und den Agenten-Lebenszyklus. Zusätzlich: Produktivitätsschicht,
OIDC-Login, privilegierter Mount-Helper (`dockweave-mountd`) und eine
Paperclip-Anbindung für Goal-Delegation inkl. **sandboxed Gate-Ausführung**.

**Toolchain:** Go ≥ 1.27, Node 24, Angular 20, Docker, systemd, SQLite
(pure Go), Keycloak 26.1.

---

## 2. Laufende Dienste & Pfade

| Dienst | Art | Zweck |
|---|---|---|
| `dockweaved` (system) | User `dockweave` (uid 997) | Daemon, API, DB, Docker |
| `dockweave-mountd` (system) | root, nur `CAP_SYS_ADMIN` | Mount-Syscalls per FD-Protokoll |
| `dockweave-web` (user) | krusty | Angular-UI + API-Proxy auf `127.0.0.1:8790` |
| `dockweave-broker` (user) | krusty | Credential-Vault/Broker, Sockets unter `~/.local/share/dockweave/broker/`, TCP `172.17.0.1:8787` |
| `dockweave-keycloak` (docker) | Container | IdP `http://127.0.0.1:8080`, Realm `dockweave` |
| Paperclip (user) | krusty | `http://127.0.0.1:3100`, Company `43b81f94-5079-4e24-8500-2e9cd78732a6` |

Sockets: `/run/dockweave/dockweaved.sock`, `dockweaved.term.sock`,
`mountd.sock` (Gruppe `dockweave`, 0660).

**Web-Zugang:** stabiler Token `dw-7607e59342eadecabb561e7d13594797e69a6b35762d1924`
in `~/.config/dockweave/web.env`. Alternativ OIDC-Login.

**Deploy-Stand (verifiziert):** `dockweaved` in `/usr/local/bin` ist der Build
mit `3b8ce2c`; `gate approve`, `gate run`, `gate runs`, `gate reviews`
funktionieren live. Web + CLI sind deployt.

---

## 3. Letzte Session — was gebaut wurde

### 3.1 2D-Graph (Angular)

Dateien:
- `ui/web/src/app/core/graph-layout-optimizer.ts` (neu)
- `ui/web/src/app/core/graph-edge-router.ts` (neu)
- `ui/web/src/app/components/graph.component.ts` (stark erweitert)
- `ui/web/e2e/graph-layout.spec.ts` (Modus `star` ergänzt)

Features:
- **Kreuzungsbewusste Nachoptimierung** nach Force/Stress: Kosten aus
  Kanten-Kreuzungen, Kanten durch Knoten, Label-Überlappungen,
  Knoten-Überlappungen, Kantenlänge. Koordinaten-Descent + Tausch
  gleichartiger Knoten + Drift-Grenze + Mindestabstände.
- **Satelliten** (Connector-/MCP-/Skill-Knoten) laufen bei Force/Stress durch
  denselben Optimierer (gecacht, damit der 5-s-Refresh nicht neu rechnet).
- **Dynamische Connectoren**: jede Kante bekommt einen eigenen Port am
  Knotenrand, Position per Strahl-Rechteck-Schnitt (`borderPoint`).
- **Kanten-Routing**: Kanten, die fremde Knoten schneiden, bekommen
  Waypoints über einen Visibility-Graph (`routeAroundNodes`).
- **Neuer Layout-Modus `star`** („Stern (Hubs)"): Hubs nach Grad, BFS-Zuordnung,
  Ringradius aus Knotengrößen, Hub-Kreis ohne Ringüberlappung.
- **Force-Gravitation** gegen ausreißende unverbundene Knoten.
- **Fit nach der Animation**, damit Force/Stress nicht winzig gezoomt bleiben.

### 3.2 Gate-Enforcement (Paperclip ↔ DockWeave)

Doku: `docs/gate-model.md` / `.de.md` (Verantwortungsgrenze, Non-Goals,
Status-Semantik, Fail-closed-Regeln). TODO: `todo/gate-enforcement.json`
(**20 done, 2 deferred**).

- **Graph → Paperclip-Sync** (`internal/gatesync`): aktivierte Gate-Connectoren
  werden beim `PUT /graph` nach `<workspace>/.paperclip/gates.json` gespiegelt.
  Merge beim ersten Sync, danach gehört die Datei DockWeave (`generatedBy`).
  Drift-Erkennung über Sidecar `.paperclip/.gates.dockweave.json`.
- **Sandbox-Runner** (`internal/gateexec` + `internal/dockerclient/sandbox.go`):
  Gates laufen in einem Wegwerf-Container (Workspace-Kopie, kein Netz, keine
  Caps, `no-new-privileges`, read-only Rootfs, `/tmp`-tmpfs, Limits, non-root).
  Tamper-Hashes über geschützte Dateien, Diff-Scope, neue Dependencies,
  Secret-Scan, Budget-Gate, Image-Digest, Phasen `pre_run/after_run/post_run`.
- **Persistenz**: Migration **15** (`gate_runs`, `gate_approvals`) und
  **16** (`gate_runs.issue_id`); `schemaVersion = 16`.
- **API**: `POST /api/v1/gates/run`, `GET /api/v1/gates/runs`,
  `POST/GET /api/v1/gates/approvals/...`, `POST/GET /api/v1/gates/reviews...`.
- **CLI**: `dockweave gate run|runs|approve|approval|review|reviews`.
- **Evidence/Audit**: jeder Lauf schreibt `gate_runs`, Evidence (`kind=gate`)
  und ein Audit-Event (`gate.run`); Reviews `kind=review` + `gate.review`;
  Freigaben `gate.approval`.
- **Review-Sync**: PASS/FAIL wird genau einmal pro Issue gespeichert;
  widersprüchliches Verdict → HTTP 409 + Audit.
- **Paperclip-Wrapper**:
  - `deployments/paperclip/gates.py` delegiert an `dockweave gate run`
    (Host-Shell nur mit `DOCKWEAVE_GATE_HOST_FALLBACK=1`).
  - `parent-close.py`: Review-PASS nur mit Attribution (zugewiesener
    Reviewer-Agent oder menschlicher Board-User); Human Approval braucht
    Paperclip-Interaction **und** `dockweave gate approve <issue>`.
  - `dockweave-run.sh`: übergibt `DOCKWEAVE_ISSUE_ID`, schreibt FAIL-Verdicts
    nach DockWeave.
- **UI**: Gate-Contract-Felder (`command`, `cwd`, `timeout_sec`,
  `runner_image`, `protected`, `allow_paths`, `deny_new_dependencies`,
  `secret_scan`, `budget`, `review_required`, `human_approval_required`),
  Paperclip-Sync-Einstellungen, Sync-Drift-Anzeige, Gate-Health-Panel
  (aufklappbare Läufe mit Ergebnissen/Diff/Budget/Evidence) und Review-Liste.

### 3.3 Behobene Bugs

- `internal/api/auth.go`: Middleware schrieb die aufgelöste Rolle nicht in den
  Request-Kontext → `gate approve` war für Admins 403. Jetzt im Kontext + Test.
- `internal/memory/memory.go`: `overrides`-Map ohne Lock → Daemon-Crash
  „concurrent map read and map write". `RWMutex` + Race-Test.

Wichtige Commits dieser Session:
`94b67a6`, `e0806cf`, `004b0fd`, `7b8a42d`, `3b8ce2c`.

---

## 4. Bauen & Deployen

```bash
cd /home/krusty/dockweave
go build ./... && go test ./... && go vet ./...

# Web (Angular in Go-Binary einbetten) + CLI
cd ui/web && npm run build && cd ../..
go build -o bin/dockweave ./cmd/dockweave
cp bin/dockweave ~/.local/bin/dockweave
systemctl --user restart dockweave-web

# Daemon (braucht root; kein passwortloses sudo)
go build -o bin/dockweaved ./cmd/dockweaved
sudo install -m 0755 bin/dockweaved /usr/local/bin/
sudo systemctl restart dockweaved
```

**Nach jedem Daemon-Restart:** kurz warten, bis der Socket wieder da ist —
ein direkter `dockweave …`-Aufruf in derselben Shell-Zeile kann sonst
„connect: no such file or directory" liefern.

```bash
sleep 1; ls -l /run/dockweave/dockweaved.sock
```

---

## 5. Gates benutzen / verifizieren

```bash
# Sandbox-Lauf (echtes Projekt)
dockweave gate run /home/krusty/projects/crystal-run --issue DOC-63 --json
dockweave gate runs --limit 5

# Review + Freigabe (admin)
dockweave gate review DOC-63 --verdict PASS --note "reviewed"
dockweave gate reviews
dockweave gate approve DOC-63 --note "ok"
dockweave gate approval DOC-63
```

Erwartung beim Crystal-Run: `tests` + `build` laufen im Container (kein Host),
`status=passed`, Image-Digest gesetzt, kein Tamper.

Tests:

```bash
go test ./...
go test -race ./internal/memory
DOCKWEAVE_GATE_INTEGRATION=1 go test ./internal/gateexec -run TestRun -v
DOCKWEAVE_GATE_INTEGRATION=1 DOCKWEAVE_GATE_WORKSPACE=/home/krusty/projects/crystal-run \
  go test ./internal/gateexec -run TestRunRealWorkspace -v
cd ui/web && npm run e2e
```

Hinweis: `internal/gateexec/runner_integration_test.go` nutzt echtes Docker und
wird ohne `DOCKWEAVE_GATE_INTEGRATION=1` übersprungen.

In der DB liegen Smoke-Datensätze `GATE-SMOKE-1` (Review PASS + Approval) und
ein realer Lauf für `crystal-run`. Unkritisch, ggf. später aufräumen.

---

## 6. Wichtige Dateien (neu/geändert)

| Bereich | Datei |
|---|---|
| Graph-Optimierer | `ui/web/src/app/core/graph-layout-optimizer.ts` |
| Kanten-Router | `ui/web/src/app/core/graph-edge-router.ts` |
| Graph-UI | `ui/web/src/app/components/graph.component.ts` |
| Gate-Sync | `internal/gatesync/gatesync.go` |
| Gate-Runner | `internal/gateexec/{runner,hash,policy,util}.go` |
| Sandbox-Container | `internal/dockerclient/sandbox.go` |
| Gate-Persistenz | `internal/model/gate.go`, `internal/persistence/gates.go` |
| Gate-API | `internal/api/gates.go` |
| Gate-CLI | `cmd/dockweave/gate.go` |
| Web-Sync-Status | `internal/webui/graph.go`, `internal/webui/server.go` |
| Paperclip | `deployments/paperclip/{gates,parent-close}.py`, `dockweave-run.sh` |
| Doku | `docs/gate-model.md` / `.de.md`, `docs/paperclip-delegation.md` / `.de.md` |
| TODO | `todo/gate-enforcement.json` |

---

## 7. Wichtige Gotchas

- **Kein passwortloses sudo.** Root-Schritte (Daemon installieren/starten)
  muss der Nutzer ausführen.
- **Paperclip-Wrapper ist fail-closed:** Ist der Daemon nicht erreichbar,
  wird `STATUS: DONE` abgelehnt. Host-Ausführung nur mit
  `DOCKWEAVE_GATE_HOST_FALLBACK=1`.
- **Gate-Image:** Default `node:22-bookworm` (lokal vorhanden). Pro Workspace
  über `runner_image` konfigurierbar. Fehlt das Image und kann nicht gepullt
  werden, schlägt der Lauf fehl (kein Host-Fallback).
- **Workspace-Kopie:** Gates laufen auf einer Kopie unter
  `/var/lib/dockweave/gates`. Das Original wird nicht verändert. Große
  `node_modules` kosten Kopierzeit (~185 MB crystal-run, Lauf ~12 s).
- **Sandbox läuft als aktueller UID** (non-root), damit erzeugte Dateien
  wieder löschbar sind. Kein Broker-/Docker-Socket, kein Netz.
- **CRLF-Rauschen:** `core.autocrlf=true`; `M`-Dateien können reine
  Zeilenenden sein. Prüfen mit `git diff --ignore-cr-at-eol`.
- **Bash-History:** `!` in doppelt gequoteten Strings → „event not found";
  `set +H`.
- **Skill-Katalog:** Daemon kann `/var/lib/dockweave/skills` nicht schreiben
  („skill catalog disabled"). Optional:
  `sudo chown -R dockweave:dockweave /var/lib/dockweave/skills`.
- **Broker-Permission:** `/home/krusty/.local/share` braucht `o+x`, sonst
  erreicht der Daemon den Broker-Socket nicht.
- **Drift:** Wird `.paperclip/gates.json` manuell verändert, zeigt die UI
  „Drift"; der nächste Sync überschreibt die Datei (bewusst).
- **Review-Attribution:** Unattributierte `VERDICT: PASS`-Kommentare zählen
  nicht (Override: `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1`).

---

## 8. Offene Punkte / nächste Schritte

1. **`todo/gate-enforcement.json`:** 20 Tasks `done`; offen nur die bewussten
   Non-Goals `gate-040` (keine zweite Dependency-DB) und `gate-041` (kein
   eigenes Issue-Board), beide `deferred`.
2. **Rollen-Durchsetzung am Web-Gateway** (monitor = nur lesen) ist weiterhin
   nicht erzwungen; Rollen werden aus OIDC abgeleitet/gespeichert. Der Daemon
   erzwingt seine Peer-Rollen bereits.
3. **Paperclip-Pipeline:** Quotas (Codex/DeepSeek) beachten; Goal-Runs im Board
   prüfen. Review-PASS braucht jetzt Attribution + DockWeave-Sync.
4. **Display-Modus-Bericht (alt):** Falls „Wechsel tut nichts" erneut kommt,
   erst Hard-Reload/Cache prüfen; der E2E deckt den Wechsel ab.
5. **Optional:** Edge-Bundling für Force/Stress; Graph-Label-Positionen
   dynamisch statt immer bei 0.5.
6. **Ananta-Import / LLM-Review-Agent:** laut Spezifikation optional, bewusst
   nicht gebaut.
7. **Smoke-Daten:** `GATE-SMOKE-1` in `gate_reviews`/`gate_approvals` kann bei
   Bedarf entfernt werden (es gibt noch keinen Delete-Endpoint).

---

## 9. Schnellzugriff

```bash
# Dienste
systemctl status dockweaved dockweave-mountd --no-pager
systemctl --user status dockweave-web dockweave-broker --no-pager

# Health
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8790/
dockweave agent list
dockweave broker status
dockweave gate runs

# Keycloak / Paperclip
docker ps --filter name=dockweave-keycloak
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/realms/dockweave/.well-known/openid-configuration
/home/krusty/.local/bin/paperclipai issue list -C 43b81f94-5079-4e24-8500-2e9cd78732a6 --json

# Logs
journalctl -u dockweaved -n 50 --no-pager
journalctl -u dockweave-mountd -n 30 --no-pager
journalctl --user -u dockweave-web -n 30 --no-pager
```
