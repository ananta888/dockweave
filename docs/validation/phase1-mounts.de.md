# Phase-1-Validierung: Linux-Mount-Semantik für DockWeave

**Datum:** 2026-10-23 · **Kernel:** 6.18.33 (WSL2, Ubuntu 24.04) · **Umgebung:** privilegierter Container (CAP_SYS_ADMIN, initial userns)

Getestet wurde die Kernannahme der DockWeave-Architektur: dynamische Mounts
in einen laufenden Agenten-Namespace über einen `rshared`-Hostbaum mit
`rslave`-Containerbind.

## Ergebnisse

| # | Test | Ergebnis |
|---|---|---|
| 1 | Host-Baum `mount --bind` auf sich selbst + `mount --make-rshared` | ✅ `shared` |
| 2 | Agent-Namespace `mount --make-rslave` auf dem Baum | ✅ `private,slave` (= rslave) |
| 3 | Dynamischer Mount **nach** Agent-Start wird im Agenten sichtbar | ✅ |
| 4 | Rückpropagation Agent→Host | ✅ unterbunden |
| 5 | `mount --bind -o ro` → Schreiben im Slave-Namespace | ⚠️ **RW!** (s. Befund A) |
| 6 | RO-Staging (privat binden → remount ro → `mount --move`) | ✅ RO im Slave |
| 7 | `umount` mit offenem fd | ✅ `EBUSY` (sicherer Fehlerzustand) |
| 8 | fd bleibt nach EBUSY gültig; umount nach Schließung erfolgreich | ✅ |
| 9 | Nested-Submount unter RO-Remount | nicht reproduzierbar (Kernel 6.18 erzwingt ro) |
| 10 | Bestehende Mounts via `findmnt`/`/proc/self/mounts` erkennbar | ✅ |

## Befund A — RO-Propagation ist nicht atomar (kritisch)

`mount --bind -o ro src dst` führt intern zwei Schritte aus: Bind (RW) und
Remount (RO). Der Bind propagiert sofort in alle Slave-Namespaces — als RW.
Remount-Flags propagieren **nicht**. Ein Agent, der zwischen den Schritten
oder durch den verbliebenen RW-Klon schreibt, umgeht die RO-Absicht.

**Konsequenz (verbindlich, siehe AGENTS.md Regel 2):** RO-Binds werden
außerhalb des Shared-Baums privat gebunden, per `remount,bind,ro`
abgesichert und erst dann per `mount --move` in den Shared-Baum
verschoben. Der Move propagiert den bereits read-only markierten Mount.

## Befund B — Unmount-Semantik

Ein reguläres `umount(2)` scheitert mit `EBUSY`, solange Dateien des Mounts
geöffnet sind. Bereits offene Deskriptoren bleiben nach einem (erzwungenen)
Unmount gültig und lesbar — Unmount entzieht keine bestehenden Zugriffe.
DockWeave meldet `EBUSY` als strukturierten Fehler und nutzt `MNT_DETACH`
nur auf explizite Anforderung (auditiert). Für unvertrauenswürdige Agenten
unterstützt der Grant-Entzug optional den Stopp des Agentenprozesses.

## Plattformhinweis WSL2

Die Tests laufen unter WSL2. Propagation funktioniert, aber produktive
Zielplattform ist natives Linux. Windows/macOS/Docker-Desktop werden nicht
als unterstützt behandelt; die Capability-Erkennung des Daemons prüft
Linux, CAP_SYS_ADMIN und Propagationsfähigkeit beim Start und degradiert
oder verweigert sonst mit klarer Diagnose.

## Reproduktion

`tests/integration/testdata/mount-semantics.sh` (im Repo) bzw.:

```bash
docker run --rm --privileged -v "$PWD/tests/integration/testdata:/work" \
  ubuntu:24.04 bash /work/mount-semantics.sh
```
