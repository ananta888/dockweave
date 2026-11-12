# Learning Project: Understanding Harnesses and Agent Systems

This repository is primarily a **learning and experimentation platform**.
The goal is not to run DockWeave as a finished product, but to make the
following understandable:

- what an **agent harness** is,
- how an **agent system** is composed of model, harness, sandbox, identity,
  tools, memory and orchestration,
- which **technologies** are used and why,
- and which **security decisions** are necessary when agents may execute code,
  read/write files and call APIs.

> **Not a production promise.** DockWeave is a teaching project. It is not
> hardened for multi-user operation, hosting or protection against a
> compromised host root. Open items are tracked in
> `todo/credential-vault.json` and the security documents.

## 1. What is a Harness?

An LLM is just a text model. A **harness** is the program around the model that
turns a request into real work:

| Harness responsibility | Example |
|---|---|
| Prompting / system context | rules, roles, project context, AGENTS.md |
| Tool calling | read files, run shell, apply patches |
| Execution | start commands in the container, feed output back |
| Context management | conversation, files, search results |
| Approvals / boundaries | sandbox, allowed paths, network, budgets |
| Loop control | plan → act → observe → repeat |
| Error handling | retries, cancellation, timeouts |

OpenCode, Codex CLI, Claude Code and Gemini CLI are familiar harnesses. They
differ in UI, tools and configuration but follow the same pattern.

Important: **harness ≠ model ≠ agent identity ≠ container.** DockWeave
separates them:
- The model comes from a provider (OpenRouter, OpenAI, Anthropic, …).
- The harness runs inside the agent container.
- The agent identity is a stable record (name, image, command, permissions),
  independent of container IDs.
- The container is the sandbox, not the identity.

## 2. Layers of an Agent System

```
Operator / UI
   │  identity, policy, audit
   ▼
Orchestration  (agents, chains, gates, sessions)
   │
   ├── Sandbox      (container, filesystem, network, resources)
   ├── Harness      (prompt, tools, loop, context)
   ├── Model        (LLM provider)
   ├── Memory       (memory, knowledge, skills)
   └── Credentials  (vault, broker, session tokens)
```

Each layer needs its own security boundary. A proxy alone is not a network
boundary, a container alone is not an identity, and an API key in an
environment variable is not secret management.

## 3. DockWeave as a Case Study

| Layer | Implementation | Entry point |
|---|---|---|
| Agent identity | SQLite, stable ID, type/image/command | `internal/model`, `internal/persistence` |
| Sandbox | Docker, three mounts, cap-drop, non-root | `internal/dockerclient`, `internal/agents` |
| File access | workspace grants ro/rw, mount manager | `internal/workspaces`, `internal/mounts` |
| Harness | agent brings its own (OpenCode, Codex, …) | agent command |
| Memory | `/memory`, `/knowledge`, `/notes` | `internal/memory`, `internal/knowledge` |
| Skills/MCP | per agent and per agent type in SQLite, materialized into memory home | `internal/agents`, `internal/agentconfig` |
| Credentials | unprivileged broker, vault, sessions, budgets | `internal/credentialvault`, `internal/credentialbroker` |
| Egress | `offline` / `broker-only` via Docker networks | `internal/agents`, `internal/dockerclient` |
| Audit | hash-chained events | `internal/persistence` |
| UI | CLI, TUI, Angular web UI, graph | `cmd/dockweave`, `ui/tui`, `ui/web` |
| Agent-driven UI | UI MCP server + WebSocket bridge | `internal/webui` |
| Tests/demos | fake provider, UI MCP, demos | `examples/`, `tests/` |

Typical flow:

```text
CLI/Web → dockweaved (root) → Docker Engine → agent container
                              │
                              ├─ /agent   (harness install, AGENTS.md, skills)
                              ├─ /memory  (persistent home)
                              ├─ /workspace (project grants, rslave)
                              └─ /knowledge
```

The agent does **not** run as root and gets no Docker socket.

## 4. Technologies and Why

| Technology | Use | Learning rationale |
|---|---|---|
| **Go** | daemon, CLI, broker, web gateway | concurrency, static binaries, clear interfaces |
| **Docker Engine API** | create, exec, attach | reproducible isolation without a custom runtime |
| **Linux mounts/namespaces** | `rslave`, RO staging, `openat2` | real filesystem boundaries, not conventions |
| **SQLite (modernc, pure Go)** | registry, grants, MCP/skills, audit | local, transactional, no external DB |
| **Unix sockets + `SO_PEERCRED`** | API/admin/terminal | local auth without TCP/token leakage |
| **Argon2id + AES-256-GCM** | credential vault | established KDF and AEAD, no custom crypto |
| **JSON Schema / MCP** | tool and provider contracts | explicit, checkable tool interfaces |
| **OpenAI-compatible HTTP APIs** | broker gateway | many harnesses support `baseURL` + dummy key |
| **Angular 20** | web UI | structured components, typed API |
| **Foblex Flow** | agent/resource graph | explain nodes, edges, ports and layouts visually |
| **xterm.js** | browser chat/terminal | a real PTY instead of a fake chat bubble |
| **Docker `internal` networks** | `broker-only` | block external routes, keep host gateway for broker |
| **nftables (planned)** | host gateway filtering | network boundary outside agent control |

