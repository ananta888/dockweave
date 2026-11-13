# Keycloak & OIDC Human Guide

[Deutsche Fassung](README.de.md) · English

An installable skill and a human-readable knowledge base for designing and explaining:

- Keycloak and OpenID Connect,
- authentication versus application authorization,
- privacy-preserving subject identifiers,
- auditability and journaling,
- GDPR erasure and legal retention,
- delegated administration and separation of duties.

The project is intentionally written for two audiences at the same time:

1. people who need an understandable explanation before making a decision;
2. assistants and engineers who need precise constraints, implementation steps, and tests.

## Safe starting point

1. Keycloak is the only persistent system of record for the central identity profile: name, email address, profile postal address, and telephone number.
2. Applications normally store `iss` + `sub`. Any profile value in a token, cache, log, journal, export, index, screenshot, or backup is another personal-data processing location with its own legal basis, security, retention, access, restriction, and erasure obligations.
3. Keycloak audits authentication and administration; each application audits access to its own sensitive business records using identifiers and references, not duplicated profiles or raw tokens.
4. A role operator must be unable to promote their own, an alternate, or a newly created account. Account creation, credential reset, target eligibility, approval, execution, and audit control must be separated and tested.

A required delivery or invoice address may belong in its business record. That is a documented exception with a separate purpose and lifecycle, not a reusable copy of the login profile. The legal consequences of uncontrolled copies are explained explicitly in the privacy and audit references.

## Why this exists

Identity projects often fail at the boundaries:

- an application trusts a decoded token without validating it;
- email addresses become permanent database keys;
- a UUID is incorrectly treated as anonymous;
- Keycloak is expected to audit application database reads;
- every personal record is assigned the same ten-year retention period;
- a helpdesk administrator can indirectly grant themselves privileges through groups or composite roles.

This guide makes those boundaries explicit and turns them into testable designs.

## Example questions

- Explain Keycloak and OIDC to a non-specialist without losing technical accuracy.
- Review whether an API validates Keycloak access tokens correctly.
- Design a data-minimized identity architecture using `iss` and `sub`.
- Explain the difference between audit logs, security logs, and a business journal.
- Design a GDPR deletion workflow that preserves legally required records.
- Keep a former employee attributable in old audit records without retaining an active login.
- Configure delegated Keycloak administrators so they cannot assign themselves privileged roles.
- Produce acceptance tests for Fine-Grained Admin Permissions.

## Repository structure

```text
SKILL.md
agents/openai.yaml
.github/workflows/quality.yml
references/
  foundations.md
  audit-and-journaling.md
  privacy-and-retention.md
  delegated-administration.md
  authoritative-sources.md
  de/
    grundlagen.md
    audit-und-journaling.md
    datenschutz-und-aufbewahrung.md
    delegierte-administration.md
    quellen.md
scripts/
  validate_skill.py
  check_external_links.py
tests/
  prompt-evals.json
LICENSE
```

`SKILL.md` contains the compact decision and routing rules. The reference files are standalone explanations for humans and are loaded only when relevant. Complete German equivalents are available under `references/de/`.

## Installation

For a per-user installation, clone the repository into the current skills directory:

```bash
mkdir -p "$HOME/.agents/skills"
git clone https://github.com/ananta888/keycloak-oidc-human-guide.git \
  "$HOME/.agents/skills/keycloak-oidc-human-guide"
```

For repository-scoped discovery, place it at `.agents/skills/keycloak-oidc-human-guide` inside that repository. Then invoke it explicitly with `$keycloak-oidc-human-guide` or let automatic discovery select it for matching questions.

The current [OpenAI skill documentation](https://learn.chatgpt.com/docs/build-skills) defines `$HOME/.agents/skills` and `.agents/skills` as the local locations. Use legacy locations only when the documentation for a particular installation explicitly requires them. For wider managed distribution, package the skill in a plugin as described by the current documentation.

## Design position

The guide favors:

- Authorization Code Flow with PKCE,
- least privilege and narrow token audiences,
- `iss` + `sub` instead of mutable profile fields,
- Keycloak as the only persistent system of record for central identity-profile fields,
- minimal claims and documented business-record exceptions instead of shadow profiles,
- application-level access auditing for sensitive records,
- separate, tamper-resistant audit storage,
- per-category retention rules,
- fine-grained delegated administration plus external approval for four-eyes workflows.

It does not present Keycloak as a business-data store, an immutable compliance archive, or a complete replacement for application authorization.

## Quality checks

Run the deterministic checks locally:

```bash
python3 scripts/validate_skill.py .
```

They validate skill metadata, local links, bilingual structure, source-review dates, and prompt-evaluation fixtures. External links are checked separately because networks can be transient:

```bash
python3 scripts/check_external_links.py .
```

## Legal scope

The legal sections explain architecture consequences of GDPR and German retention requirements. They are not a substitute for legal advice about a particular organization, sector, or record type. Current legal claims should be checked against the primary sources linked in `references/authoritative-sources.md`.

## License

BSD 3-Clause License. See [LICENSE](LICENSE).
