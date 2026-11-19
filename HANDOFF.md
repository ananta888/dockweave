# DockWeave — Session Handoff

Stand: 2026-11-19, letzter Commit `49bf61d` (Push nach `origin/main`).
Diese Datei fasst den wichtigsten Stand zusammen, damit ein anderer Agent
nahtlos weiterarbeiten kann.

---

## 1. Was das ist

`/home/krusty/dockweave` — Go-Backend + Angular-Web-UI + TUI. DockWeave führt
KI-Agenten in isolierten Docker-Containern aus und verwaltet Mounts,
Credentials und Agenten-Lebenszyklus. Zusätzlich gibt es inzwischen:
Produktivitätsschicht, OIDC-Login, einen privilegierten Mount-Helper und eine
Paperclip-Anbindung für Goal-Delegation.

**Sprachen/Toolchain:** Go ≥ 1.27, Node 24, Angular, Docker, systemd, SQLite
(pure Go), Keycloak 26.1.

---

## 2. Laufende Dienste & Pfade

| Dienst | Art | Zweck |
|---|---|---|
| `dockweaved` (system) | root→nein: läuft als User `dockweave` (uid 997) | Daemon, API, DB, Docker |
| `dockweave-mountd` (system) | root, **nur** `CAP_SYS_ADMIN` | führt Mount-Syscalls auf FDs aus |
| `dockweave-web` (user) | krusty | Angular-UI + API-Proxy auf `127.0.0.1:8790` |
| `dockweave-broker` (user) | krusty | Credential-Vault/Broker, Socket `~/.local/share/dockweave/broker/{admin,broker}.sock`, TCP `172.17.0.1:8787` |
| `dockweave-keycloak` (docker) | Container | IdP, `http://127.0.0.1:8080`, Realm `dockweave` |
| Paperclip (user) | krusty | `http://127.0.0.1:3100`, Company `DockWeave Engineering` |

Sockets: `/run/dockweave/dockweaved.sock`, `dockweaved.term.sock`,
`mountd.sock` (Gruppe `dockweave`, 0660).

**Web-Zugang:** stabiler Token `dw-7607e59342eadecabb561e7d13594797e69a6b35762d1924`
in `~/.config/dockweave/web.env` (nicht im Drop-in duplizieren!). Alternativ
OIDC-Login.

---

## 3. Security-Split (wichtig, SD-018)

- Daemon läuft **ohne** Capabilities; Mounts gehen über `dockweave-mountd`
  (root, nur `CAP_SYS_ADMIN`) per FD-Protokoll (`SCM_RIGHTS`) über
  `/run/dockweave/mountd.sock`.
- Ziele müssen unter `/var/lib/dockweave` liegen; Quellen sind Workspaces
  (vom Daemon validiert).
- **Achtung:** `open_tree(OPEN_TREE_CLONE)` funktioniert nur, wenn die Quelle
  bereits ein Mountpoint ist. Deshalb nutzt `bind` wieder `mount(2)` auf dem
  aufgelösten Pfad; `move`/`remount`/`setattr` sind FD-/pfadbasiert.
- Implementierung: `internal/mounthelper/`, `internal/mounts/backend.go`,
  `cmd/dockweave-mountd/`.
- Units: `deployments/systemd/dockweave-mountd.service`,
  `deployments/systemd/dockweaved.service`.
- Doku: `docs/mount-helper.md` / `.de.md`, `docs/security-decisions.md` (SD-018).
- Migration: `scripts/migrate-unprivileged.sh` (sudo) +
  `scripts/migrate-broker-user.sh` (user).

### Fallstricke, die schon aufgetreten sind
- `/home/krusty/.local/share` ist `0700` → Daemon kann Broker-Socket nicht
  erreichen. Fix: `sudo chmod o+x /home/krusty/.local/share`.
- `/home/krusty` brauchte `o+x` (Workspace-Kanonisierung).
- `/var/lib/dockweave/.staging` und `agents/` müssen `dockweave` gehören
  (`.staging` 0750).
- Mounts können `EBUSY` liefern, wenn aktive Agenten den Pfad nutzen →
  `dockweave grant revoke <id> --force` (auditiert).

---

## 4. OIDC / Keycloak

- Implementierung: `internal/oidc/` (DDD: `domain.go`, `provider.go`,
  `session.go`, `service.go`).
- Flow: Authorization Code + **PKCE + nonce** (nonce wird beim Callback
  gegen `id_token` geprüft), AES-GCM-verschlüsseltes Session-Cookie
  (`dockweave_oidc`), Rollen aus `groups` gemappt.
- Routen: `GET /oauth/login`, `GET /oauth/callback`, `POST /oauth/logout`
  (vom Token-Auth ausgenommen).
