#!/usr/bin/env bash
# DockWeave uninstaller: stops the daemon and removes binaries, service and
# configuration. Your data (/var/lib/dockweave: agents, memories, snapshots,
# registry) is kept unless --purge is given.
#
# Usage:  ./scripts/uninstall.sh [--purge]
set -euo pipefail

PURGE=false
if [ "${1:-}" = "--purge" ]; then
	PURGE=true
fi

if [ "$(id -u)" -eq 0 ]; then
	SUDO=""
else
	SUDO="sudo"
fi

if [ -n "$SUDO" ]; then
	echo "==> Checking sudo access (password prompt may appear once)"
	sudo -v
fi

# ---- stop the daemon ----
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] && $SUDO systemctl list-unit-files dockweaved.service >/dev/null 2>&1; then
	echo "==> Stopping and disabling dockweaved.service"
	$SUDO systemctl disable --now dockweaved 2>/dev/null || true
	$SUDO rm -f /etc/systemd/system/dockweaved.service
	$SUDO systemctl daemon-reload
fi
if $SUDO pkill -x dockweaved 2>/dev/null; then
	echo "==> Stopped running dockweaved"
	for _ in $(seq 1 50); do
		$SUDO pgrep -x dockweaved >/dev/null 2>&1 || break
		sleep 0.1
	done
fi

# ---- binaries ----
echo "==> Removing binaries"
$SUDO rm -f /usr/local/bin/dockweave /usr/local/bin/dockweaved
$SUDO rm -f "$HOME/.local/bin/dockweave" "$HOME/.local/bin/dockweaved"

# ---- sockets, config ----
echo "==> Removing sockets and /etc/dockweave"
$SUDO rm -rf /run/dockweave /etc/dockweave

# ---- data (only with --purge) ----
if $PURGE; then
	echo "==> --purge: deleting /var/lib/dockweave (agents, memories, snapshots, registry)"
	$SUDO rm -rf /var/lib/dockweave
else
	if [ -d /var/lib/dockweave ]; then
		echo "==> Keeping your data in /var/lib/dockweave (use --purge to delete)"
	fi
fi

cat <<EOF

== DockWeave removed ==
  removed: daemon, /usr/local/bin/dockweave{,d}, /etc/dockweave, /run/dockweave
$(if $PURGE; then echo "  purged: /var/lib/dockweave"; else echo "  kept:  /var/lib/dockweave (data)"; fi)

The "dockweave" group was kept (delete manually if wanted: groupdel dockweave).
You may also undo the group membership of your user: deluser USER dockweave
EOF
