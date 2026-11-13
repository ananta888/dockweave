# Auditability and journaling

## One sentence each

- A **runtime log** helps operate and debug a system.
- A **security log** helps detect attacks and policy violations.
- An **audit log** establishes who performed a relevant action and what happened.
- An **access history** records that protected data was read, exported, or otherwise accessed.
- A **business journal** preserves the ordered history of business events and corrections.

They may use the same technical pipeline, but they have different purposes, readers, retention periods, and payloads.

## Why ordinary logs are insufficient

A normal log line such as this is useful for debugging but weak evidence:

```text
Loaded record successfully
```

It does not establish:

- who acted;
- which record was involved;
- whether access was allowed or denied;
- why the access occurred;
- which workflow or request initiated it;
- whether the entry was later changed or deleted.

Audit requirements must therefore be designed, not discovered after an incident.

## Keycloak's audit boundary

Keycloak provides two important event families.

### User events

Examples include login, login error, logout, registration, code-to-token exchange, refresh, password updates, and account changes.

Keycloak does not store all user events in the Admin Console by default. Enable **Realm settings → Events → User events settings → Save events** and set an intentional expiry. Select the event types actually required.

### Admin events

Administrative changes made through the Admin Console use the Admin REST API and can be recorded as Admin Events. Enable **Realm settings → Events → Admin events settings → Save events**.

`Include Representation` adds the submitted JSON representation. It may help reconstruct changes, but can significantly increase volume and personal or confidential content. Leave it disabled for profile operations under the strict profile-data baseline; any narrower exception needs a field, storage, access, retention, erasure, and restore review. It is not a guaranteed before-and-after diff.

### What Keycloak cannot see

Keycloak normally cannot know that an application:

- opened customer file 74291;
- exported 500 employee records;
- viewed a medical result;
- changed the status of an invoice;
- calculated a score using a person's data.

The application or a shared audit gateway must emit those events.

## Recommended application audit event

```json
{
  "schema_version": 1,
  "event_id": "01J6A...",
  "occurred_at": "2026-08-27T10:15:22.431Z",
  "actor_iss": "https://login.example.org/realms/company",
  "actor_sub": "8f53b5c1-...",
  "actor_type": "human",
  "client_id": "case-management-ui",
  "service": "case-management-api",
  "action": "READ_CASE",
  "target_type": "case",
  "target_id": "case-74291",
  "purpose_ref": "support-ticket-1834",
  "decision": "ALLOW",
  "correlation_id": "req-91b..."
}
```

Add fields only when they change investigation or compliance value. Possible additions include tenant, effective role, approval ID, legal basis code, export row count, or reason for emergency access.

Avoid:

- passwords and client secrets;
- access, ID, or refresh tokens;
- complete request and response bodies;
- full names, email addresses, postal addresses, or telephone numbers when a subject reference is sufficient;
- unstructured reason fields where a controlled code is possible;
- sensitive data copied from the target record.

## Profile data in logs and journals

Under the strict baseline, audit records use `actor_iss` + `actor_sub`, a target reference, and controlled purpose codes. They do not duplicate the central Keycloak profile. This rule covers application logs, Keycloak event listeners, reverse proxies, API gateways, distributed tracing, security information and event management (SIEM), support bundles, analytics, exports, screenshots, and backups.

If a name, email address, postal address, telephone number, raw token, or full Keycloak Admin REST representation enters any of those systems, it creates another personal-data processing location. Inventory it and establish its purpose, legal basis, recipients, access controls, protection, retention, search/export response, erasure or restriction behavior, backup behavior, and accountable owner. A debug or audit label is not a legal basis and an immutable journal does not justify indefinite retention.

Keycloak's `Include Representation` option deserves special caution because submitted user representations can contain complete profile attributes. Do not enable it for profile operations under the strict baseline merely to obtain a convenient change history. Prefer a separately designed listener or pipeline that creates a whitelisted minimal change event without first persisting the full representation. If full-representation logging is demonstrably indispensable, treat it as a documented exception and test access, size limits, expiry, erasure, and restore behavior.

For a legacy copy discovered in logs:

1. stop producing new copies;
2. preserve only what a documented incident, legal obligation, or legal hold requires;
3. restrict access and locate replicas in indexes, archives, exports, and backups;
4. delete or irreversibly anonymize the rest through the approved lifecycle;
5. record the remediation without reproducing the profile value in the deletion evidence.

