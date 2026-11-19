# Privilegientrennung: dockweaved und dockweave-mountd

DockWeave braucht `mount(2)`, um Workspaces an laufende Agenten zu haengen.
Statt den ganzen Daemon als root laufen zu lassen, gibt es einen getrennten
Helfer, der nur Dateideskriptoren akzeptiert.

## Komponenten

| Prozess | Benutzer | Rechte | Aufgabe |
|---|---|---|---|
| `dockweaved` | `dockweave` (+ `docker`-Gruppe) | keine besonderen Rechte | API, DB, Docker, Sessions |
| `dockweave-mountd` | root | `CAP_SYS_ADMIN`, `CAP_DAC_READ_SEARCH` | `mount`/`mount_setattr`/`umount` auf empfangenen FDs |

## Protokoll

Eine JSON-Zeile plus optionale FDs per `SCM_RIGHTS` ueber
`/run/dockweave/mountd.sock`. Ziele muessen unterhalb von `--root` liegen; der
Helfer prueft das per `readlink(/proc/self/fd/N)` vor dem Syscall. Der Socket
ist `0660`, `--uid` beschraenkt den Client.

Die Bind-*Quelle* wird ueber `/proc/self/fd/<n>` gemountet: Der Kernel loest
den offenen, vom Daemon bereits verifizierten Deskriptor auf statt den Pfad
erneut nachzuschlagen. Damit bleibt das verifizierte Objekt ueber die
Privileg-Grenze gepinnt (TOCTOU-sicher). Ziele werden per `readlink` aus dem
FD aufgeloest, mit State-Root-Pruefung (und bei `bind` zusaetzlich einem
Inode-Check gegen Vertauschen). Die RO-Sequenz (Bind → privat → Remount ro →
`mount_setattr` → Move) wird vom Daemon orchestriert; der Helfer fuehrt nur
die einzelnen Syscalls aus.

## Deployment

```bash
install -m 0755 bin/dockweave-mountd /usr/local/bin/
install -m 0644 deployments/systemd/dockweave-mountd.service /etc/systemd/system/
install -m 0644 deployments/systemd/dockweaved.service /etc/systemd/system/
useradd --system --home /var/lib/dockweave --shell /usr/sbin/nologin dockweave
usermod -aG docker dockweave
chown -R dockweave:dockweave /var/lib/dockweave
systemctl daemon-reload
systemctl enable --now dockweave-mountd dockweaved
```

Die mountd-Unit darf **kein** `ProtectSystem`, `PrivateTmp` oder aehnliches
setzen: Diese Optionen erzeugen eine private Mount-Namespace, in der erzeugte
Mounts weder den Host noch die Container erreichen.

Ohne `DOCKWEAVE_MOUNTD_SOCKET` faellt der Daemon auf den direkten Root-Backend
zurueck und startet als Nicht-root nicht.

## Migration von einem Root-Daemon

Zwei idempotente Skripte automatisieren die Schritte fuer eine bestehende
Instanz:

```bash
sudo ./scripts/migrate-unprivileged.sh /home/krusty   # System-Seite (Binaries,
                                                      # Units, User, Eigentum)
./scripts/migrate-broker-user.sh                      # User-Seite (Broker-Socket)
```

`migrate-unprivileged.sh` bekommt die Host-Verzeichnisse, die der Daemon
durchqueren koennen muss (z. B. das Home mit den registrierten Workspaces) und
setzt dort `o+x`. Es rekursiert nie in Mounts und vergibt kein Lesen/Schreiben.
