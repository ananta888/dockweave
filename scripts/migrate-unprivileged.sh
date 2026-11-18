#!/usr/bin/env bash
# Migrate DockWeave from the root daemon to the unprivileged daemon +
# privileged mount helper. Idempotent: safe to re-run.
#
# Run as root:
#   sudo ./scripts/migrate-unprivileged.sh [HOME_DIR ...]
#
# HOME_DIR ...: optional host directories the daemon must be able to traverse
# (e.g. /home/krusty when workspaces live under /home/krusty/projects). Only
# execute (+x) is added, never read or write.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO/bin"
HOMES=("$@")

if [ "$(id -u)" != 0 ]; then
  echo "run as root: sudo $0" >&2
  exit 1
fi

echo "== 1/6 Benutzer =="
# The dockweave group (gid 1001) usually already exists from the CLI socket
# group; reuse it and do not create a duplicate group.
if ! id dockweave >/dev/null 2>&1; then
  if getent group dockweave >/dev/null; then
    GID=$(getent group dockweave | cut -d: -f3)
    useradd --system -M --no-user-group --gid "$GID" --shell /usr/sbin/nologin dockweave
  else
    useradd --system -M --shell /usr/sbin/nologin dockweave
  fi
fi
DWUID=$(id -u dockweave)
echo "dockweave uid=$DWUID"
usermod -aG docker dockweave || true

echo "== 2/6 Binaries + Units =="
install -m 0755 "$BIN/dockweaved"        /usr/local/bin/
install -m 0755 "$BIN/dockweave-mountd"  /usr/local/bin/
install -m 0644 "$REPO/deployments/systemd/dockweaved.service"      /etc/systemd/system/
install -m 0644 "$REPO/deployments/systemd/dockweave-mountd.service" /etc/systemd/system/
sed -i "s/--uid [0-9]*/--uid $DWUID/" /etc/systemd/system/dockweave-mountd.service
rm -f /etc/systemd/system/dockweaved.service.d/override.conf

echo "== 3/6 Eigentum (State-Root) =="
chown dockweave:dockweave /var/lib/dockweave
chown dockweave:dockweave /var/lib/dockweave/dockweave.db* 2>/dev/null || true
mkdir -p /var/lib/dockweave/.staging
chown dockweave:dockweave /var/lib/dockweave/.staging
chmod 750 /var/lib/dockweave/.staging
chown dockweave:dockweave /var/lib/dockweave/agents

echo "== 4/6 Workspace-Trees (nicht rekursiv in Mounts) =="
for tree in /var/lib/dockweave/agents/*/workspace; do
  [ -d "$tree" ] || continue
  chgrp dockweave "$tree" || true
  chmod g+w "$tree" || true
done

echo "== 5/6 Broker-Verzeichnis =="
BK="/home/$(id -un "${SUDO_USER:-root}")/.local/share/dockweave/broker"
if [ -d "$BK" ]; then
  chgrp dockweave "$BK" || true
  chmod 0750 "$BK" || true
  # The broker lives under the user's private ~/.local/share (0700); the
  # daemon needs traverse (+x) on that ancestor, not read.
  SHARE="$(dirname "$(dirname "$BK")")"
  chmod o+x "$SHARE" 2>/dev/null || true
fi

echo "== 6/6 Homes durchlassbar machen + Neustart =="
for h in "${HOMES[@]}"; do
  if [ -d "$h" ]; then
    chmod o+x "$h"
    echo "  o+x $h"
  fi
done

systemctl daemon-reload
systemctl enable --now dockweave-mountd
systemctl restart dockweaved

echo "done. verify with: dockweave agent list"