### Why not custom crypto?

Hand-written cryptography is almost always wrong. The vault uses Argon2id
(memory-hard) and AES-256-GCM (authenticated encryption). Each entry is bound
to its name via AAD so ciphertexts cannot be swapped.

### Why a separate broker?

The root daemon must stay small. Credentials do not belong in the component
that controls Docker and mounts. The broker is unprivileged, keeps secrets only
in memory, and gives agents only short-lived session tokens.

## 5. Security: The Why

### 5.1 Threat model (short)

| Adversary | Goal | Control |
|---|---|---|
| Compromised agent | read secrets | no keys in container; vault outside; broker |
| Compromised agent | escape filesystem | cap-drop, non-root, mount allowlist, `openat2` |
| Compromised agent | affect other agents | separate memory/workspace trees |
| Prompt injection | exfiltrate data | fixed provider destinations, no free target choice, no redirects |
| Agent | abuse budget | rate/concurrency/cost limits in broker |
| Local user | control daemon | socket permissions + `SO_PEERCRED` roles |
| Tampering | alter audit | event hash chain |

### 5.2 Core principles

1. **Least privilege:** `CapDrop: ALL`, no Docker socket, non-root by default.
2. **Deny by default:** no workspace grant, credential grant, provider or model
   means denied.
3. **Secretless agents:** an agent never gets the real provider key, only a
   short-lived restricted session token.
4. **Fail closed:** mount, network, vault or policy failures deny, not expand.
5. **Separate trust boundaries:** operator, broker, daemon and agent are
   different roles; the agent is untrusted.
6. **No security theatre:** what is not enforced (full egress isolation, host
   gateway port filtering) is documented as open.
7. **Traceability:** security-relevant changes are audited and decisions are
   recorded in `docs/security-decisions.md`.

### 5.3 Why a credential broker instead of EnvFiles?

A key in an env var is visible to the agent, `docker inspect`,
`/proc/*/environ` and accidental logs. Prompt injection can read it. The broker
inverts the model:

- The agent only knows a dummy/session token.
- The broker injects the real key immediately before the allowed provider
  request.
- The destination is fixed per provider; redirects are not followed.
- Budgets and rate limits are checked broker-side.

Limits: a copied session token remains usable until expiry/revocation; a
compromised host root can read process memory; budgets are estimates, not a
billing guarantee.

### 5.4 Why egress isolation?

An HTTP proxy is not a security boundary if the agent can reach the internet
directly. Network profiles:

- `offline`: no network.
- `broker-only`: internal Docker network, no external route, broker on host
  gateway.
- `restricted-development`: not implemented → fail closed.
- `unrestricted-legacy`: explicitly unsafe.

Still open: host gateway port filtering with nftables so the agent can reach
only the broker, not other host services.

### 5.5 Why MCP/JSON Schema contracts?

MCP does not describe "a server" as one JSON; it defines JSON-RPC methods and a
per-tool `inputSchema` (JSON Schema). DockWeave adds a normalized
`mcpServer` contract, imports `mcpServers` configs and validates tool schemas.
Connections between agents, tools, gates and providers become explicit instead
of implicit.

## 6. What This Project Is Not

- Not a production-ready platform.
- Not full egress isolation (nftables part open).
- No protection against compromised host root or kernel.
- Not multi-tenant.
- No guarantee that broker cost estimates equal the provider bill.
- No replacement for provider-side limits, rotation and audits.

## 7. Learning Path (Exercises)

1. **Three mounts:** create/start an agent, compare `docker inspect` with
   `dockweave agent paths`.
2. **Workspace grant:** register a workspace, assign ro/rw, test writes inside
   the container, inspect the mount table.
3. **Understand the harness:** start OpenCode/Codex in the container, inspect
   process tree, config files and session markers.
4. **MCP/skills:** inspect type defaults, apply them per agent, check the
   materialized `opencode.json`/`.claude.json`.
5. **Credential broker:** run the demo, observe the fake provider, prove the
   real value only reaches the broker.
6. **Egress:** set `broker-only`, test direct provider access, expect failure;
   then document the host gateway residual risk.
7. **Policy:** test wrong agent, wrong model, expired session, budget limit.
8. **Threat model:** add a new attack idea and design a control with acceptance
   criteria and a test.

## 8. Glossary

| Term | Meaning |
|---|---|
| **Harness** | program that controls a model and executes tools |
| **Agent** | identity + configuration + harness runtime |
| **Sandbox** | container/namespace isolating code and files |
| **Grant** | explicit permission (file or credential) |
| **Broker** | intermediary that injects real credentials |
| **Session** | short-lived capability token for a grant |
| **MCP** | Model Context Protocol for tools/resources |
| **Egress** | outbound network traffic |
| **Fail closed** | deny on error, never open |
| **Audit chain** | linked events that reveal tampering |

## 9. Further Reading

- `docs/learning/build-your-own.md` — step-by-step build guide

- `docs/architecture.md` — components and data flow
- `docs/security-decisions.md` — decisions made
- `docs/threat-model-credential-broker.md` — broker threat model
- `docs/egress-security.md` — egress concept and open items
- `docs/adr/0001-credential-broker.md` — comparison with Agent Vault
- `todo/credential-vault.json` — open work with acceptance criteria
