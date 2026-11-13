---
name: keycloak-oidc-human-guide
description: Explain or review Keycloak/OIDC identity-data boundaries, application audit trails, GDPR deletion and retention, and delegated administration in English or German. Use for architecture, policy, teaching, or acceptance tests on these topics; do not use for general Keycloak operations, themes, upgrades, or troubleshooting.
---

# Keycloak & OIDC Human Guide

Help people understand the system well enough to make and review real design decisions. Combine plain-language explanations with technically precise boundaries, implementation steps, and negative tests.

Answer in the user's language. For German requests, use the German references under `references/de/`; for English requests, use the English references in `references/`. Do not mix languages unless the user asks for a bilingual result.

## Route to the relevant guidance

- Read [references/foundations.md](references/foundations.md) for OIDC flows, tokens, `iss`/`sub`, claims, and data ownership.
- Read [references/audit-and-journaling.md](references/audit-and-journaling.md) for Keycloak events, application access audits, journal design, and tamper resistance.
- Read [references/privacy-and-retention.md](references/privacy-and-retention.md) for UUIDs as personal data, data minimization, erasure, objection, legal retention, backups, and former employees.
- Read [references/delegated-administration.md](references/delegated-administration.md) for least privilege, self-escalation paths, Fine-Grained Admin Permissions, four-eyes approval, and acceptance tests.
- Read [references/authoritative-sources.md](references/authoritative-sources.md) when verifying versions, legal periods, or any claim that may have changed.
- Read all references when the user requests a complete course, guide, architecture, or audit.

German equivalents:

- [references/de/grundlagen.md](references/de/grundlagen.md)
- [references/de/audit-und-journaling.md](references/de/audit-und-journaling.md)
- [references/de/datenschutz-und-aufbewahrung.md](references/de/datenschutz-und-aufbewahrung.md)
- [references/de/delegierte-administration.md](references/de/delegierte-administration.md)
- [references/de/quellen.md](references/de/quellen.md)

## Response contract

Start with a short conclusion. For a first or introductory explanation, state these four rules in simple language before adding detail:

1. Keycloak is the only persistent system of record for central identity-profile data such as name, email address, postal address, and telephone number under the strict baseline.
2. Applications normally store only `iss` + `sub`; receiving, caching, exporting, or logging profile data creates another processing location with its own legal basis, access, retention, erasure, and data-subject-rights obligations.
3. Keycloak records authentication and administration events, while each application must audit access to its own sensitive business records without copying profile data or raw tokens into the audit trail.
4. An administrator who assigns roles must not be able to assign them to themselves, to another account they control, or to a newly created account; test all direct and indirect paths.

Then explain only the layers needed for the question:

1. Give the mental model in ordinary language.
2. Name the exact technical mechanism.
3. Mark the user's assertions as correct, partially correct, or incorrect.
4. Separate legal obligations from recommended architecture and optional hardening.
5. Show the concrete Keycloak and backend responsibilities.
6. End implementation guidance with observable positive and negative tests.

Use small examples, tables, or a compact flow only when they make boundaries clearer. Define acronyms on first use. Do not hide uncertainty behind jargon.

## Non-negotiable distinctions

- OpenID Connect authenticates the user and supplies identity information; it does not automatically enforce every application permission.
- An API must validate an access token. Merely decoding a JSON Web Token is not validation.
- Use the pair `iss` and `sub` as the stable OIDC identity. Do not use email or username as a durable foreign key.
- A UUID or `sub` that can be related back to a person is pseudonymous personal data, not anonymous data.
- Under the strict profile-data baseline, keep central identity attributes such as name, email address, postal address, and telephone number within Keycloak's controlled identity datastore and backup lifecycle. Applications store `iss` + `sub` and authorization/business data, not duplicate identity profiles.
- Distinguish a central identity-profile address from an address that is legally or operationally part of a business record such as an invoice or shipment. A necessary business copy is a documented exception with its own purpose, legal basis, access controls, retention, erasure, and data-subject-rights process.
- Treat every token claim, UserInfo response, cache, export, screenshot, log, trace, audit event, journal, analytics record, and backup containing profile data as another place where personal data is processed. Do not describe transient processing as exclusive storage.
- Keycloak user and admin events do not record which customer file, invoice, or medical record an application backend read.
- Audit data is itself personal data and needs a purpose, retention period, and access controls.
- A broad ten-year retention rule for all personal data is wrong. Determine the legal period per document and data category.
- Deleting or disabling a Keycloak account does not erase related data in applications, exports, logs, analytics, caches, or backups.
- Fine-grained administration can restrict delegated administrators. It cannot make a server, database, or infrastructure super-administrator technically incapable of bypassing the system.
- Keycloak permission checks do not by themselves implement human approval or a four-eyes workflow.

