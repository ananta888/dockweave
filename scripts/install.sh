#!/usr/bin/env bash
# DockWeave installer: builds, installs, configures and starts the daemon.
#
# Usage:  ./scripts/install.sh
# Needs sudo once (the script never needs to be run via sudo). Afterwards the
# CLI works in the very same shell: binaries land on /usr/local/bin and the
# daemon socket is owned by your primary group.
set -euo pipefail

cd "$(dirname "$0")/.."

# ---- resolve the real user (works with and without sudo) ----
if [ "$(id -u)" -eq 0 ]; then
	SUDO=""
	REAL_USER="${SUDO_USER:-root}"
else
	SUDO="sudo"
	REAL_USER="${SUDO_USER:-$USER}"
fi
REAL_UID="$(id -u "$REAL_USER")"
REAL_GROUP="$(id -gn "$REAL_USER")"

if [ -n "$SUDO" ]; then
	echo "==> Checking sudo access (password prompt may appear once)"
	sudo -v
fi

# ---- build ----
echo "==> Building dockweave"
# -buildvcs=false: the build must not depend on git metadata (releases get
# their version from VERSION/make; containers may run as foreign root).
if ! make build GOFLAGS="${GOFLAGS:-} -buildvcs=false"; then
	echo "error: build failed — is the Go toolchain (>= 1.27) installed?" >&2
	echo "       make finds it on PATH or at ~/.local/go/bin/go" >&2
	exit 1
fi

# ---- install binaries (system-wide: on PATH in every shell) ----
echo "==> Installing to /usr/local/bin"
$SUDO install -Dm755 bin/dockweave  /usr/local/bin/dockweave
$SUDO install -Dm755 bin/dockweave-tui /usr/local/bin/dockweave-tui
$SUDO install -Dm755 bin/dockweaved /usr/local/bin/dockweaved
# Remove stale user-local copies: they shadow /usr/local/bin when
# ~/.local/bin comes first in PATH and would silently run old builds.
for f in "$HOME/.local/bin/dockweave" "$HOME/.local/bin/dockweave-tui" "$HOME/.local/bin/dockweaved"; do
	if [ -e "$f" ]; then
		echo "==> Removing stale $f (shadows /usr/local/bin)"
		rm -f "$f"
	fi
done

# ---- admin group (for future logins; see SOCKET_GROUP note below) ----
$SUDO groupadd -f dockweave 2>/dev/null || true
$SUDO usermod -aG dockweave "$REAL_USER" 2>/dev/null || true

# ---- configuration (kept if it already exists) ----
CONF=/etc/dockweave/dockweaved.conf
$SUDO mkdir -p /etc/dockweave
if $SUDO test -f "$CONF"; then
	echo "==> $CONF exists — keeping it:"
	$SUDO cat "$CONF"
else
	echo "==> Writing $CONF"
	$SUDO tee "$CONF" >/dev/null <<EOF
# DockWeave daemon configuration (written by scripts/install.sh)
#
# Roles (SO_PEERCRED identifies, this policy authorizes):
#   admin   — full API and terminals (ADMIN_UIDS)
#   monitor — read-only GET/HEAD (MONITOR_UIDS)
# Unknown UIDs are denied.
ADMIN_UIDS=0:$REAL_UID
#
# Socket group (0660 root:GROUP). Your primary group makes the CLI work
# immediately in this shell. After your next login you may switch this to
# "dockweave" (all members of that group may connect).
SOCKET_GROUP=$REAL_GROUP
EOF
fi

# ---- stop a previously running daemon (idempotent re-install) ----
if $SUDO pkill -x dockweaved 2>/dev/null; then
	echo "==> Stopped running dockweaved"
	for _ in $(seq 1 50); do
		$SUDO pgrep -x dockweaved >/dev/null 2>&1 || break
		sleep 0.1
	done
fi

# ---- start the daemon: systemd when available, else background ----
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
	echo "==> Installing and starting systemd unit"
	$SUDO tee /etc/systemd/system/dockweaved.service >/dev/null <<'EOF'
[Unit]
Description=DockWeave daemon
After=docker.service network.target

[Service]
ExecStart=/usr/local/bin/dockweaved
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
	$SUDO systemctl daemon-reload
	$SUDO systemctl enable --now dockweaved
else
	echo "==> Starting dockweaved in the background (no systemd)"
	echo "    logs: /var/log/dockweaved.log"
	$SUDO sh -c 'nohup /usr/local/bin/dockweaved >/var/log/dockweaved.log 2>&1 &'
fi

# ---- wait for the socket ----
SOCK=/run/dockweave/dockweaved.sock
echo "==> Waiting for $SOCK"
for _ in $(seq 1 50); do
	[ -S "$SOCK" ] && break
	sleep 0.2
done
if [ ! -S "$SOCK" ]; then
	echo "error: daemon did not come up. Check /var/log/dockweaved.log or:" >&2
	echo "       journalctl -u dockweaved" >&2
	exit 1
fi

# ---- verify the CLI as the real user (same shell usability) ----
echo "==> Verifying CLI access as $REAL_USER"
VERIFY="/usr/local/bin/dockweave status"
if [ "$(id -u)" -eq 0 ] && [ "$REAL_USER" != root ]; then
	su -s /bin/sh "$REAL_USER" -c "$VERIFY"
else
	$VERIFY
fi

cat <<EOF

== DockWeave installed ==
  binaries : /usr/local/bin/dockweave{,d}
  config   : $CONF
  daemon   : running (socket $SOCK)
  logs     : /var/log/dockweaved.log or: journalctl -u dockweaved

Try it now (works in this shell):
  dockweave workspace register myproject /srv/projects/myproject
  dockweave tui

Undo everything (keeps your data):
  ./scripts/uninstall.sh          # remove daemon, binaries and config
  ./scripts/uninstall.sh --purge  # ... and delete /var/lib/dockweave
EOF