Failure to control or erase these copies can lead to GDPR violations and supervisory, compensation, or breach-response consequences. The exact consequence depends on facts; see [Privacy, erasure, and retention](privacy-and-retention.md#legal-consequences-of-additional-copies).

## Actor, target, and subject are different

An event may involve several people:

- **actor:** employee or service performing the action;
- **target record owner:** person whose data was accessed;
- **approver:** person who authorized exceptional access;
- **requester:** person who initiated a workflow.

Do not overload one `user_id` field for all four. Model the roles explicitly when needed.

## Reads versus changes

### Sensitive reads

Log a read when knowing that access occurred is important, for example medical files, personnel records, bank records, large exports, or exceptional administrative access.

Do not automatically record every page view forever. Define which data classes and operations require access audit and why.

### Changes

A useful change event records:

- actor and time;
- target and operation;
- previous and new state or a safe diff where necessary;
- workflow, approval, or reason reference;
- success or failure;
- correlation to application and infrastructure traces.

Avoid placing complete sensitive objects into the audit event merely to create a history. Store an approved minimal diff, cryptographic digest, or reference to a separately controlled journal record.

## Business journal behavior

A journal is append-only from the business perspective:

```text
10:00 Invoice 17 posted
10:15 Invoice 17 reversed, reason: duplicate
10:16 Corrected Invoice 18 posted
```

Do not overwrite the first event so it appears never to have existed. Corrections are new events referring to the original.

For accounting-related systems, preserve the link from source document through initial entry, journal, account, and resulting statement for the required period. Preserve historic system and procedure information needed to interpret the records.

## Tamper resistance

Keycloak's local event store is useful but is not a complete immutable archive. An administrator with event-management capability may clear events, and a database administrator has deeper access.

For significant evidence:

- transmit events to a separately administered system;
- use append-only or immutable retention controls;
- separate write, read, administration, and deletion roles;
- use authenticated transport;
- use reliable UTC timestamps and time synchronization;
- monitor sequence gaps, ingestion failures, and clock drift;
- consider hash chains, signing, or WORM storage for high assurance;
- audit access to the audit store itself;
- prevent the audited administrator from controlling every copy.

Immutability does not mean infinite retention. Expired data must still be deleted according to policy, unless a documented legal hold applies.

## Correlation without leaking tokens

Generate a request correlation ID at the edge and propagate it through trusted headers or tracing context. Store the identifier in Keycloak-adjacent, application, audit, and infrastructure events.

Do not use the raw access token as a correlation value. If a token-specific fingerprint is genuinely needed, derive a one-way, scoped value and document the risk.

## Purpose and reason

For ordinary operations, purpose is often inherent in the workflow. An agent opening a case assigned to them may not need to type a reason each time.

Require an explicit purpose or ticket for:

- break-glass access;
- access outside normal assignment;
- bulk export;
- support impersonation;
- privileged search across people;
- disclosure to an external recipient.

Prefer controlled purpose codes plus a workflow reference over unrestricted free text.

## Data subject access

The Court of Justice of the European Union ruled in case C-579/21 that information about the time and purposes of consultations of a person's data can fall within the right of access. Employee identities are not normally disclosed merely because they performed the consultation under the controller's authority; disclosure may be required exceptionally when necessary for the person's rights and balanced against employee rights.

Architectural consequence:

- internally retain enough information to identify the actor when justified;
- be able to report time and purpose;
- do not promise that every employee name will always be disclosed;
- design the query and redaction process before the first request.

The judgment is not a universal statement that every controller must log every read in every low-risk system. Use law, risk, sector rules, and data sensitivity to define the audit scope.

## Retention of logs

Create separate periods for:

- login security events;
- administrative changes;
- sensitive record access;
- business journal entries;
- infrastructure traces;
- approval records.

The business document's retention period does not automatically make every debug log field necessary for the same duration.

## Operational failure modes

Test behavior when:

- the audit sink is unavailable;
- a message is duplicated;
- event order changes;
- the actor mapping no longer exists in active Keycloak;
- system time is wrong;
- a user has several concurrent sessions;
- an administrator attempts to clear events;
- a backup is restored after deletion;
- a log event is larger than the receiver limit.

Decide whether a sensitive operation fails closed when audit delivery fails or continues with a durable local queue and urgent alert. The decision must be risk-based and tested.

## Acceptance tests

1. Successful and failed logins appear with correct realm and client context.
2. An administrative role assignment produces a Keycloak Admin Event.
3. Reading a protected application record produces an application audit event.
4. A denied read produces a denied event without exposing record contents.
5. Complete tokens and secrets never appear in the audit payload.
6. Correlation IDs connect the relevant events.
7. The delegated administrator cannot delete the external event.
8. A former employee's historic actor reference remains resolvable only for the approved period.
9. Expired events are deleted and a legal hold prevents only the justified deletion.
10. Audit-sink failure triggers the designed queue, denial, and alert behavior.
11. Names, email addresses, postal addresses, telephone numbers, and raw tokens are absent from logs, traces, journals, event representations, and support bundles by default.
12. An approved exceptional field can be found, restricted, exported, expired, and erased across primary storage, indexes, archives, and restore tests.
