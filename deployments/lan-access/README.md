# DockWeave on the LAN (TLS)

The web gateway stays on `127.0.0.1:8790`. A Caddy proxy in front of it
terminates TLS with its own local CA and is reachable only on the home
network:

| Port | Target | Allowed |
|---|---|---|
| 8443 | web gateway (UI, API, terminals) | everything (token or IdP login required) |
| 8444 | Keycloak | only `/realms/dockweave/*` and `/resources/*`; admin console and master realm → 403 |

## Before you start (mandatory)

Run `scripts/keycloak-up.sh` once: it generates random passwords for the
Keycloak admin and the realm users plus the client secret into 0600 files
under `~/.config/dockweave/` (see `docs/oidc.md`); the repository ships no
credentials and `lan-access.sh` refuses to start without them. Disable users
you do not need (`user`, `monitor`) in the admin console
(`http://127.0.0.1:8080/admin`, local only). The gateway token
(`~/.config/dockweave/web.env`) grants full admin rights – do not share it.

## Setup

1. WSL: `scripts/lan-access.sh up [LAN_HOST] [LAN_IP]` (default:
   `<hostname>.fritz.box` and its IPv4). Starts the proxy, switches the
   Keycloak issuer to `https://LAN_HOST:8444/realms/dockweave`, registers the
   new callback URL on client `dockweave` and writes the gateway drop-in
   `~/.config/systemd/user/dockweave-web.service.d/zz-lan-access.conf`.
2. WSL: trust the CA and restart the gateway (it verifies Keycloak over TLS):
   ```bash
   sudo cp ~/.local/share/dockweave/lan-root-ca.crt /usr/local/share/ca-certificates/dockweave-lan-root-ca.crt
   sudo update-ca-certificates
   systemctl --user restart dockweave-web
   ```
3. Windows, PowerShell **as administrator** (WSL2 in NAT mode):
   ```powershell
   Set-NetConnectionProfile -InterfaceAlias WLAN -NetworkCategory Private
   netsh interface portproxy add v4tov4 listenaddress=<LAN_IP> listenport=8443 connectaddress=127.0.0.1 connectport=8443
   netsh interface portproxy add v4tov4 listenaddress=<LAN_IP> listenport=8444 connectaddress=127.0.0.1 connectport=8444
   New-NetFirewallRule -DisplayName "DockWeave LAN (TLS)" -Direction Inbound -Protocol TCP -LocalPort 8443,8444 -RemoteAddress LocalSubnet -Profile Private -Action Allow
   ```
   The rule applies only to the "Private" network profile and the local
   subnet; on a foreign Wi-Fi ("Public") everything stays closed. Give the
   machine a fixed IPv4 in the router.
4. Every client (including Windows on this machine): import
   `lan-root-ca.crt` as a trusted root CA, then open `https://LAN_HOST:8443/`.

Afterwards the IdP login also runs through `https://LAN_HOST:8443` locally
(callback URL); token login on `http://127.0.0.1:8790` keeps working.

## Back to local only

`scripts/lan-access.sh down` (proxy off, Keycloak and gateway as before) and
run the printed Windows commands. `scripts/lan-access.sh status` shows the
state.

## Security

- The gateway keeps listening on loopback only; Keycloak (`:8080`) is
  published inside WSL only, without Windows forwarding. Only the proxy is
  reachable from the network, and only through the Windows port forwarding.
- The gateway honours `X-Forwarded-For` only from a loopback proxy and only
  its last entry (the login limiter cannot be bypassed); Caddy replaces
  client-supplied forwarding headers.
- WebSockets still check Origin against Host; the proxy preserves Host.
- The proxy CA is private (key in volume `lan-access_caddy-data`); Caddy
  renews leaf certificates automatically.
