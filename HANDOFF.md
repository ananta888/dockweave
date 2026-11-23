# DockWeave — Session Handoff

Stand: 2026-11-23, Branch `main`. Ticketing (ADR 0003, Track
`todo/todo.ticketing-2026-11-22.json`) ist bis auf die Abnahme TK-015
umgesetzt; davor Gate-Pipeline A → Gate → B (`todo/archive/gate-pipeline.json`).

Diese Datei fasst den Stand so zusammen, dass ein anderer Agent nahtlos
weiterarbeiten kann. Abschnitt 3.00 beschreibt Ticketing und die
Bereinigung vom 2026-11-23, 3.0 die Pipeline-Session; 3.1–3.4 sind älter
(Graph/Gates, Security-Hardening).

> **Regel:** Keine Zugangsdaten (Tokens, Secrets, Passwörter,
> installationsspezifische IDs) in diese Datei — das Repo ist öffentlich
> (AGENTS.md, Regel 9).

---

## 1. Was das ist

`~/dockweave` — Go-Backend + Angular-Web-UI + TUI. DockWeave führt
KI-Agenten in isolierten Docker-Containern aus und verwaltet Mounts,
Credentials und den Agenten-Lebenszyklus. Zusätzlich: Produktivitätsschicht,
OIDC-Login, privilegierter Mount-Helper (`dockweave-mountd`) und eine
Paperclip-Anbindung für Goal-Delegation inkl. **sandboxed Gate-Ausführung**.

**Toolchain:** Go 1.27.3 (go.mod, GOTOOLCHAIN=local), Node 24, Angular 20, Docker, systemd, SQLite
(pure Go), Keycloak 26.1.

---

## 2. Laufende Dienste & Pfade

| Dienst | Art | Zweck |
|---|---|---|
| `dockweaved` (system) | User `dockweave` (uid 997) | Daemon, API, DB, Docker |
| `dockweave-mountd` (system) | root, nur `CAP_SYS_ADMIN` | Mount-Syscalls per FD-Protokoll |
| `dockweave-web` (user) | krusty | Angular-UI + API-Proxy auf `127.0.0.1:8790` |
| `dockweave-broker` (user) | krusty | Credential-Vault/Broker, Sockets unter `~/.local/share/dockweave/broker/`, TCP `172.17.0.1:8787` |
| `dockweave-keycloak` (docker) | Container | IdP nur `127.0.0.1:8080`, Realm `dockweave`; Zugangsdaten in `~/.config/dockweave/keycloak*.env` |
| Paperclip (user) | krusty | `http://127.0.0.1:3100`, Company-ID lokal (`paperclipai company list`) |

Sockets: `/run/dockweave/dockweaved.sock`, `dockweaved.term.sock`,
`mountd.sock` (Gruppe `dockweave`, 0660).

**Web-Zugang:** Gateway-Token in `~/.config/dockweave/web.env`
(`DOCKWEAVE_WEB_TOKEN`, Rechte 0600) oder OIDC-Login. **Zugangsdaten
(Tokens, Secrets, IDs) gehören nie in diese Datei — das Repo ist öffentlich.**
Der früher hier eingetragene Token wurde am 2026-11-23 rotiert.

**Deploy-Stand (verifiziert):** `dockweaved` in `/usr/local/bin` ist der Build
mit `3b8ce2c`; `gate approve`, `gate run`, `gate runs`, `gate reviews`
funktionieren live. Web + CLI sind deployt.

---

## 3. Letzte Sessions — was gebaut wurde

### 3.00 Ticketing und Bereinigung (2026-11-22/23)

