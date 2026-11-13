# Privacy, erasure, and retention

## The central correction

Using only a UUID in an application database can be good data minimization. It does not make the record anonymous.

If Keycloak, an HR table, an audit archive, or another available source can relate the UUID to a natural person, the identifier is pseudonymous personal data. GDPR still applies.

The strict architecture baseline in this guide is equally important: Keycloak is the sole persistent system of record for the central identity profile, including name, email address, profile postal address, and telephone number. Applications normally persist only `iss` + `sub`. Moving a profile field into a token, cache, log, journal, export, index, analytics system, or backup creates another processing location. Outside Keycloak's controlled identity datastore and backup lifecycle, such a copy defeats the “only in Keycloak” baseline unless it is a documented business-record exception.

## Anonymous versus pseudonymous

### Pseudonymous

```text
application row: subject_id = 8f53...
identity system: 8f53... = Max Example
```

The application team may not see the name, but the organization can still perform the relation. This reduces risk and access, yet remains personal data.

### Anonymous

The relation to a person has been irreversibly removed to the degree that identification is no longer reasonably possible using likely means. Removing the name from one table while retaining the mapping elsewhere is not anonymization.

## Data-minimized architecture

Use `iss` + `sub` in backends and keep mutable central identity fields persistently only in Keycloak under the strict baseline. An application may process a narrowly scoped value just in time, but does not persist a shadow profile.

Possible patterns:

- display a token or UserInfo claim without storing it permanently;
- avoid a display-name cache; if performance makes a cache demonstrably necessary, treat it as an approved exception with a short period and working deletion;
- keep a necessary delivery, invoice, or contractual address in the relevant business record even when the login profile later changes, but do not reuse it as the current identity profile;
- use a pairwise subject for unrelated clients;
- use aggregated or anonymized analytics instead of a global person identifier.

The rule is not “all personal data in Keycloak.” Keycloak must not contain invoices, orders, medical files, or other business records. The rule is: “central identity-profile attributes persist only in Keycloak; every business-data copy must be independently necessary and governed for its own purpose.”

Use this decision gate before any other persistent copy:

| Question | If the answer is no |
|---|---|
| Is the field part of this system's own business record rather than a convenience copy of the login profile? | Do not store it |
| Is there a documented purpose and legal basis? | Do not store it |
| Are access, recipients, retention, restriction, erasure, backup, and restore behavior implemented? | Do not store it |
| Can the copy be found for access and deletion requests? | Do not store it |
| Has the exception owner accepted and periodically reviewed it? | Do not store it |

## Tokens spread claims

A claim copied into an access token may reach:

- the client;
- API gateway;
- backend services;
- tracing and error tools;
- browser or device storage;
- support screenshots.

Minimize claims, avoid complete-token logging, and avoid placing sensitive business attributes in tokens just because a mapper can do it.

An access token is commonly copied through multiple infrastructure layers and is often retained accidentally in telemetry. Under the strict baseline, omit name, email address, postal address, and telephone number from access tokens unless one particular claim is demonstrably required. An ID token or UserInfo response intended for the client is not permission to persist its profile fields.

## Legal consequences of additional copies

An additional copy is not automatically unlawful, but it is a separate act or location of processing. The controller must be able to demonstrate, among other things:

- a defined purpose and legal basis under GDPR Articles 5 and 6;
- data minimization, storage limitation, and accountability under Article 5;
- privacy by design and by default under Article 25;
- appropriate access controls, confidentiality, integrity, availability, and protection against unauthorized alteration or erasure under Article 32;
- a complete response process for access, erasure, and restriction under Articles 12, 15, 17, and 18;
- inclusion in the processing inventory and retention/deletion controls where required.

If a profile value remains in a log, journal, search index, export, analytics platform, or restored backup after it should have been erased, changing or deleting the Keycloak profile alone is not a complete erasure. “Immutable,” “audit,” “debug,” and “backup” are technical labels, not blanket exceptions to purpose and storage limitation. A legal obligation or legal hold can justify retaining only the necessary categories for the necessary period and use.

