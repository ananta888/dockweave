# DockWeave im WLAN/LAN (TLS)

Das Web-Gateway bleibt auf `127.0.0.1:8790`. Ein Caddy-Proxy davor spricht
TLS mit eigener lokaler CA und ist nur im Heimnetz erreichbar:

| Port | Ziel | Durchgelassen |
|---|---|---|
| 8443 | Web-Gateway (UI, API, Terminals) | alles (Token- oder IdP-Login nötig) |
| 8444 | Keycloak | nur `/realms/dockweave/*` und `/resources/*`; Admin-Konsole und Master-Realm → 403 |

## Vorher (Pflicht)

Die mitgelieferten Keycloak-Demo-Passwörter (`krusty/krusty`, `user/user`,
`monitor/monitor`) sind im Netz ein offenes Tor: in der Admin-Konsole
(`http://127.0.0.1:8080/admin`, nur lokal) starke Passwörter setzen oder die
Demo-User deaktivieren. Das Gateway-Token (`~/.config/dockweave/web.env`)
gibt volle Admin-Rechte – nicht weitergeben.

## Einrichten

1. WSL: `scripts/lan-access.sh up [LAN_HOST] [LAN_IP]` (Standard:
   `<hostname>.fritz.box` und dessen IPv4). Startet den Proxy, stellt den
   Keycloak-Issuer auf `https://LAN_HOST:8444/realms/dockweave` um, trägt die
   neue Callback-URL im Client `dockweave` ein und schreibt das Gateway-Drop-in
   `~/.config/systemd/user/dockweave-web.service.d/zz-lan-access.conf`.
2. WSL: der CA vertrauen und Gateway neu starten (das Gateway prüft Keycloak
   per TLS):
   ```bash
   sudo cp ~/.local/share/dockweave/lan-root-ca.crt /usr/local/share/ca-certificates/dockweave-lan-root-ca.crt
   sudo update-ca-certificates
   systemctl --user restart dockweave-web
   ```
3. Windows, PowerShell **als Administrator** (WSL2 im NAT-Modus):
   ```powershell
   Set-NetConnectionProfile -InterfaceAlias WLAN -NetworkCategory Private
   netsh interface portproxy add v4tov4 listenaddress=<LAN_IP> listenport=8443 connectaddress=127.0.0.1 connectport=8443
   netsh interface portproxy add v4tov4 listenaddress=<LAN_IP> listenport=8444 connectaddress=127.0.0.1 connectport=8444
   New-NetFirewallRule -DisplayName "DockWeave LAN (TLS)" -Direction Inbound -Protocol TCP -LocalPort 8443,8444 -RemoteAddress LocalSubnet -Profile Private -Action Allow
   ```
   Die Regel gilt nur im Netzwerkprofil „Privat“ und nur für das lokale
   Subnetz: in einem fremden WLAN (Profil „Öffentlich“) bleibt alles zu.
   Am besten in der Fritzbox dem Rechner immer dieselbe IPv4 zuweisen.
4. Jeder Client (auch Windows auf diesem Rechner): `lan-root-ca.crt` als
   vertrauenswürdige Stammzertifizierungsstelle importieren (Windows:
   Doppelklick → Zertifikat installieren → Lokaler Computer →
   „Vertrauenswürdige Stammzertifizierungsstellen“; macOS: Schlüsselbund,
   „Immer vertrauen“; Firefox: Einstellungen → Zertifikate → Importieren).
   Dann `https://LAN_HOST:8443/` öffnen.

Danach führt die IdP-Anmeldung auch lokal über `https://LAN_HOST:8443`
(Callback-URL); der Token-Login über `http://127.0.0.1:8790` bleibt.

## Zurück auf nur lokal

`scripts/lan-access.sh down` (Proxy aus, Keycloak und Gateway wie vorher)
und die ausgegebenen Windows-Befehle ausführen. `scripts/lan-access.sh
status` zeigt den Zustand.

## Sicherheit

- Das Gateway lauscht weiter nur auf Loopback; Keycloak (`:8080`) ist nur
  innerhalb von WSL veröffentlicht, ohne Windows-Weiterleitung. Aus dem
  Netz erreichbar ist nur der Proxy, und das nur über die
  Windows-Portweiterleitung.
- Das Gateway wertet `X-Forwarded-For` nur von einem Loopback-Proxy aus und
  nur den letzten Eintrag (Login-Drossel nicht umgehbar); Caddy ersetzt
  vom Client gesendete Header.
- WebSockets prüfen weiter Origin gegen Host; der Proxy reicht den Host durch.
- Die Proxy-CA ist privat (Schlüssel im Volume `lan-access_caddy-data`),
  Blattzertifikate erneuert Caddy automatisch.