**Ticketing (ADR 0003, `docs/ticketing.md`):** Port `internal/ticketing` mit
Adaptern `native` (auf der `tasks`-Tabelle) und `paperclip` (HTTP-API),
führender Anbieter + optionaler Spiegel mit Live-Sync (Feld-Merge,
auditierte Konflikte, Tombstones beim Löschen), Ticket-API v1 + OpenAPI,
menschliche Akteure über das Gateway, Rollen/Rechte aus Keycloak-Gruppen,
Dispatcher `internal/dispatch` (inkl. Lead-Modus mit Unteraufgaben),
`dockweave ticket run`, Angular-Ansicht „Tickets“, Workspace pro Ticket.
TK-001 … TK-014 und TK-016 sind `done`.

**Offen: TK-015 — Abnahme auf der echten Installation.** Stand 2026-11-23:
Der installierte `dockweaved` ist vom 2026-11-22 22:14, also vor dem Commit
`db277ef` (TK-016, 22:20) gebaut — vor der Abnahme neu bauen und
installieren. Ticketing ist im Daemon **nicht konfiguriert** (`/api/v1/tickets`
→ 503 `ticketing_unavailable`); `TICKET_PROVIDER=native|paperclip` in
`dockweaved.conf` setzen. Danach Wrapper auf `DOCKWEAVE_DISPATCH=daemon`,
ein echtes Paperclip-Ticket bis DONE (Gate + Routing), Checkout/Release/Next
gegen echtes Paperclip prüfen, ein natives Ticket ohne Paperclip. Erst dann
`DOCKWEAVE_DISPATCH=daemon` zum Standard machen. Braucht sudo und
Entscheidungen des Nutzers.

**Bereinigung 2026-11-23:**

- Der Web-Gateway-Token stand im öffentlichen Repo (diese Datei und
  `ui/web/e2e/graph-layout.spec.ts`). Er wurde **rotiert** (alter Token →
  401) und aus beiden Dateien entfernt; der Live-E2E scheitert jetzt ohne
  `DOCKWEAVE_WEB_TOKEN` mit klarer Meldung. In der Git-Historie steht der
  alte Wert weiterhin, er ist aber ungültig.
- `internal/supervisor`: schmale Interfaces `ContainerEngine` /
  `MountReconciler` (ohne Remove — Regel 7 als Compile-Zeit-Garantie) und
  erstmals Unit-Tests (Zustandsabbildung, Engine-Ausfall, Prepare-Fehler,
  Reconcile inkl. Orphans und Registry-Status).
- AGENTS.md: Rechtetrennung Daemon/`dockweave-mountd`, vollständige
  Paketstruktur, Regel 9 (keine Zugangsdaten im Repo).
- `make lint` (staticcheck v0.8.2) lokal installiert: keine Befunde.

**Keycloak gehärtet (SD-023):** Master-Admin hatte `admin/admin`, die
Demo-Logins galten live, das Client-Secret war der öffentliche Dev-Default,
und Port 8080 war auf `0.0.0.0` veröffentlicht (aus Agent-Containern über
die Docker-Bridge erreichbar → Admin-Konsole → DockWeave-Admin). Jetzt:
Port nur `127.0.0.1`, alle Zugangsdaten per `scripts/keycloak-up.sh --rotate`
rotiert und lokal in `~/.config/dockweave/{keycloak,keycloak-users,oidc}.env`
(0600), Passwort-Grant am Client aus, Realm-Datei und Compose ohne
Zugangsdaten. Das Gateway lädt das Secret per `EnvironmentFile=` aus
`oidc.env` (Drop-in `override.conf` angepasst) und schreibt konfigurierte
Tokens nicht mehr ins Journal; der Web-Token wurde danach nochmals rotiert.

