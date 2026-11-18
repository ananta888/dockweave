#!/usr/bin/env bash
# Point the user-level broker at the shared socket group so the unprivileged
# daemon (dockweave) can connect. Run as the broker user (krusty), no sudo.
set -euo pipefail

DWUID=$(id -u dockweave 2>/dev/null || echo 997)
D="$HOME/.config/systemd/user/dockweave-broker.service.d"
mkdir -p "$D"

cat > "$D/override.conf" <<EOF
[Service]
ExecStart=
ExecStart=%h/dockweave/bin/dockweave-broker --data-dir %h/.local/share/dockweave/broker --listen 172.17.0.1:8787 --admin-uids 0,1000,$DWUID --socket-mode 0660 --socket-group dockweave
EOF

systemctl --user daemon-reload
systemctl --user restart dockweave-broker
echo "broker restarted; socket group:"
ls -l "$HOME/.local/share/dockweave/broker/admin.sock"
