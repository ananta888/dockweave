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
ist `0660`, `--uid` beschraenkt den Client. Die RO-Sequenz liegt im Helfer,
Aufrufer koennen sie nicht abkuerzen.

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

Ohne `DOCKWEAVE_MOUNTD_SOCKET` faellt der Daemon auf den direkten Root-Backend
zurueck und startet als Nicht-root nicht.