**LAN-Login repariert (2026-11-23):** OIDC lief nicht (`/oauth/login` →
503), weil das Gateway den Issuer `https://<LAN_HOST>:8444` aus WSL nicht
erreichte. Gründe: Windows leitete 8443/8444 nicht weiter, das WLAN-Profil
war „Öffentlich“, WSL vertraute der Proxy-CA nicht, und Docker-Bridges mit
`192.168.176.0/20` (u. a. `keycloak_default` und eine Bridge außerhalb dieses
Docker-Daemons) überdecken das Heimnetz `192.168.178.0/24` — WSL routet
`192.168.178.x` in die Bridge. Behoben durch: Windows-Portproxy
`192.168.178.88:8443/8444 → 127.0.0.1`, WLAN-Profil „Privat“, Firewall-Regel
„DockWeave LAN (TLS)“ (Privat, lokales Subnetz); im Gateway
`SSL_CERT_FILE` (System-Wurzeln + Proxy-CA, kein sudo) und die neue Option
`DOCKWEAVE_OIDC_CONNECT_TO=<LAN_HOST>:8444=127.0.0.1:8444`. Beides schreibt
`scripts/lan-access.sh up`. Verifiziert: Browser-Login als `krusty` → Angular
→ API 200; von Windows über `192.168.178.88` 8443 → 200, 8444-Realm → 200,
8444 `/admin/` → 403.

Hinweise: Eine überflüssige zweite Firewall-Regel „DockWeave LAN (TLS) from
WSL“ wurde beim Eingrenzen angelegt und kann gelöscht werden. Die
Docker-Subnetz-Überschneidung bleibt für andere WSL→LAN-Verbindungen
bestehen; dauerhafte Abhilfe ist `default-address-pools` in
`/etc/docker/daemon.json` (sudo, Docker-Neustart). Der Rechner sollte in der
Fritzbox eine feste IPv4 (`192.168.178.88`) bekommen, sonst passt der
Portproxy nicht mehr.

### 3.0 Gate-Pipeline (2026-11-21)

Ziel: Agent B arbeitet nur, wenn das deterministische Gate hinter Agent A auf
dem aktuellen Workspace-Stand bestanden ist. Entscheidungen D-1 (Durchsetzung
am Endpoint + Wrapper-Vorabprüfung) und D-2 (`on_true`/`on_false` als
Aktionen) in `docs/adr/0002-pipeline-enforcement.md`, SD-020.

- **Ausdrücke:** Literale `true`/`false`; `gateexec.Validate` +
  `POST /graph/validate-expression`; neue Gates ohne Default-Ausdruck.
- **Routing:** `allow | deny | wake:<agent> | rework`, Texte in
  `reason_true`/`reason_false`; alter Freitext bleibt als Begründung gültig
  (`internal/gatesync/action.go`).
- **Frische:** `gateexec.Fingerprint` (Pfade + Inhalte, ignorierte Bäume wie
  der Diff, Limits 100k Dateien / 2 GiB / 60 s). `gate_runs` hat
  `workspace_fingerprint`, `upstream_agent_id`, `phases` (Migration 17).
- **Kanten:** `gatesync.DerivePipeline` → Gateway pusht bei jedem Speichern
  `PUT /api/v1/pipelines` (admin, Revisionsschutz, `reset` bei neuer
  Graph-Datei). Tabellen `pipeline_edges`, `pipeline_overrides` (Migration 18).
- **Admission:** `internal/pipeline` (fail-closed, Gründe `no_run`, `failed`,
  `error`, `stale`, `expired`, `tamper`, `fingerprint_unavailable`,
  `unknown_gate`, `denied`, `internal`, `override`).
  `GET /api/v1/pipelines/admission`, `GET /api/v1/agents/{id}/pipeline`.
- **Durchsetzung:** `/v1/chat/completions` und herdr-Prompt → `409
  pipeline_blocked` vor dem ersten Byte an den Agenten. Audit
  `pipeline.update|blocked|override|override_used`.
- **Override:** `dockweave pipeline override <agent> --gate <id> --reason …`
  (einmalig, 15 min, nie für tamper).
- **CLI/SDK:** `dockweave pipeline list|status|override|overrides`,
  `gate run --upstream-agent --phase`; `pkg/sdk/pipelines.go`.
- **Wrapper:** `deployments/paperclip/pipeline.py` (precheck, handle-409,
  route), `pre_run`-Gates vor dem Lauf, `after_run,post_run` nach DONE,
  `blocked-check.py` fail-closed, keine hart codierten Pfade mehr
  (Konfiguration in `~/.config/dockweave/paperclip.env`, alte Werte dort
  eingetragen). Tests: `make test-wrapper`.
