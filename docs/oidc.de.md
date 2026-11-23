# OpenID-Connect-Anmeldung (Keycloak)

DockWeave kann die Web-Anmeldung an einen beliebigen OpenID-Connect-IdP
delegieren (Keycloak, Zitadel, Authentik, Dex, …). Der statische Token bleibt
Standard und dient als Fallback, wenn der IdP nicht erreichbar ist.

## Ablauf

Authorization-Code mit PKCE und Nonce: `/oauth/login` erzeugt `state`, `nonce`
und einen PKCE-Verifier, speichert sie 10 Minuten und leitet zum IdP um. Der
IdP leitet zurück zu `/oauth/callback` mit `code` und `state`. Der Code wird
getauscht, das `id_token` verifiziert (Signatur, `iss`, `aud`, `exp`) und nur
`iss` + `sub` sowie die gemappten Rollen behalten. Ein AES-GCM-versiegeltes
Session-Cookie (`dockweave_oidc`, HttpOnly, SameSite=Lax) wird gesetzt.
`/oauth/logout` löscht die Session und leitet zum End-Session-Endpunkt.

Die Daemon-API bleibt unverändert (`SO_PEERCRED`); der OIDC-Principal wirkt
nur am Web-Gateway.

### IdP beim Gateway-Start nicht erreichbar

Die Discovery blockiert das Gateway nie. Ist der IdP beim Start des Gateways
nicht erreichbar (etwa weil Keycloak beim Booten erst danach startet), bleibt
der IdP-Button auf der Anmeldeseite, das Gateway wiederholt die Discovery im
Hintergrund (5 s, verdoppelnd bis 1 min; einmal als `oidc issuer
unreachable…` geloggt, danach `oidc ready`), und ein Anmeldeversuch in der
Zwischenzeit antwortet mit `503` und `Retry-After`, statt still zu scheitern.
Sobald der IdP läuft, ist kein Gateway-Neustart nötig; der statische Token
funktioniert durchgehend.

## Mitgeliefertes Keycloak (Standard)

Ein startfertiges Keycloak mit `dockweave`-Realm liegt bei:

```bash
./scripts/keycloak-up.sh
```

Es startet Keycloak auf `127.0.0.1:8080` (nur Loopback, auch aus
Agent-Containern nicht erreichbar), importiert das `dockweave`-Realm (Client
`dockweave`, Gruppen `dockweave-admin`/`dockweave-monitor`, Benutzer `krusty`
(Hauptadmin), `user` (Standard) und `monitor`) und druckt die passenden
`dockweave web`-OIDC-Einstellungen. Dateien unter `deployments/keycloak/`.

Das Repository enthält **keine Zugangsdaten**. Beim ersten Lauf erzeugt das
Skript sie in 0600-Dateien unter `~/.config/dockweave/`:

| Datei | Inhalt |
|---|---|
| `keycloak.env` | Master-Realm-Admin (`KC_ADMIN_USER`, `KC_ADMIN_PASSWORD`) |
| `keycloak-users.env` | Zufallspasswörter für `krusty`, `user`, `monitor` |
| `oidc.env` | `DOCKWEAVE_OIDC_CLIENT_SECRET`, von Keycloak erzeugt |

`./scripts/keycloak-up.sh --rotate` rotiert alle drei. Der Passwort-Grant
ist am Client `dockweave` abgeschaltet (das Gateway nutzt nur den
Authorization-Code-Flow mit PKCE). In einer systemd-User-Unit das Secret
per `EnvironmentFile=%h/.config/dockweave/oidc.env` laden, nicht per
`Environment=`-Zeile — Drop-ins sind für alle lesbar.

DockWeave braucht nur eine Issuer-URL, daher funktionieren dieselben Flags
mit jedem IdP. Keycloak kann selbst als Identity-Broker externe IdPs
federieren; das wird in der Keycloak-Admin-Console konfiguriert.

## Konfiguration

```bash
dockweave web \
  --oidc-issuer https://keycloak.example/realms/dockweave \
  --oidc-client-id dockweave \
  --oidc-client-secret '…' \
  --oidc-admin-groups dockweave-admin \
  --oidc-monitor-groups dockweave-monitor \
  --oidc-session-key <32-byte-hex>
```

Dieselben Werte gibt es als `DOCKWEAVE_OIDC_*`-Umgebungsvariablen.
`--oidc-connect-to host:port=addr:port[,…]` (`DOCKWEAVE_OIDC_CONNECT_TO`)
leitet nur die eigenen IdP-Verbindungen des Gateways um (Discovery, JWKS,
Token-Austausch), wie `curl --connect-to`; TLS wird weiterhin gegen den
Issuer-Host geprüft. Nötig, wenn der öffentliche Issuer-Name vom Gateway-Host
aus nicht routbar ist (LAN-Zugang aus WSL, siehe `deployments/lan-access`).
`--oidc-session-key` verschlüsselt das Session-Cookie; leer erzeugt einen
Zufallsschlüssel (Session überlebt dann keinen Gateway-Neustart).

## Struktur (SOLID / DDD)

- `internal/oidc/domain.go` – reine Domäne (`Principal`, `Session`, `Role`).
- `internal/oidc/provider.go` – `Provider`-Port + go-oidc-Adapter.
- `internal/oidc/session.go` – `SessionStore`-Port + AES-GCM-Cookie-Adapter.
- `internal/oidc/lazy.go` – `LazyProvider`: Discovery bei Bedarf mit
  gedrosselten Wiederholungen und Hintergrund-Warm-up.
- `internal/oidc/service.go` – Anwendungsdienst für den Login-Fluss.

## Audit

Erfolgreiche Anmeldung und Abmeldung werden doppelt erfasst: als strukturierte
Logzeile (`slog`, nur Event + `iss` + `sub`) und als best-effort, hash-verkettetes
Daemon-Event (`POST /api/v1/audit`, Typ `auth.oidc`) zur Manipulationssicherheit.
Profile-Claims und Raw-Tokens werden nie geschrieben. Damit das Daemon-Event
angenommen wird, muss die Gateway-UID in `ADMIN_UIDS` stehen, z. B.
`ADMIN_UIDS=0:1000:<gateway-uid>`.

## Sicherheit

In der Session bleiben `iss` + `sub`, die abgeleiteten Rollen, die
IdP-Gruppen (Berechtigungsdaten für Ticket-Rollen, ADR 0003 D-6a) und
`preferred_username` als Anzeigename für die Zuordnung in gemeinsamen
Tickets. Andere Profil-Claims (E-Mail, voller Name, …) werden nie in
Sessions oder Logs kopiert. Das Gateway reicht den angemeldeten Menschen im
Header `X-DockWeave-Actor` (ID `iss|sub`, Benutzername, Gruppen) an den
Daemon weiter und verwirft einen vom Client gesendeten Wert immer; der
Daemon beachtet ihn nur von Peers in `GATEWAY_UIDS`, die zugleich
Admin-Peers sind (SD-022). Das Cookie ist verschlüsselt/signiert; `state`
und `nonce` sind einmalig und laufen ab. Rate-Limit und Header-Härtung
bleiben aktiv.
