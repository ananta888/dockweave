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
`--oidc-session-key` verschlüsselt das Session-Cookie; leer erzeugt einen
Zufallsschlüssel (Session überlebt dann keinen Gateway-Neustart).

## Struktur (SOLID / DDD)

- `internal/oidc/domain.go` – reine Domäne (`Principal`, `Session`, `Role`).
- `internal/oidc/provider.go` – `Provider`-Port + go-oidc-Adapter.
- `internal/oidc/session.go` – `SessionStore`-Port + AES-GCM-Cookie-Adapter.
- `internal/oidc/service.go` – Anwendungsdienst für den Login-Fluss.

## Audit

Erfolgreiche Anmeldung und Abmeldung werden doppelt erfasst: als strukturierte
Logzeile (`slog`, nur Event + `iss` + `sub`) und als best-effort, hash-verkettetes
Daemon-Event (`POST /api/v1/audit`, Typ `auth.oidc`) zur Manipulationssicherheit.
Profile-Claims und Raw-Tokens werden nie geschrieben. Damit das Daemon-Event
angenommen wird, muss die Gateway-UID in `ADMIN_UIDS` stehen, z. B.
`ADMIN_UIDS=0:1000:<gateway-uid>`.

## Sicherheit

Nur `iss` + `sub` und Rollen werden behalten; Profile-Claims werden nie in
Sessions oder Logs kopiert. Das Cookie ist verschlüsselt/signiert; `state`
und `nonce` sind einmalig und laufen ab. Rate-Limit und Header-Härtung
bleiben aktiv.