- **UI:** proceed-Kante mit Status/Tooltip, Badge an Downstream-Agenten,
  Routing-Editor, Inline-Validierung, `pipeline_sync.error`-Banner;
  Playwright `npm run e2e:pipeline` (gemocktes Backend); `internal/webui/dist`
  neu gebaut.
- **Tests:** `go test ./...`, `make test-wrapper`,
  `go test -tags integration -run 'TestGateExpressionPathInSandbox|TestPipelineEndToEnd' ./tests/integration/`
  (echtes Docker, ohne Mount-Operationen).

**Noch zu tun durch den Nutzer:** neuen `dockweaved` deployen (Migrationen
17/18 laufen beim Start), `dockweave` CLI + `dockweave-web` neu installieren,
für wake/rework `DOCKWEAVE_PAPERCLIP_AGENT_MAP` anlegen.

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
Status-Semantik, Fail-closed-Regeln). TODO: `todo/archive/gate-enforcement.json`
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

### 3.4 Security-Hardening (Analyse-Runde, 2026-11-19)

Eine Quellcode-Analyse deckte Stellen auf, an denen Code und Doku/Sicherheits-
Kontrakt auseinanderliefen. Gepflegt in `todo/archive/hardening-2026-11.json`
(**6 done, 1 deferred**), Commits `7ebd92f` … `6e6c732`:

- **mountd-TOCTOU (revertiert)** (`internal/mounthelper`): Ein Versuch, die
  Bind-*Quelle* per `/proc/self/fd/<n>` zu pinnen, wurde revertiert — der
  WSL2-Deployment-Kernel lehnt eine `/proc/self/fd`-Bind-Quelle mit `EINVAL`
  ab. Der Helper löst die Quelle daher weiter per `readlink` auf (kleines
  TOCTOU-Fenster auf dem Quell-Pfad, dokumentiert in SD-019).
- **Explizites `MS_PRIVATE`** auf dem Staging-Mount vor dem RO-Remount
  (`internal/mounts`) — zuvor beruhte die „Privatsphäre" nur auf der Lage.
- **`MS_NOSYMFOLLOW` fail-closed**: Nichtverfügbarkeit liefert jetzt
  `ErrSymlinkProtectionUnavailable` statt stiller Degradation.