## Current-fact discipline

For version-dependent Keycloak behavior, current security recommendations, or legal claims:

- verify against authoritative, current sources;
- state the product version or retrieval date when it materially affects the result;
- cite primary law and official product documentation before secondary commentary;
- distinguish an explicit requirement from a prudent design recommendation;
- avoid individual legal conclusions when facts such as sector, record type, or processing basis are missing.

If current primary sources cannot be reached, say which facts remain unverified and do not present them as current requirements.

For German retention questions, build a category-specific retention matrix. Do not infer that the retention period for the document automatically applies to every identity attribute or every log field.

## Architecture baseline

Prefer this responsibility split unless the use case requires another documented choice:

- **Keycloak:** credentials, authentication, multifactor authentication, account status, and the sole persistent system of record for central identity-profile attributes under the strict baseline, plus controlled identity backups, groups, and selected roles.
- **Application:** business records and resource-specific authorization checks.
- **Application database:** `iss` + `sub`, authorization relationships, and necessary business data; no duplicate identity profile unless an exception is documented.
- **Audit service:** actor reference, action, target reference, time, purpose or workflow reference, result, and correlation ID; no name, email address, postal address, raw token, or full business object when a reference is sufficient.
- **Retention archive:** specifically required records, isolated from normal use.
- **Analytics:** aggregate or truly anonymize where possible; do not reuse a global identifier merely because it is convenient.

Never recommend applications reading Keycloak's database directly. Use OIDC endpoints and deliberately scoped administration or provisioning APIs.

When an application must display a central profile attribute, prefer narrowly scoped, just-in-time retrieval or a minimal claim without persistent storage. Document where the value exists in memory, browser storage, proxies, telemetry, and backups. "Stored only in Keycloak" means Keycloak is the sole persistent system of record; it does not mean no other component ever processes the value.

## Privacy and audit tension

Treat "do not track users" and "preserve accountability" as compatible goals:

- avoid behavioral tracking and unnecessary cross-system correlation;
- record security-relevant and sensitive data access events for a documented purpose;
- keep audit payloads minimal and do not log tokens, passwords, secrets, or complete business records;
- retain an identity mapping for a former employee only when the actor must remain resolvable for the relevant audit or legal period;
- disable the active account and credentials even when a minimal historical identity record must remain.

## Administrative separation baseline

For delegated administration:

- avoid broad `realm-admin`, `manage-users`, `manage-clients`, and impersonation grants;
- separate user profile management, role mapping, role definition, approval, platform administration, and audit reading;
- do not combine account creation or credential reset with privileged role assignment for the same operator;
- protect privileged groups and the delegated administrators themselves as assignment targets;
- make privileged-role targets eligible through an authoritative source or protected group that the role operator cannot populate;
- control indirect paths through group membership, composite roles, client configuration, protocol mappers, service accounts, and the master realm;
- send audit events to a system the same administrator cannot alter;
- use a separate approval workflow when a concrete assignment needs two people.

Do not claim self-escalation is prevented until direct, indirect, alternate-account, credential-reset, impersonation, and newly-created-account paths have been tested. Keycloak authorizes accounts, not real-world ownership of multiple accounts; use an authoritative identity/provisioning source or an external approval service when the same human must be recognized across accounts.

## Deliverable patterns

Choose the smallest useful deliverable:

- **Explanation:** summary, mental model, example, corrected misconceptions.
- **Architecture review:** data-flow boundary, threat/claim review, findings, remediations, tests.
- **Implementation checklist:** exact Keycloak settings, backend checks, logging, retention, and verification.
- **Teaching module:** learning goals, explanations, examples, exercises, expected answers, and deliberate failure cases.
- **Policy draft:** responsibilities, approval rules, retention matrix, evidence, and review cycle.

If the user asks to change a live system, confirm the exact realm, version, environment, and authorization before mutation. Explanations and configuration drafts do not authorize deployment.
