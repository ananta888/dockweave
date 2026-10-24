# AGENTS.md — DockWeave Projektanweisungen

Dieses Dokument richtet sich an KI-Coding-Agenten und menschliche Entwickler,
die an DockWeave mitarbeiten. Es beschreibt Struktur, Konventionen, Build- und
Testverfahren sowie verbindliche Sicherheitsregeln.

## Projektüberblick

DockWeave führt KI-Coding-Agenten in isolierten Docker-Containern aus.
Jeder Agent erhält drei getrennte Bereiche:

| Container-Pfad | Zweck | Host-Pfad (Default) |
|---|---|---|
| `/agent` | Agenteninstallation (bevorzugt read-only) | `/var/lib/dockweave/agents/<id>/agent` |
| `/memory` | Persistentes Langzeitgedächtnis (rw) | `/var/lib/dockweave/memories/<mem-id>` |
| `/workspace` | Dynamische Projektfreigaben (`rslave`) | `/var/lib/dockweave/agents/<id>/workspace` |

Verwaltung durch den Daemon `dockweaved` (root, Unix-Socket
`/run/dockweave/dockweaved.sock`), CLI `dockweave` und TUI.

## Verbindliche Sicherheitsregeln

1. **Mount-Rechte nur im Daemon.** CLI/TUI/API führen niemals selbst
   `mount(2)` aus; sie sprechen ausschließlich mit `dockweaved`.
2. **RO-Staging-Regel (Phase-1-validiert):** Read-only-Binds werden niemals
   direkt mit `-o ro` in den Shared-Baum gemountet. Korrekte Sequenz:
   privat binden → `remount,bind,ro` → `mount --move` in den Shared-Baum.
   Grund: Remount-Flags propagieren nicht in Slave-Namespaces.
3. **Kein `umount -l` / `MNT_DETACH` als Standard.** Bei `EBUSY` wird ein
   strukturierter Fehler zurückgegeben; detachen ist nur mit explizitem
   Force-Flag erlaubt und wird auditiert.
4. **Pfadprüfung:** Alle Host-Pfade werden kanonisiert
   (`filepath.EvalSymlinks`), gegen die Allowlist registrierter
   Wurzelverzeichnisse geprüft und bei sicherheitskritischen Zugriffen mit
   `openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS)` geöffnet (TOCTOU-Schutz).
5. **Agenten erhalten niemals** `/var/run/docker.sock`, keine privilegierten
   Container, keine `CAP_SYS_ADMIN`.
6. **Keine SQLite-Datei wird von mehreren Agenten gleichzeitig beschrieben.**
   Memory-Verzeichnisse haben eigene Identitäten und genau einen
   Schreibinhaber; Attach/Detach an Ersatzagenten nur im gestoppten Zustand.
7. Der Daemon entfernt bei Recovery niemals Mounts, die nicht eindeutig von
   DockWeave stammen (Label-/Registry-Prüfung).

## Build & Test

Voraussetzungen: Go ≥ 1.24, Docker Engine, Linux (Kernel ≥ 5.12 empfohlen).

```bash
go build ./...                      # Build
go test ./...                       # Unit-Tests (unprivilegiert)
go test -race ./...                 # Race Detector
go vet ./... && staticcheck ./...   # Statische Analyse
```

Privilegierte Integrationstests (echte Mounts) laufen ausschließlich
in der dafür vorgesehenen Container-Umgebung:

```bash
make test-integration    # baut + startet privilegierten Testcontainer
```

Niemals privilegierte Mount-Tests direkt auf einem Entwickler-Host
ausführen. Tests, die `CAP_SYS_ADMIN` benötigen, prüfen
`os.Geteuid()==0` + Capability und skippen sonst mit `t.Skip`.

## Struktur

- `cmd/dockweave` — CLI (cobra), `cmd/dockweaved` — Daemon
- `internal/model` — zentrale Datenmodelle (Agent, Workspace, Grant, Mount, Event)
- `internal/persistence` — SQLite (modernc.org/sqlite, pure Go, WAL)
- `internal/mounts` — Mount-Manager (sicherheitskritisch; Änderungen nur
  mit erweiterten Tests + Review)
- `internal/dockerclient` — Docker Engine API Wrapper (Labels s. unten)
- `internal/agents`, `internal/workspaces` — Fachlogik
- `internal/monitor` — Provider: `docker`, `procfs` (tetragon: geplant)
- `internal/api` — versionierte REST-API (v1) über Unix-Socket/TCP
- `internal/supervisor` — Recovery/Soll-Ist-Abgleich
- `pkg/sdk` — öffentlicher Go-Client
- `ui/tui` — Bubble Tea TUI
- `todo/*.json` — maschinenlesbare Aufgabenstände
- `tests/integration` — privilegierte Tests

## Docker-Label-Konvention

| Label | Bedeutung |
|---|---|
| `dockweave.managed=true` | Container gehört zu DockWeave |
| `dockweave.agent.id=<id>` | Stabile Agenten-ID |
| `dockweave.agent.name=<name>` | Anzeigename |

## Konventionen

- Go: `gofmt`, `go vet` sauber; Fehler immer mit Kontext (`fmt.Errorf("...: %w")`).
- Keine Mock-Daten in produktiven Pfaden; Provider liefern echte oder
  explizit als „unverfügbar" markierte Daten.
- DB-Zugriff nur über `internal/persistence`; Migrationen versioniert.
- Jede Änderung an `internal/mounts` erfordert einen Eintrag in
  `docs/security-decisions.md`, falls sie das Bedrohungsmodell berührt.
- Commits: klein, thematisch, Conventional-Commits-Stil
  (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`).
- Dokumentation zweisprachig (Deutsch + Englisch); englische Dateien ohne
  Suffix, deutsche mit `.de.md`.

## Status-Felder der TODO-JSONs

`pending` → `in_progress` → `done` | `blocked` | `deferred`.
Geänderte Stände werden im selben Commit wie der zugehörige Code gepflegt.