- Audit: Login/Logout → `slog` + best-effort `POST /api/v1/audit`
  (Event-Typ `auth.oidc`, nur `iss`+`sub`).
- OIDC-Discovery hat **10s-Timeout** und fällt bei Fehler auf Token-Login
  zurück (verhindert Gateway-Hänger).
- Web-Flags: `--oidc-issuer/-client-id/-client-secret/-redirect-url/-scopes/
  -roles-claim/-admin-groups/-monitor-groups/-session-key` (auch als
  `DOCKWEAVE_OIDC_*`).
- Keycloak-Standard: `deployments/keycloak/docker-compose.yml` +
  `realm-dockweave.json`, Start mit `./scripts/keycloak-up.sh`.
- **User:** `krusty`/`krusty` (Hauptadmin, Gruppe `dockweave-admin`),
  `user`/`user`, `monitor`/`monitor`; Master-Admin `admin`/`admin`.
  Client-Secret `dockweave-dev-secret` (Dev!).
- Aktivierung im Web: Drop-in
  `~/.config/systemd/user/dockweave-web.service.d/override.conf` mit den
  `DOCKWEAVE_OIDC_*`-Variablen.
- Doku: `docs/oidc.md` / `.de.md`.

---

## 5. Produktivitätsschicht

- Migration **14** (`internal/persistence/persistence.go`): `objectives`,
  `tasks` (+`attempts`), `focus_sessions`, `work_events`, `evidence`,
  `input_budgets`, `reviews`, `objective_revisions`.
- Modelle: `internal/model/productivity.go`.
- Store: `internal/persistence/productivity.go` (Fokus-Single-Session per
  unique index, idempotente Events/Evidence, `done→verified` nur mit
  verifiziertem Nachweis).
- API: `internal/api/productivity.go` – u.a. `/objectives`, `/tasks`,
  `/tasks/today`, `/focus/*`, `/work-events`, `/evidence`, `/budgets`,
  `/reviews`, `/reviews/report`, `/calendar`, `/tasks/suggest`, Export,
  `/tasks/rollover`, `/focus/switch`, `/productivity/prune`, `/api/v1/audit`.
- Collector: `internal/productivity/collector.go` (Agentenlaufzeit per
  Presence, Broker-Usage-Deltas → `llm`-Events).
- CLI: `cmd/dockweave/productivity.go`
  (`objective/task/focus/plan/event/evidence/budget/review/log/export/prune`).
