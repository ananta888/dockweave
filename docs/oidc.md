# OpenID Connect (Keycloak) sign-in

DockWeave can delegate web login to any OpenID Connect IdP (Keycloak, Zitadel,
Authentik, Dex, …). The static token remains the default and acts as a
break-glass fallback when the IdP is unreachable.

## Flow

Authorization-code with PKCE and nonce:

1. `/oauth/login` generates `state`, `nonce` and a PKCE verifier, stores them
   for 10 minutes, and redirects to the IdP.
2. The IdP redirects back to `/oauth/callback` with `code` and `state`.
3. The code is exchanged, the `id_token` is verified (signature, `iss`, `aud`,
   `exp`) and its `nonce` is bound to the login attempt, and only `iss` +
   `sub` plus the mapped roles are kept.
4. An AES-GCM sealed session cookie (`dockweave_oidc`, HttpOnly, SameSite=Lax)
   is set and the browser is redirected to the app root.
5. `/oauth/logout` clears the session and redirects to the IdP end-session
   endpoint.

The daemon API remains unchanged: it still authorizes by `SO_PEERCRED`. The
OIDC principal is only used at the web gateway.

### IdP not reachable at gateway start

Discovery never blocks the gateway. If the IdP is not reachable when the
gateway starts (for example Keycloak starting after it on boot), the IdP
button stays on the login page, the gateway retries discovery in the
background (5 s, doubling up to 1 min; logged once as `oidc issuer
unreachable…`, then `oidc ready`), and a login attempt in the meantime answers
`503` with `Retry-After` instead of failing silently. No gateway restart is
needed once the IdP is up; the static token keeps working throughout.

## Bundled Keycloak (standard)

A ready-to-run Keycloak with a `dockweave` realm is included:

```bash
./scripts/keycloak-up.sh
```

It starts Keycloak on `127.0.0.1:8080`, imports the `dockweave` realm (client
`dockweave`, groups `dockweave-admin`/`dockweave-monitor`, demo users
`krusty`/`krusty` (Hauptadmin),
`user`/`user` (standard) and `monitor`/`monitor`) and prints the exact `dockweave web`
OIDC settings. Files live in `deployments/keycloak/` (`docker-compose.yml`,
`realm-dockweave.json`). Change the dev client secret before any real use.

Because DockWeave only needs an issuer URL, the same flags work with any IdP
(Keycloak, Zitadel, Authentik, Dex). Keycloak can itself act as an identity
broker to federate external IdPs; that is configured in the Keycloak admin
console, not in DockWeave.

## Configuration

```bash
dockweave web \
  --oidc-issuer https://keycloak.example/realms/dockweave \
  --oidc-client-id dockweave \
  --oidc-client-secret '…' \
  --oidc-admin-groups dockweave-admin \
  --oidc-monitor-groups dockweave-monitor \
  --oidc-session-key <32-byte-hex>
```

The same settings are available as `DOCKWEAVE_OIDC_*` environment variables.
`--oidc-session-key` encrypts the session cookie; when empty a random key is
generated, so sessions do not survive a gateway restart.

`RolesClaim` defaults to `groups`; set `--oidc-roles-claim roles` for realms
that expose roles instead. Groups listed in `AdminGroups`/`MonitorGroups` are
mapped to the `admin`/`monitor` capabilities.

## Structure (SOLID / DDD)

- `internal/oidc/domain.go` – pure domain: `Principal`, `Session`, `Role`,
  errors. No external dependencies.
- `internal/oidc/provider.go` – the `Provider` port plus the go-oidc adapter
  (discovery, authorization URL, exchange, role mapping).
- `internal/oidc/session.go` – the `SessionStore` port plus the AES-GCM cookie
  adapter.
- `internal/oidc/lazy.go` – `LazyProvider`: on-demand discovery with
  rate-limited retries and a background warm-up.
- `internal/oidc/service.go` – the application service that orchestrates the
  flow (state/nonce/PKCE, callback, logout).

## Audit

Successful login and logout are recorded twice:

- a structured log line (`slog`, event + `iss` + `sub` only), and
- a best-effort, hash-chained daemon event (`POST /api/v1/audit`, type
  `auth.oidc`) for tamper resistance.

Profile claims and raw tokens are never written to either channel. For the
daemon event to be accepted, the gateway's uid must be in `ADMIN_UIDS`
(`/etc/dockweave/dockweaved.conf`), e.g. `ADMIN_UIDS=0:1000:<gateway-uid>`.

## Security notes

- Retained in the session: `iss` + `sub`, the mapped roles, the IdP groups
  (authorization data for ticket roles, ADR 0003 D-6a) and
  `preferred_username` as display handle for attribution in shared tickets.
  No other profile claim (email, full name, …) is copied into sessions or
  logs.
- The gateway forwards the signed-in human to the daemon in
  `X-DockWeave-Actor` (id `iss|sub`, username, groups) and always drops a
  client-supplied value. The daemon honours it only from peers in
  `GATEWAY_UIDS` that are also admin peers (SD-022).
- The session cookie is encrypted and signed (AES-GCM); tampering yields
  `invalid session`.
- `state` and `nonce` are single-use and expire; replayed callbacks are
  rejected.
- Login remains rate limited and the response headers remain hardened.