Depending on the facts, failures can result in corrective powers such as an order to restrict, erase, or stop processing and administrative fines under GDPR Articles 58 and 83. A person may claim compensation for material or non-material damage under Article 82. If uncontrolled copies are involved in a personal-data breach, notification to the authority or affected people may be required under Articles 33 and 34. These consequences are fact-specific; this guide does not decide an individual legal case. The primary provisions are listed in the [authoritative source map](authoritative-sources.md#european-data-protection-law).

## Four different data-subject actions

| Action | Basic effect |
|---|---|
| Withdraw consent | Stops future processing based on that consent; prior processing is not automatically unlawful |
| Object under GDPR Article 21 | Applies particularly to public-interest or legitimate-interest processing; direct marketing must stop for that purpose |
| Erasure under Article 17 | Requires deletion when the relevant grounds apply and no exception remains |
| Restriction under Article 18 | Retains marked data but sharply limits further processing |

Do not treat withdrawal, objection, restriction, and erasure as synonyms.

## Erasure is not absolute

GDPR Article 17 includes exceptions when processing remains necessary, including:

- compliance with a legal obligation;
- performance of certain public-interest tasks;
- establishment, exercise, or defense of legal claims;
- specified public-health, research, or archival purposes under their conditions.

The exception applies only to the necessary data and purpose. It does not authorize keeping an entire active profile and every convenience or marketing field.

## No universal ten-year rule

As of 2026, German tax and commercial rules include examples such as:

- ten years for books, inventories, opening balances, annual accounts, management reports, and organization documents needed to understand them;
- eight years for ordinary accounting vouchers;
- six years for many commercial or business letters and other tax-relevant documents;
- ten years for accounting vouchers of certain regulated financial and insurance institutions.

The period often starts at the end of the calendar year in which the relevant document, entry, or event occurred. Other sector rules, limitation periods, audits, or legal holds may change the result.

Never copy this list into a specific policy without checking the current law, organization type, document function, and any transition rules.

## Retention matrix

Build a matrix instead of one global setting:

| Category | Purpose and basis | Start event | Period | Store | Allowed access | Final action |
|---|---|---|---:|---|---|---|
| Active Keycloak account | Identity and contract | contract end | policy-specific | Keycloak | IAM | disable/delete |
| Annual account | Commercial/tax law | year end | verify, often 10 years | archive | finance/audit | delete after period |
| Accounting voucher | Commercial/tax law | year end | verify, often 8 years | archive | finance/audit | delete after period |
| Business letter | Commercial/tax law | year end | verify, often 6 years | archive | limited business role | delete after period |
| Login security event | Security | event time | risk-based | SIEM | security | rolling deletion |
| Sensitive-record access | Accountability | event time | risk/sector-based | audit store | audit role | rolling deletion |
| Role approval | Access governance | decision time | policy/risk-based | workflow archive | IAM/audit | delete after period |

Keep a reference to the exact legal or policy rule used for each row.

## Former employees and historic attribution

### The actual requirement

When a former employee performed an action that must remain traceable, the organization may need to resolve the historic actor during the relevant audit or retention period.

That does not require an active Keycloak account for ten years.

On departure:

1. disable login immediately;
2. revoke sessions, refresh tokens, credentials, service access, and administrative roles;
3. remove the person from active groups and assignment queues;
4. preserve only the historic identity information needed to interpret retained events;
5. isolate that mapping from normal identity and analytics use;
6. log access to the mapping;
7. delete it when no retained event or legal purpose still needs it.

### Minimal historic mapping

```text
actor_iss
actor_sub
historic_employee_number
historic_display_name, only if demonstrably needed
employment interval
mapping retention reason
delete_after
```

Not every field is always required. Department, private address, current contact data, MFA credentials, and password hashes are usually irrelevant to interpreting an old bookkeeping action.

### Where the mapping lives

Under the strict profile-data baseline, prefer:

- a disabled minimal identity tombstone in Keycloak; or
- an access-controlled compliance mapping containing only `iss` + `sub`, a non-profile employee reference, retention reason, and deletion date, while any required historic name remains in Keycloak.

If a lawfully retained business record itself must contain a historic name, that record is a documented business exception rather than a reusable identity profile. Every choice needs a legal basis, restricted access, retention rule, and deletion process.

### UUID without mapping is insufficient

An audit event containing only `actor_sub = 8f53...` is weak evidence when all identity mappings have been deleted. If the actor must remain attributable, preserve the minimum mapping for the justified period.

### Not every preparer is legally relevant

Distinguish:

- person who typed or imported data;
- person who reviewed it;
- person who approved it;
- legally responsible signatory;
- legal entity responsible for orderly books.

For example, a staff member may prepare figures, while the responsible merchant or legal representatives sign the annual financial statement. German Commercial Code section 245 requires the relevant signatory and date. Do not automatically retain every participant's full identity for the document's complete period unless their attribution is required for the audit trail or internal control system.

The correct question is:

> Which person's role must remain understandable to reconstruct, verify, approve, or legally attribute this record?

## Practical erasure workflow

1. Verify the requester proportionately.
2. Resolve the person's identities, including all applicable `iss` + `sub` pairs.
3. Discover systems from the record of processing activities and data inventory.
4. Search not only databases but also token telemetry, Keycloak event representations, runtime and security logs, journals, traces, browser/server caches, search indexes, support systems, exports, analytics, archives, and backups.
5. Identify each data category, purpose, legal basis, period, and legal hold.
6. Disable login and revoke active sessions when the relationship ends.
7. Delete or truly anonymize data with no remaining purpose or basis.
8. Restrict and isolate records that must be retained.
9. Remove or update search indexes, caches, exports, data-lake copies, and analytics.
10. Allow backups to expire through documented rotation and prevent deleted data from silently returning after restore.
11. Preserve a deletion ledger that contains only what is necessary to reapply deletions.
12. Record completion and justified exceptions.
13. Inform the person about the result and relevant retained categories.

Deleting the Keycloak user is one task in this workflow, not the workflow itself.

## Backups

Immediate item-level deletion from every immutable backup may be technically impractical. A defensible design normally includes:

- fixed, limited backup retention;
- strict access and use only for recovery;
- no ordinary querying of old backups;
- a deletion ledger or replay process after restore;
- destruction of expired backup media;
- documented recovery tests demonstrating that deleted accounts do not silently become active again.

Do not use "it is in a backup" as justification for indefinite retention.

## Legal holds

A legal hold suspends deletion only for the relevant records and dispute or proceeding. Record:

- authority and reason;
- covered data categories;
- start date;
- owner and review date;
- release event;
- deletion action after release.

A legal hold should not become an undocumented permanent flag.

## Right to know about access

When access logs exist, a data-subject request may require information about dates and purposes of consultations. Employee names are not automatically disclosed in every case. The internal system should still be able to identify the actor when needed for investigation and accountability.

This creates two views:

- **internal audit view:** resolvable actor under strict access;
- **data-subject response:** time, purpose, recipients, and other required information, with employee identity assessed under the applicable legal test.

## Failure modes

- Deleting Keycloak first destroys the only mapping needed to find application records.
- Retaining every account forever because some records have a ten-year period.
- Removing a name but keeping an easily available identity lookup and calling the data anonymous.
- Deleting the production database while leaving searchable warehouse and export copies.
- Restoring a backup that reactivates a deleted user.
- Keeping full HTTP logs for the period of an annual account without a separate necessity analysis.
- Using legally retained billing data later for marketing.
- Retaining the preparer's full personnel file when only an employee number and historic name are needed.
- Claiming profile data exists only in Keycloak while names or email addresses remain in tokens, event representations, logs, traces, support exports, or restored backups.
- Making an audit journal immutable without implementing category-specific expiry, erasure, restriction, and legal-hold release.

## Acceptance tests

1. A person can be found across systems using controlled identity mappings.
2. Deletion removes unnecessary profile and application data.
3. Required financial records remain isolated and readable.
4. Retained records cannot be used by the marketing application.
5. A former employee cannot authenticate after departure.
6. Historic required actions remain attributable to the former employee.
7. Access to the historic identity mapping is itself audited.
8. The mapping is deleted after the last justified record expires.
9. A backup restore reapplies deletion and revocation state.
10. Pairwise subjects do not prevent the erasure workflow from locating all related records.
11. The normal application estate contains no persistent name, email address, profile postal address, or telephone number outside Keycloak.
12. Every approved business exception has purpose, legal basis, owner, access, retention, erasure, restriction, backup, restore, and data-subject-request evidence.
13. Log and journal searches find no raw token or unapproved profile attribute, and seeded exception data disappears from active stores and indexes at expiry.