- Angular: `ui/web/src/app/components/productivity.component.ts` (Tab
  „Productivity") + `core/services/productivity.service.ts`.
- TUI: Focus-Tab in `ui/tui/tui.go`.
- Todo: `todo/productivity.json` (27/27 done).
- Doku: `docs/productivity.md` / `.de.md`.

---

## 6. Paperclip / Agenten-Team

- Paperclip-Company: `43b81f94-5079-4e24-8500-2e9cd78732a6` („DockWeave
  Engineering"). Rollen: Lead, Backend, Frontend, Reviewer, Platform.
- DockWeave-Agenten: `lead`, `demo-opencode`, `researcher`, `reviewer`,
  `platform` — alle **OpenCode** mit Modell
  `opencode/deepseek-v4.1-flash-free` (in `/memory/.config/opencode/opencode.json`).
  `developer`, `claude-test`, `herdr-codex` sind gestoppt.
- Delegation/Gates (Docs: `docs/paperclip-delegation.md` / `.de.md`):
  - `deployments/paperclip/dockweave-run.sh` (Worker- + Orchestrate-Modus)
  - `plan-extract.py`, `plan-delegate.py` (blockedByIssueIds, Rollen aus
    `~/.config/dockweave/paperclip-roles.json`), `wake-ready.py`,
    `blocked-check.py`, `gates.py` (Build-Gate aus
    `<workspace>/.paperclip/gates.json`), `paperclip-gate.py` (Interactions,
    Rework), `parent-close.py` (Abschluss nur mit Review-PASS + Human-Freigabe).
  - `STATUS: DONE|BLOCKED|IN_PROGRESS`-Vertrag; Reviewer `VERDICT: PASS|FAIL`.
- Bekannte Einschränkung: Codex/DeepSeek‑Quotas; damals auf freies
  OpenCode-Modell gewechselt.

---

## 7. 2D-Graph (Angular)

- `ui/web/src/app/components/graph.component.ts` (große Komponente).
- Layout-Modi: `mixed`, `hierarchy`, `columns`, `grid`, `radial`, `force`,
  `stress` (SMACOF). Portanzahl in `ui/web/src/app/core/graph-schema.ts`.
- Vorhanden: Layout-Caching (kein Re-Run bei 5s-Datenrefresh), Spinner mittig
  mit „Abbrechen", Animation (easeOut) in neue Position, Barycenter-Ordnung
  gegen Kantenkreuzungen, Port-Verteilung.
- **Wichtig:** `portsForId` darf **nicht** `this.nodes()` lesen (verursacht
  zirkuläre Computed-Dependency → Spinner hängt). Aktuell gelöst über
  `kindOfId(id)` aus dem ID-Präfix.
- Fallback: bei nicht-finiten/riesigen Koordinaten fällt das Layout auf Grid
  zurück; Layout-Fehler werden geloggt und crashen die UI nicht.
- E2E: `ui/web/e2e/graph-layout.spec.ts` + `ui/web/playwright.config.ts`,
  Script `npm run e2e`. Prüft alle Layout-Modi **und** den Wechsel
  Organisation↔Ressourcen (Spinner darf nicht hängen, keine Console-Errors,
  `ResizeObserver`-Warnung wird ignoriert). Playwright-Chromium liegt unter
  `~/.cache/ms-playwright` (Install: `npx playwright install chromium`).

---

## 8. Bauen & Deployen

```bash
cd /home/krusty/dockweave
go build ./... && go test ./... && go vet ./...

# Web (Angular in Go-Binary einbetten)
cd ui/web && npm run build && cd ../..
go build -o bin/dockweave ./cmd/dockweave
cp bin/dockweave ~/.local/bin/dockweave
systemctl --user restart dockweave-web

# Daemon/Helper (braucht root)
go build -o bin/dockweaved ./cmd/dockweaved
go build -o bin/dockweave-mountd ./cmd/dockweave-mountd
sudo install -m 0755 bin/dockweaved /usr/local/bin/
sudo install -m 0755 bin/dockweave-mountd /usr/local/bin/
sudo systemctl restart dockweave-mountd dockweaved
```

E2E: `cd ui/web && npm run e2e` (App muss unter `127.0.0.1:8790` laufen).

---

## 9. Wichtige Gotchas

- **CRLF-Rauschen:** `core.autocrlf=true`; viele `M`-Dateien sind nur
  Zeilenenden. echte Änderungen prüfen mit
  `git diff --ignore-cr-at-eol --stat`.
- **Bash-History:** `!` in doppelt gequoteten Strings → „event not found";
  `set +H` verwenden.
- **sudo:** kein passwortloses sudo; root-Schritte muss der Nutzer ausführen.
- **Skills-Warnung:** Daemon kann `/var/lib/dockweave/skills` nicht schreiben
  („skill catalog disabled") — optional `chown dockweave:dockweave`.
- **Broker-Permission:** siehe Abschnitt 3 (`chmod o+x .local/share`).

---

## 10. Offene Punkte / nächste Schritte

1. **User-Bericht (offen):** Beim Wechsel des *Darstellungsmodus*
   (Organisation/Ressourcen) „passiert nichts". Der E2E-Test deckt genau
   diesen Wechsel ab und ist grün, Deploy ist aktuell → Verdacht: veraltetes
   Browser-Bundle. Nächster Schritt: Hard-Reload/Cache leeren; falls weiter
   defekt, Console-Fehler + betroffenes Steuerelement vom Nutzer einholen.
2. **Edge-Bundling** (Holten & van Wijk 2009) als Option gegen sichtbare
   Kantenkreuzungen in Force/Stress (optional).
3. **Dedizierter `dockweave`-User** ist umgesetzt; `chown` läuft über den
   Helper. Verbleibende Alt-Eigentums-Feinheiten siehe Abschnitt 3.
4. **Rollen-Durchsetzung** am Web-Gateway (monitor = nur lesen) ist noch
   nicht erzwungen; Rollen werden nur abgeleitet/gespeichert.
5. **Paperclip-Pipeline** weiter beachten: Quotas (Codex/DeepSeek), Gates
   laufen über den Wrapper. Goal-Run 2/3 im Board prüfen
   (`paperclipai issue list -C 43b81f94-…`).
6. **Ananta-Import** und **LLM-Review-Agent** sind laut Spezifikation
   optional und bewusst nicht gebaut.

---

## 11. Schnellzugriff

```bash
# Dienste
systemctl status dockweaved dockweave-mountd --no-pager
systemctl --user status dockweave-web dockweave-broker --no-pager

# Health
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8790/
dockweave agent list
dockweave broker status

# Keycloak
docker ps --filter name=dockweave-keycloak
curl -s -o /dev/null -w "%{http_code}\n" \
  http://127.0.0.1:8080/realms/dockweave/.well-known/openid-configuration

# Logs
journalctl -u dockweaved -n 30 --no-pager
journalctl -u dockweave-mountd -n 30 --no-pager
```
