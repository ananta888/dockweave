#!/usr/bin/env bash
# LAN access to the DockWeave web UI over TLS (deployments/lan-access).
#
#   scripts/lan-access.sh up   [LAN_HOST] [LAN_IP]   # start proxy, switch Keycloak + gateway
#   scripts/lan-access.sh down                        # back to loopback-only
#   scripts/lan-access.sh status
#
# WSL side only. Trusting the CA (sudo) and the Windows port forwarding and
# firewall rule are printed as instructions; see deployments/lan-access/README*.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
PROXY_DIR="$HERE/deployments/lan-access"
KC_DIR="$HERE/deployments/keycloak"
DROPIN_DIR="$HOME/.config/systemd/user/dockweave-web.service.d"
DROPIN="$DROPIN_DIR/zz-lan-access.conf"
CA_OUT="$HOME/.local/share/dockweave/lan-root-ca.crt"
STATE="$HOME/.config/dockweave/lan-access.env"

cmd="${1:-status}"

# Keycloak compose needs the locally generated admin password
# (scripts/keycloak-up.sh); there is deliberately no default.
KC_ENV="$HOME/.config/dockweave/keycloak.env"
if [ "$cmd" != status ]; then
	if [ ! -f "$KC_ENV" ]; then
		echo "error: $KC_ENV missing; run scripts/keycloak-up.sh first" >&2
		exit 1
	fi
	# shellcheck disable=SC1090
	. "$KC_ENV"
	export KC_ADMIN_USER KC_ADMIN_PASSWORD
fi

kc_admin() {
	# Credentials stay inside the container (bootstrap admin from its env).
	docker exec dockweave-keycloak sh -c '/opt/keycloak/bin/kcadm.sh config credentials --server http://localhost:8080 --realm master --user "$KC_BOOTSTRAP_ADMIN_USERNAME" --password "$KC_BOOTSTRAP_ADMIN_PASSWORD" >/dev/null'
}

kc_client_urls() { # $1 = add|remove, $2 = base URL of the gateway
	kc_admin
	local raw id
	raw="$(docker exec dockweave-keycloak /opt/keycloak/bin/kcadm.sh get clients -r dockweave -q clientId=dockweave --fields id,redirectUris,webOrigins)"
	id="$(printf '%s' "$raw" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])')"
	printf '%s' "$raw" | python3 -c '
import json, sys
op, base = sys.argv[1], sys.argv[2]
c = json.load(sys.stdin)[0]
red, org = c.get("redirectUris", []), c.get("webOrigins", [])
cb = base + "/oauth/callback"
if op == "add":
    red = red + [cb] if cb not in red else red
    org = org + [base] if base not in org else org
else:
    red = [u for u in red if u != cb]
    org = [u for u in org if u != base]
json.dump({"redirectUris": red, "webOrigins": org}, open(sys.argv[3], "w"))
' "$1" "$2" /tmp/dw-kc-client.$$.json
	docker cp /tmp/dw-kc-client.$$.json dockweave-keycloak:/tmp/dw-kc-client.json >/dev/null
	rm -f /tmp/dw-kc-client.$$.json
	docker exec dockweave-keycloak /opt/keycloak/bin/kcadm.sh update "clients/$id" -r dockweave -f /tmp/dw-kc-client.json
}

case "$cmd" in
up)
	LAN_HOST="${2:-${LAN_HOST:-$(hostname | tr '[:upper:]' '[:lower:]').fritz.box}}"
	LAN_IP="${3:-${LAN_IP:-$(getent ahostsv4 "$LAN_HOST" | awk 'NR==1{print $1}')}}"
	if [ -z "$LAN_IP" ]; then
		echo "error: cannot resolve $LAN_HOST to an IPv4 address; pass LAN_IP explicitly" >&2
		exit 1
	fi
	echo "==> LAN host $LAN_HOST ($LAN_IP)"
	export LAN_HOST LAN_IP

	echo "==> Starting TLS proxy (dockweave-lan-proxy)"
	(cd "$PROXY_DIR" && docker compose up -d)

	echo "==> Switching Keycloak issuer to https://$LAN_HOST:8444"
	(cd "$KC_DIR" && docker compose -f docker-compose.yml -f docker-compose.lan.yml up -d --wait)
	kc_client_urls add "https://$LAN_HOST:8443"

	echo "==> Exporting the proxy root CA to $CA_OUT"
	mkdir -p "$(dirname "$CA_OUT")"
	for _ in $(seq 1 30); do
		docker cp dockweave-lan-proxy:/data/caddy/pki/authorities/local/root.crt "$CA_OUT" >/dev/null 2>&1 && break
		sleep 1
	done
	[ -s "$CA_OUT" ] || { echo "error: proxy root CA not found" >&2; exit 1; }

	echo "==> Gateway drop-in $DROPIN"
	mkdir -p "$DROPIN_DIR" "$(dirname "$STATE")"
	cat >"$DROPIN" <<EOF