- **Web-Gateway Rollen-Durchsetzung** (`internal/webui`): `monitor` ist jetzt
  wirklich nur lesend (GET/HEAD/OPTIONS); `/api/v1/fs/dirs` + `/ws/term` sind
  admin-only. Token/Bearer/Cookie bleiben admin. (War in Abschnitt 8 als
  „Offener Punkt Nr. 2" gelistet — jetzt erledigt.)
- **Doku angeglichen**: `docs/mount-helper.md`(+`.de.md`) beschreibt den echten
  fd-basierten Ablauf (kein `open_tree`/`move_mount`); `docs/security-decisions.md`
  erhält **SD-019** und korrigiert SD-016/SD-018; `AGENTS.md` Regeln 2/4/7
  präzisiert.
- **Integrationstest-Regel gelockert** (`tests/integration`): `requireRoot`
  prüft zusätzlich `CAP_SYS_ADMIN` (`CapEff`-Bit 21), skippt also sauber statt
  zu scheitern. Läuft jetzt auch auf dezidierten Dev-Rechnern/VMs (nie auf
  geteiltem/Produktiv-Host).

Wichtige Commits dieser Session:
`94b67a6`, `e0806cf`, `004b0fd`, `7b8a42d`, `3b8ce2c`, `7ebd92f`…`6e6c732`.

---

## 4. Bauen & Deployen

```bash
cd ~/dockweave
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
dockweave gate run ~/projects/crystal-run --issue DOC-63 --json
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
DOCKWEAVE_GATE_INTEGRATION=1 DOCKWEAVE_GATE_WORKSPACE=~/projects/crystal-run \
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
| TODO | `todo/archive/gate-enforcement.json`, `todo/archive/gate-pipeline.json` |
| Pipeline | `internal/pipeline/admission.go`, `internal/api/pipelines.go`, `internal/persistence/pipelines.go`, `internal/model/pipeline.go` |
| Pipeline-Ableitung | `internal/gatesync/{pipeline,action}.go` |
| Fingerprint | `internal/gateexec/fingerprint.go` |
| Pipeline-CLI/SDK | `cmd/dockweave/pipeline.go`, `pkg/sdk/pipelines.go` |
| Pipeline-Wrapper | `deployments/paperclip/pipeline.py`, `deployments/paperclip/tests/test_wrapper.py` |
| Pipeline-UI | `ui/web/src/app/core/services/pipeline.service.ts`, `ui/web/e2e/pipeline.spec.ts` |
| ADR | `docs/adr/0002-pipeline-enforcement.md` / `.de.md` |

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
- **Broker-Permission:** `~/.local/share` braucht `o+x`, sonst
  erreicht der Daemon den Broker-Socket nicht.
- **Drift:** Wird `.paperclip/gates.json` manuell verändert, zeigt die UI
  „Drift"; der nächste Sync überschreibt die Datei (bewusst).
- **Review-Attribution:** Unattributierte `VERDICT: PASS`-Kommentare zählen
  nicht (Override: `DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1`).

---

## 8. Offene Punkte / nächste Schritte

0. **Pipeline deployen:** Daemon-Build mit Migrationen 17/18 installieren
   (`sudo install … dockweaved`), Gateway muss als admin-Peer laufen, sonst
   meldet das Speichern `pipeline_sync.error`. Agent-Zuordnung
   `DOCKWEAVE_PAPERCLIP_AGENT_MAP` für wake/rework anlegen. Legacy-Ziele
   `dockweave-<cid>` umgehen die Pipeline (deprecated).

1. **`todo/archive/gate-enforcement.json`:** 20 Tasks `done`; offen nur die bewussten
   Non-Goals `gate-040` (keine zweite Dependency-DB) und `gate-041` (kein
   eigenes Issue-Board), beide `deferred`.
2. **Rollen-Durchsetzung am Web-Gateway:** in dieser Session umgesetzt
   (Abschnitt 3.4). Der Daemon erzwingt seine Peer-Rollen unverändert.
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
8. **Broker-Proxy-Fläche (hard-007, deferred):** Der Broker leitet jeden
   Upstream-Pfad unter `/p/{provider}/…` weiter; die Modell-Allowlist greift
   nur, wenn der Body ein `model`-Feld trägt. Als bekannte Grenze in SD-019
   dokumentiert; Endpoint-/Modell-Eingrenzung ist Folgearbeit.
9. **SSH-Broker Phasen 3–5:** Phase 2 (CA + Policy + Zertifikat + Wrapper) ist
   umgesetzt (`internal/sshbroker`, `cmd/dockweave-ssh`). Offen bleiben
   Git-Integration (`GIT_SSH_COMMAND`), FIDO2-Signer und privilegierte
   Egress-Tests (siehe `docs/ssh-broker-design.md`, Abschnitt „Phases").
10. **Mount-Helfer TOCTOU-Grenze:** Die Bind-Quelle wird im Helper per
    `readlink` aufgelöst (WSL2 lehnt `/proc/self/fd` als Bind-Quelle ab) —
    kleines TOCTOU-Fenster, dokumentiert in SD-019.

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
paperclipai issue list -C "$PAPERCLIP_COMPANY_ID" --json   # ID lokal setzen, nicht einchecken

# Logs
journalctl -u dockweaved -n 50 --no-pager
journalctl -u dockweave-mountd -n 30 --no-pager
journalctl --user -u dockweave-web -n 30 --no-pager
```
