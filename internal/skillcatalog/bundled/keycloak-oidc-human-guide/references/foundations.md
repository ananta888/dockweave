# Foundations: Keycloak and OpenID Connect

## Safe first explanation

1. **Keep the identity profile in one place.** Under the strict baseline, Keycloak is the only persistent system of record for name, email address, profile postal address, and telephone number.
2. **Applications use a reference.** They normally store `iss` + `sub`, not a second profile. If they receive a profile value for display, they must not silently persist, cache, export, or log it.
3. **Audit at both boundaries.** Keycloak records login and administration events; the application records access to its own sensitive business data using references rather than names, addresses, or raw tokens.
4. **A role operator must not promote any account they control.** This includes their current account and a new or alternate account. Account creation, credential reset, eligibility, approval, and privileged role assignment therefore need separated control and negative tests.

“Only in Keycloak” means the only **persistent system of record** for the central identity profile. A value may still be processed briefly in a browser or service. Keycloak database backups remain personal-data locations inside the controlled identity lifecycle and need limited retention and deletion replay after restore. A delivery or invoice address that forms part of a required business record is a separate, documented exception rather than a duplicate login profile.

## The shortest useful mental model

Keycloak is the building's reception desk. It checks identity and hands out a signed pass. Each room still has to check whether that pass is valid for that room and whether the named person may perform the requested action.

This separates four questions:

| Layer | Question | Main owner |
|---|---|---|
| Identity | Which account is this? | Keycloak |
| Authentication | How was the person verified? | Keycloak and configured identity providers |
| Authorization | What may the account do here? | Keycloak supplies claims; the application enforces decisions |
| Audit | Who did what, when, and with which result? | Keycloak plus each application |

Confusing these layers causes most design errors.

## OAuth 2.0, OIDC, and Keycloak

OAuth 2.0 is a framework for delegated access. OpenID Connect (OIDC) adds a standardized identity layer. Keycloak implements an OpenID Provider and can also federate to external identity providers.

OIDC gives an application a trustworthy way to learn that a user authenticated. It does not automatically answer resource-specific questions such as:

- May this employee view case 74291?
- May this manager approve their own expense?
- Does this teacher belong to the course represented by this URL?

Those decisions remain application authorization. Roles and scopes in a token are inputs to the decision, not enforcement by themselves.

## Three tokens, three purposes

### ID token

The ID token is the login result for the client. It contains identity claims and is addressed to that client. Do not use it as a general API access token.

### Access token

The access token is presented to an API. The API validates it and checks the permissions relevant to the requested operation.

### Refresh token

The refresh token lets the client obtain fresh tokens without a complete login. It is longer-lived and more sensitive than a normal access token. Do not send it to APIs that do not need it or place it in logs.

## Recommended interactive flows

Use Authorization Code Flow with Proof Key for Code Exchange (PKCE), using the `S256` challenge method.

For a server-side application or Backend for Frontend (BFF), keep tokens on the server:

```text
Browser  -> Backend/BFF: start login
Backend  -> Browser:     redirect URL with state and PKCE challenge
Browser  -> Keycloak:    authorization request
Keycloak -> Browser:     redirect to BFF callback with short code
Browser  -> Backend/BFF: callback with code and state
Backend  -> Keycloak:    code + verifier
Keycloak -> Backend/BFF: tokens
Backend  -> Browser:     secure application session
```

For a public native application or browser client, the client creates the verifier and exchanges the code itself; it has no secret that can authenticate the installed application. Avoid exposing tokens to browser code when a BFF is practical for the risk and architecture.

Public clients **must** use PKCE under RFC 9700; confidential clients should use it as well. Use `S256`, an exact redirect URI, `state` or equivalent request binding, and the issuer returned by the authorization response. Do not use Resource Owner Password Credentials, called Direct Grant in Keycloak; RFC 9700 says this grant **must not** be used. The Implicit Flow should not be used in new designs.

## What an API must validate

Base64-decoding a JSON Web Token only reveals its text. It does not prove that Keycloak issued it.

An API should validate at least:

- signature against the realm's published JSON Web Keys;
- expected issuer in `iss`;
- intended audience in `aud`;
- expiry in `exp` and, when present, `nbf`;
- allowed token type and client context;
- required scope, role, or authorization input;
- resource-specific rules using server-side data.

Use a maintained OIDC/JWT library instead of handwritten cryptography. Cache discovered signing keys safely and handle key rotation.

## Stable user identity: `iss` plus `sub`

OIDC defines `sub` as a locally unique, never-reassigned subject identifier within an issuer. Therefore the durable identity is:

```text
iss = https://login.example.org/realms/company
sub = 8f53b5c1-...
```

Store both logically, even when an installation currently has only one realm.