# Written by scripts/lan-access.sh up; remove with scripts/lan-access.sh down.
[Service]
Environment=DOCKWEAVE_OIDC_ISSUER=https://$LAN_HOST:8444/realms/dockweave
Environment=DOCKWEAVE_OIDC_REDIRECT_URL=https://$LAN_HOST:8443/oauth/callback
EOF
	printf 'LAN_HOST=%s\nLAN_IP=%s\n' "$LAN_HOST" "$LAN_IP" >"$STATE"
	systemctl --user daemon-reload

	cat <<EOF

WSL side is ready. Remaining steps (see deployments/lan-access/README.de.md):

1) Trust the proxy CA inside WSL (the gateway verifies Keycloak over TLS), then restart the gateway:
     sudo cp $CA_OUT /usr/local/share/ca-certificates/dockweave-lan-root-ca.crt && sudo update-ca-certificates
     systemctl --user restart dockweave-web

2) Windows, PowerShell as administrator:
     Set-NetConnectionProfile -InterfaceAlias WLAN -NetworkCategory Private
     netsh interface portproxy add v4tov4 listenaddress=$LAN_IP listenport=8443 connectaddress=127.0.0.1 connectport=8443
     netsh interface portproxy add v4tov4 listenaddress=$LAN_IP listenport=8444 connectaddress=127.0.0.1 connectport=8444
     New-NetFirewallRule -DisplayName "DockWeave LAN (TLS)" -Direction Inbound -Protocol TCP -LocalPort 8443,8444 -RemoteAddress LocalSubnet -Profile Private -Action Allow

3) On every client (and in Windows on this machine): import $CA_OUT as a trusted root CA,
   then open https://$LAN_HOST:8443/
EOF
	;;
down)
	[ -f "$STATE" ] && . "$STATE"
	if [ -n "${LAN_HOST:-}" ] && docker ps --format '{{.Names}}' | grep -qx dockweave-keycloak; then
		kc_client_urls remove "https://$LAN_HOST:8443" || true
	fi
	echo "==> Keycloak back to http://127.0.0.1:8080"
	(cd "$KC_DIR" && docker compose up -d --wait)
	echo "==> Stopping TLS proxy"
	(cd "$PROXY_DIR" && LAN_HOST=x LAN_IP=x docker compose down)
	rm -f "$DROPIN" "$STATE"
	systemctl --user daemon-reload
	systemctl --user restart dockweave-web
	cat <<EOF
Back to loopback-only. On Windows (admin PowerShell), remove forwarding and rule:
     netsh interface portproxy delete v4tov4 listenaddress=${LAN_IP:-<LAN_IP>} listenport=8443
     netsh interface portproxy delete v4tov4 listenaddress=${LAN_IP:-<LAN_IP>} listenport=8444
     Remove-NetFirewallRule -DisplayName "DockWeave LAN (TLS)"
EOF
	;;
status)
	[ -f "$STATE" ] && cat "$STATE" || echo "LAN access: off"
	docker ps --filter name=dockweave-lan-proxy --format '{{.Names}} {{.Status}}'
	[ -f "$DROPIN" ] && cat "$DROPIN"
	;;
*)
	echo "usage: $0 up [LAN_HOST] [LAN_IP] | down | status" >&2
	exit 2
	;;
esac
