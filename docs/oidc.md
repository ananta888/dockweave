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
   `exp`), and only `iss` + `sub` plus the mapped roles are kept.
4. An AES-GCM sealed session cookie (`dockweave_oidc`, HttpOnly, SameSite=Lax)
   is set and the browser is redirected to the app root.
5. `/oauth/logout` clears the session and redirects to the IdP end-session
   endpoint.

The daemon API remains unchanged: it still authorizes by `SO_PEERCRED`. The
OIDC principal is only used at the web gateway.

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
- `internal/oidc/service.go` – the application service that orchestrates the
  flow (state/nonce/PKCE, callback, logout).

## Security notes

- Only `iss` + `sub` and the mapped roles are retained; profile claims are
  never copied into sessions or logs.
- The session cookie is encrypted and signed (AES-GCM); tampering yields
  `invalid session`.
- `state` and `nonce` are single-use and expire; replayed callbacks are
  rejected.
- Login remains rate limited and the response headers remain hardened.