The pair is stable only for that issuer. A realm move, issuer/hostname change, identity-provider replacement, import, restore into a different realm, or switch between public and pairwise subjects needs an explicit identity-migration map; do not assume matching `sub` values preserve a person across issuers.

Do not use these as durable keys:

- email address,
- username,
- display name,
- phone number.

They can change, may be recycled, and have no OIDC stability guarantee.

Treat `sub` as an opaque string. Do not parse it, assume it always has UUID syntax, or derive business information from it.

## Public versus pairwise subjects

A public subject gives multiple clients the same `sub`. This is convenient, but makes cross-application correlation easier.

A pairwise subject gives the same person a different `sub` for each client sector. Keycloak provides a SHA-256 pairwise subject mapper.

Choose pairwise subjects when unrelated applications should not correlate people. Before enabling them, answer:

- Which clients need to recognize the same person?
- Which redirect hosts belong to one sector?
- How will shared APIs resolve subjects from several clients?
- How are the pairwise salt and sector configuration backed up?
- How will existing records be migrated?

Pairwise identifiers reduce one correlation path. They do not prevent correlation through email, names, phone numbers, browser identifiers, or business data.

## Claims are copies of personal data

Moving an email address from Keycloak into a signed token does not make it cease to be personal data. Every client, proxy, backend, debug log, and monitoring tool that receives the token may process those claims.

Use client scopes and protocol mappers to expose only the claims a client needs. Keep optional claims out of default scopes. Disable broad role scope where possible and narrow the token audience.

Under the strict profile-data baseline, omit name, email address, postal address, and telephone number from access tokens unless a documented use truly requires a particular claim. Just-in-time display is still processing and needs protection, but it need not become persistent storage. Never log complete tokens merely for debugging.

## Where data belongs

### Keycloak

Good candidates:

- credentials and authentication factors;
- account state;
- the sole persistent system of record for central identity-profile attributes such as name, email address, profile postal address, and telephone number under the strict baseline;
- groups, organizations, and selected shared roles;
- federation links to LDAP, Active Directory, or another identity provider.

### Application backend

Good candidates:

- `iss` + `sub`;
- necessary domain data such as orders, cases, or course participation;
- resource-specific authorization relationships;
- references to legally retained records;
- application audit events containing subject and object references rather than profile attributes;
- a delivery, invoice, or contractual address only when it is genuinely part of the business record and its separate purpose, legal basis, access, retention, and erasure rules are documented.

The backend may retrieve a narrowly scoped profile value just in time for display. By default it must not write that value to its database, session store, distributed cache, search index, trace, analytics, error report, or audit journal. Browser and proxy storage must be included in the data-flow review.

### Not Keycloak's job

Do not turn Keycloak into the database for:

- invoices and orders;
- personnel or medical files;
- support conversations;
- learning progress;
- complete contract histories.

Central identity management is useful. Keycloak is the sole persistent system of record for the **central identity profile** in this guide's strict baseline, but it is not a database for all personal and business data. Necessary exceptions belong to their business record and must not become an uncontrolled shadow profile.

## Service-to-service access

When no human user is involved, use a dedicated confidential client and service account. The subject then represents the client/service rather than a human.

Use a separate client per service or meaningful trust boundary. Grant only its required roles, restrict audience, rotate secrets or use stronger client authentication, and log the client identity as the actor.

Do not reuse a human user's token for background jobs after the user's session has ended unless the delegation model explicitly supports and records that behavior.

## Logout, disabling, and token lifetime

A locally validated access token may remain usable until it expires. Disabling a user in Keycloak does not magically reach every offline API check.

Choose controls according to risk:

- short access-token lifetime;
- refresh-token and session revocation;
- backchannel logout;
- token introspection for high-risk requests;
- a local revocation or account-state check when immediate denial is essential.

## Production baseline

- Run Keycloak in production mode with HTTPS.
- Configure a fixed, correct hostname and trusted proxy behavior.
- Use exact redirect URIs and deliberate web origins.
- Review effective default and optional client scopes; inspect real tokens to confirm that API tokens contain no unapproved profile claims.
- Keep the management port, health, and metrics internal.
- Restrict the admin console and Admin API to an administrative network or gateway.
- Require strong multifactor authentication or passkeys for administrators.
- Separate normal and privileged accounts.
- Back up realm configuration, keys, database, and pairwise-subject secrets.
- Test restore procedures and key rotation.

## Teaching checks

A learner understands the foundations when they can explain why:

1. an ID token should not be used as a universal API token;
2. decoding a JWT is not validation;
3. email is a poor foreign key;
4. `sub` alone is not globally unique;
5. a role in a token still requires enforcement by the backend;
6. removing a Keycloak account does not delete an order database;
7. a UUID may still be personal data;
8. displaying an email claim is processing even when it is not stored;
9. a delivery address in an invoice is not the same data purpose as the current profile address;
10. a new account can be an escalation path even when direct self-assignment is blocked.
