# Delegated administration and separation of duties

## Goal

A person who maintains accounts or executes approved role assignments should not automatically be able to:

- obtain the assigned business privilege themselves;
- grant administrative privileges;
- modify other administrators;
- enter a privileged group indirectly;
- alter the role definition to make a harmless role powerful;
- impersonate a privileged user;
- delete the evidence of their action.

This is a combination of least privilege, need to know, separation of duties, and independent audit.

Use a fully patched Keycloak release before relying on these controls. As reviewed on 2026-08-28, Keycloak 26.7.2 was the current release and included fixes for Fine-Grained Admin Permission bypass and information-disclosure vulnerabilities. Recheck the [authoritative source map](authoritative-sources.md#keycloak) and the exact deployed version; configuration cannot compensate for a known authorization bypass.

## Roles to separate

| Function | Typical capability | Should not also control |
|---|---|---|
| User administrator | profile and credential maintenance | role approval or privileged role mapping |
| Account provisioner | create and disable accounts from an authoritative source | privileged-target eligibility or role mapping |
| Access operator | execute approved assignments | approve own request or define roles |
| Eligibility owner | determine who may receive a privileged role | execute the mapping or administer operators |
| Access approver | decide business need | technical assignment |
| Role/security administrator | define roles and policies | ordinary account maintenance |
| Application owner | define application need | Keycloak platform administration |
| Audit reader | inspect evidence | change identities or logs |
| Platform administrator | run and update Keycloak | normal daily work with superuser access |
| Break-glass custodian | emergency access | routine administration |

One employee may hold more than one organizational function in a small company, but critical combinations must be identified, justified, monitored, and compensated. Separate accounts and step-up authentication still help.

## Coarse Keycloak administration roles

Each realm contains a `realm-management` client with roles such as:

- `realm-admin`;
- `manage-users` and `view-users`;
- `manage-clients` and `view-clients`;
- `manage-realm`;
- `manage-events` and `view-events`;
- `manage-identity-providers`;
- `manage-authorization`;
- `impersonation`.

These are convenient but broad. Current Keycloak documentation describes `manage-users` as including broad user management, role mapping, and group membership capabilities. Assigning it to a narrowly intended helpdesk operator is therefore usually too much.

Keycloak has a safeguard that an administrator with `manage-users` can only assign administrative roles they themselves possess. Do not treat that one safeguard as complete protection against business-role, group, composite-role, or client-configuration escalation.

## Fine-Grained Admin Permissions

Current supported Fine-Grained Admin Permissions are enabled under:

```text
Realm settings → Admin Permissions → Enabled
```

They allow policy-based control over realm resources and operations.

For delegated use of the Admin Console, the operator may also need the applicable `query-users`, `query-groups`, `query-clients`, or `query-organizations` role. These query roles support console discovery; by themselves they do not grant permission to view or manage a protected resource. Grant only the query roles needed for the intended console pages.

Do not combine Fine-Grained Admin Permissions with broad applicable `realm-management` roles such as `view-users`, `manage-users`, `view-clients`, or `manage-clients` for the same delegated operator. Current Keycloak behavior bypasses the corresponding fine-grained permission evaluation when a sufficient broad admin role already grants the operation. Verify effective roles inherited through groups and composites, not only direct mappings.

Current documentation also states that Fine-Grained Admin Permissions do not support enforcing access to federated resources. Test LDAP- or other federation-backed targets in the exact deployed version and route unsupported high-risk administration through a separately authorized service workflow.

### User operations

Relevant user scopes include:

- `view`;
- `manage`;
- `reset-password`;
- `map-roles`;
- `manage-group-membership`;
- `impersonate`.

### Role operations

Relevant role permissions include:

- `map-role` to assign a selected role to a user;
- `map-role-composite` to include it in another role;
- `map-role-client-scope` to expose it through a client scope.

### Group operations

Relevant operations include viewing or managing groups, members, and membership. Remember that a user can inherit roles through a group. Assigning a user to a protected group requires both the relevant user `manage-group-membership` permission and the target group's `manage-membership` permission; constrain and test both sides.

### Client operations

Client administration can change redirect URIs, credentials, scopes, and protocol mappers. It is a different and often more powerful function than assigning a predefined business role.

## Recommended group model

Create clearly named groups such as:

```text
/administrators/delegated-user-admins
/administrators/access-operators
/administrators/security-role-admins
/administrators/platform-admins
/users
/privileged-business-users
```

Protect `/administrators` and `/privileged-business-users` as targets. A delegated administrator must not be able to add themselves or another person to these groups.

## User administrator pattern

For a helpdesk-style user administrator:

1. Create a policy selecting `delegated-user-admins`.
2. Grant `view` for permitted ordinary users.
3. Grant only the required parts of `manage` and `reset-password`.
4. Do not grant `map-roles`.
5. Do not grant `manage-group-membership` unless the exact target groups are separately constrained.
6. Do not grant `impersonate`.
7. Deny management of administrator and privileged-user groups.
8. Exclude administrator-group members as user targets.

If `manage` is still broader than the use case allows in the deployed Keycloak version, separate the task through a narrow service workflow instead of relying on the Admin Console.

## Access operator pattern

An access operator can assign selected business roles without possessing them:

1. Select the allowed target users with a User Permission.
2. Grant the operator `map-roles` for those targets.
3. On each allowed role, grant `map-role` to the operator policy; or use a deliberately scoped client role permission.
4. Do not grant mapping permission for administrative, composite, or privileged roles.
5. Exclude the operator and all administrators as targets.
6. Protect group membership paths that grant the same role.
7. Do not allow the operator to create accounts, set or reset credentials, impersonate users, or populate the protected eligibility source.
8. Record the approval/workflow reference outside Keycloak.

This separates "may map roles" from "which roles may be mapped" and "to which users."

## The newly created or alternate account route

Blocking assignment to the operator's current `sub` is necessary but insufficient. The same person may create or control another account and assign the privilege to that account. Keycloak normally cannot infer that two accounts belong to the same human.

For privileged roles, enforce all of these boundaries:

1. **Provisioning is separate.** The role operator cannot create users, link identities, set initial passwords, reset credentials, manage identity providers, or impersonate users.
2. **Targets are pre-eligible.** An authoritative HR/IAM/SCIM source or a protected eligibility group supplies the allowed target population. The role operator cannot write that source or add members to that group. A newly created local account is not eligible by default.
3. **Approval binds the human and account.** A controlled workflow binds requester, approver, target person, target `iss` + `sub`, role, reason, and time window. For critical access it rejects a requester or operator who is also the target person, including a different account linked to that person.
4. **Execution is narrow.** A service account maps only the approved role to the approved eligible target and cannot create users, modify role definitions, or widen eligibility.
5. **Evidence crosses systems.** Provisioning, identity-linking, eligibility, approval, role mapping, credential changes, denied attempts, and use of the new privilege are correlated in an independently controlled audit store.

If one administrator can both create an account or take over its credentials **and** make it eligible for and assign a privileged role, Keycloak configuration alone cannot substantiate that self-escalation is prevented. Remove the combination or require an independent external approval and execution boundary.

## Direct and indirect escalation paths

Review all of these before stating that self-escalation is prevented.

### Direct assignment

Can the operator assign the privileged role to their own user?

### Group membership

Can they add themselves to a group that inherits the role?

### Composite roles

Can they change an allowed role so it contains an administrative role?

### Client scopes and protocol mappers

Can they configure a client to emit a hardcoded role or an overbroad audience? Keycloak documentation notes privilege-leak risks around client management and protocol mappers.

### Service accounts

Can they obtain, rotate, or use credentials for a more privileged service account?

### Impersonation

Can they impersonate a user who already has the privilege?

### Password and credential reset

Can they reset a privileged user's credentials and log in as that user?

### New and alternate accounts

Can they create an account, control its credentials or federated link, make it an eligible target, assign the role, and then use it? Can they split these steps between two accounts they control?

### Identity federation

Can they change an LDAP, SCIM, or identity-provider mapper so privileged membership is imported?

### Master realm and server administration

Can they gain access through a master-realm user, bootstrap account, Keycloak database, container platform, secret store, or backup restore?

Fine-Grained Admin Permissions govern Admin Console and Admin API operations. They do not constrain someone who directly controls the database or runtime.

## Four-eyes approval

Fine-grained permissions determine whether an actor may invoke an administrative operation. They do not prove that a second person approved a particular assignment.

Use an external IAM, ITSM, or controlled automation workflow:

```text
requester
   ↓ request
business approver
   ↓ approved workflow ID
restricted service account
   ↓ role mapping
Keycloak
   ↓ admin event
independent audit store
```

Requirements:

- requester and approver differ for critical roles;
- the service account accepts only approved role, target, and time window;
- the service account has no broad realm administration role;
- workflow ID, approver, decision, target, role, and execution result are retained;
- expired temporary access is automatically removed;
- rejected and failed attempts remain auditable.

A general automation platform administrator can be as powerful as an IAM administrator. Protect workflow code, secrets, and deployment permissions accordingly.

## Break-glass access

Use a separate emergency account or controlled elevation path:

- no ordinary daily use;
- strong multifactor authentication;
- credentials held in a privileged-access vault;
- short validity or just-in-time activation;
- immediate alert on use;
- mandatory incident or reason reference;
- review after every use;
- independent log copy.

Do not depend on the emergency account for normal maintenance.

## Keycloak configuration checklist

1. Inventory all master and realm administrators.
2. Remove shared administrator accounts.
3. Require strong MFA/passkeys for privileged accounts.
4. Enable supported Fine-Grained Admin Permissions in the target realm.
5. Grant only necessary `query-*` console roles and remove broad admin roles that bypass the corresponding fine-grained checks.
6. Create explicit policies for each delegated administrator group.
7. Define allowed user targets from an authoritative, operator-independent eligibility source.
8. Define allowed role mappings.
9. Protect privileged groups, eligibility groups, and administrator targets.
10. Omit impersonation, account creation, and credential reset from the access-operator function.
11. Separate client, role-definition, identity-provider, account-provisioning, eligibility, and realm administration.
12. Restrict Admin Console and Admin API at the network edge.
13. Enable user and admin events with minimized event payloads.
14. Export events to an independently controlled audit system.
15. Test policies using Keycloak's permission evaluation and real Admin REST API calls, including federated targets where relevant.
16. Recertify memberships, effective roles, provisioning rights, and eligibility writers regularly.
17. Patch promptly and repeat the negative suite after each Keycloak upgrade or permission-model change.

## Acceptance tests

### Positive tests

1. User administrator can view an allowed ordinary user.
2. User administrator can perform the intended profile or credential operation.
3. Access operator can assign one approved business role to an allowed target.
4. Audit reader can inspect required events without modifying identities.
5. Approval workflow assigns exactly the approved role and records its workflow ID.

### Negative tests

1. User administrator cannot assign any role.
2. Access operator cannot assign an administrative role.
3. Access operator cannot assign the business role to themselves.
4. Access operator cannot modify another administrator.
5. Access operator cannot add anyone to a privileged group.
6. Access operator cannot modify a composite role.
7. Delegated administrator cannot manage clients or protocol mappers.
8. Delegated administrator cannot impersonate a privileged user.
9. Delegated administrator cannot reset a privileged user's credentials.
10. Delegated administrator cannot clear the independent audit event.
11. A service account cannot perform an unapproved assignment.
12. A denied operation appears in the monitoring/audit path where the design requires it.
13. Access operator cannot create a user through the Admin Console or `POST /admin/realms/{realm}/users`.
14. Access operator cannot set or reset credentials, link an identity, or impersonate a newly created account.
15. A separately authorized user administrator can create an ordinary account, but neither that administrator nor the access operator can make it a privileged eligible target alone.
16. Access operator cannot add a new or alternate account to an eligibility, administrator, role-bearing, or privileged group.
17. An assignment requested through one account to another account controlled by the same person is rejected for critical access.
18. Removing direct broad admin roles is not defeated by an inherited `manage-users`, `view-users`, client-management, or composite role.
19. The result for a federated target matches the documented limitation and approved fallback workflow.

Run tests against both the Admin Console and Admin REST API. Check effective roles inherited through groups and composites, not only direct assignments.

## Limits to state honestly

No Keycloak policy can identify real-world common ownership of unrelated accounts or absolutely prevent an infrastructure owner with full control over Keycloak, its database, backups, secrets, and audit receiver from bypassing controls. Reduce those risks through authoritative identity linking, organizational separation, privileged access management, independent logging, change approval, monitoring, and external review.
