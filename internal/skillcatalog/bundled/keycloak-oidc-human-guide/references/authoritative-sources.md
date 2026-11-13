# Authoritative sources

**Last reviewed: 2026-08-28**

Use this file as a source map, not as a replacement for checking the current text. Product behavior and law can change.

## Keycloak

### Server Administration Guide

https://www.keycloak.org/docs/latest/server_admin/

Use for:

- user and admin events;
- realm-management roles;
- Fine-Grained Admin Permissions;
- client scopes, role mappings, groups, and organizations;
- authentication flows, multifactor authentication, and WebAuthn.

Important sections to search:

- `Configuring auditing to track events`
- `Auditing user events`
- `Auditing admin events`
- `Managing access to realm resources`
- `Delegating realm administration using permissions`
- `Fine grained admin permissions`
- `Role scope mappings`

Version note: the latest documentation reviewed for this project was in the Keycloak 26.7 line. Verify behavior for the exact deployed version.

### Current release and security fixes

https://www.keycloak.org/2026/08/keycloak-2672-released

As of the review date, Keycloak 26.7.2 was current. The release includes fixes for security issues affecting Fine-Grained Admin Permissions, including CVE-2026-14613 (role-groups endpoint authorization bypass) and CVE-2026-15945 (group-hierarchy information disclosure). Use it to explain why a fully patched release and regression tests are prerequisites for delegated-administration claims. Always check whether a later release or advisory exists.

### Securing applications with OIDC

https://www.keycloak.org/securing-apps/oidc-layers

Use for:

- discovery endpoint;
- authorization, token, UserInfo, certificate, introspection, and logout endpoints;
- authorization code flow;
- access-token validation guidance;
- redirect URI security.

### Protocol mappers

https://www.keycloak.org/admin-api/protocol-mappers

Use for:

- available OIDC protocol mappers;
- SHA-256 pairwise subject mapper;
- claim and audience mapping.

### Production and proxy configuration

- https://www.keycloak.org/server/configuration
- https://www.keycloak.org/server/hostname
- https://www.keycloak.org/server/reverseproxy
- https://www.keycloak.org/server/logging

Use for production mode, HTTPS, fixed hostname, trusted proxy behavior, separated administrative hostname, management endpoints, structured logs, Syslog, and observability.

## OpenID Connect and OAuth

### OpenID Connect Core 1.0

https://openid.net/specs/openid-connect-core-1_0-final.html

Use for:

- `iss`, `sub`, and `aud` semantics;
- stability and uniqueness of `iss` + `sub`;
- public and pairwise subject identifiers;
- ID token validation.

### OAuth 2.0 Security Best Current Practice, RFC 9700

https://www.rfc-editor.org/rfc/rfc9700.html

Use for:

- PKCE requirements and recommendations;
- avoidance of the Implicit Flow;
- prohibition of Resource Owner Password Credentials;
- access-token audience and privilege restriction;
- modern threat mitigations.

## European data protection law

### General Data Protection Regulation

https://eur-lex.europa.eu/eli/reg/2016/679/oj/eng

Relevant articles:

- Article 4: personal data and pseudonymization;
- Article 5: purpose limitation, data minimization, storage limitation, accountability;
- Article 6: legal bases;
- Article 7: withdrawal of consent;
- Article 12: transparent handling and response deadlines;
- Article 15: right of access;
- Article 17: erasure and exceptions;
- Article 18: restriction of processing;
- Article 21: objection;
- Article 25: data protection by design and by default;
- Article 30: records of processing activities;
- Article 32: security of processing;
- Articles 33 and 34: breach notification where their conditions apply;
- Article 58: supervisory authority corrective powers;
- Article 82: compensation and liability;
- Article 83: administrative fines.

### CJEU case C-579/21

https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:62021CJ0579

Use for:

- right of access to information about times and purposes of consultations;
- distinction between recipients and employees acting under the controller's authority;
- conditional treatment of employee identity disclosure.

Do not turn the judgment into an unsupported universal rule that every read in every application must be logged forever.

## German commercial and tax retention

### Fiscal Code section 147

https://www.gesetze-im-internet.de/ao_1977/__147.html

Use for document categories, current retention periods, start rules, availability, readability, and tax-authority data access.

### Commercial Code section 257

https://www.gesetze-im-internet.de/hgb/__257.html

Use for commercial document categories and periods, including special treatment of accounting vouchers for certain regulated institutions.

### Commercial Code sections 238, 239, and 245

- https://www.gesetze-im-internet.de/hgb/__238.html
- https://www.gesetze-im-internet.de/hgb/__239.html
- https://www.gesetze-im-internet.de/hgb/__245.html

Use for traceability of business transactions, completeness/order/immutability, and required signing of annual financial statements.

### GoBD

Official BMF material:

- https://amtliche-handbuecher.bundesfinanzministerium.de/ao/2024/Anhaenge/BMF-Schreiben-und-gleichlautende-Laendererlasse/Anhang-33/inhalt.html
- https://www.bundesfinanzministerium.de/Content/DE/Downloads/BMF_Schreiben/Weitere_Steuerthemen/Abgabenordnung/2025-07-14-GoBD-2-aenderung.html

Use for:

- progressive and retrograde traceability;
- document function and journal function;
- internal controls and separation of duties;
- change logging and immutability;
- procedure documentation;
- electronic retention and data access.

Check the latest amendments before citing paragraph numbers or technical examples.

## German information-security guidance

### BSI identity and authorization management

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/Umsetzungshinweise/Umsetzungshinweise_2022/Umsetzungshinweis_zum_Baustein_ORP_4_Identitaets_und_Berechtigungsmanagement.pdf?__blob=publicationFile&v=2

Use for least privilege, need to know, responsibility, approval, role separation, and regular authorization review.

### BSI logging

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2022/04_OPS_Betrieb/OPS_1_1_5_Protokollierung_Edition_2022.html

Use for security-relevant event collection, protected storage, evaluation, and disposal.

### BSI software development

https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2023/03_CON_Konzepte_und_Vorgehensweisen/CON_8_Software_Entwicklung_Edition_2023.pdf?__blob=publicationFile&v=3

Use for least privilege, trustworthy authentication, encryption, security logging, documentation, and testing.

## Verification checklist

Before presenting a claim as current:

1. Open the primary source.
2. Confirm its publication, amendment, or software version.
3. Read the surrounding section, not only a search snippet.
4. Separate exact legal wording from implementation advice.
5. Record the access date when the result becomes a durable guide or policy.
6. Explain unresolved sector- or fact-specific questions instead of guessing.
