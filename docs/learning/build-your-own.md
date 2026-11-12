# Build Your Own DockWeave — A Beginner's Guide

This guide explains how to build an agent system like DockWeave from scratch.
It is aimed at developers who know Go and Linux basics but are new to coding
agents, harnesses, sandboxes and secure credential flow.

The goal is **understanding and rebuilding**, not copying a finished product.
DockWeave is the reference implementation; every phase points at real files.

---

## 0. The Mental Model

An agent is **not** a single program. It has layers:

```text
User / Operator
      │
      ▼
Control plane (CLI, API, UI)
      │  identity, policy, audit
      ▼
Orchestration (agents, chains, gates)
      │
      ├── Sandbox      (container, files, network)
      ├── Harness      (prompt, tools, loop)
      ├── Model        (LLM provider)
      ├── Memory       (memory, knowledge, skills)
      └── Credentials  (vault, broker, sessions)
```

Key ideas:

- The **model** produces text.
- The **harness** turns text into actions (tools, shell, files).
- The **sandbox** limits what those actions can reach.
- The **control plane** decides who may do what.
- **Credentials** do not belong in the agent.

Separate these five and you have the core architecture.

---

## 1. The Smallest Harness: the Agent Loop

A harness is essentially a loop:

```go
for step := 0; step < maxSteps; step++ {
    resp := model.Chat(ctx, tools, history)
    if len(resp.ToolCalls) == 0 {
        return resp.Text
    }
    for _, call := range resp.ToolCalls {
        result := executeTool(ctx, call)
        history = append(history, toolResult(call, result))
    }
}
return errors.New("max steps exceeded")
```

Watch out for:

- **Max steps** and **timeouts**.
- Validate tool calls; never execute blindly.
- Limit results (do not dump huge outputs into context).
- Allow user cancellation.
- Every tool execution is a potential security boundary.

A tool needs a clear contract:

```json
{
  "name": "read_file",
  "description": "Read a file",
  "inputSchema": {
    "type": "object",
    "properties": { "path": { "type": "string" } },
    "required": ["path"]
  }
}
```

DockWeave does not ship a harness: OpenCode, Codex, Claude Code, etc. are the
harnesses. DockWeave provides the environment, identity and boundaries.

---

## 2. Sandbox: Why a Container Alone Is Not Enough

A minimal sandbox run:

```bash
docker run --rm -it \
  --user 1000:1000 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --memory 2g --cpus 1 \
  -v /srv/project:/workspace/project:ro \
  -v /var/lib/agents/<id>/memory:/memory \
  codex-image
```

Rules:

- No `--privileged`
- No `/var/run/docker.sock` inside the agent
- No `CAP_SYS_ADMIN`
- Non-root by default
- Resource limits
- Explicit network mode (`none`, internal network, bridge)

A container isolates processes and files, but not automatically:

- the network (bridge often allows internet),
- secrets in env vars,
- other containers,
- the host via exposed ports.

You need policy and egress control on top.

Reference: `internal/dockerclient/dockerclient.go`, `internal/agents/agents.go`.

---

## 3. Control Plane: Daemon + CLI + Unix Socket

A naive design lets the CLI call Docker directly. Safer is a small control
plane:

```text
CLI/TUI/Web ──(Unix socket, SO_PEERCRED)──> dockweaved (root)
                                              │
                                              ├── Docker API
                                              ├── SQLite
                                              └── Mount manager
```

Why:

- Privileged operations stay in one place.
- The CLI is a thin client.
- The socket is local and protected by peer credentials.
- Roles (`admin`, `monitor`) are enforced centrally.

Minimal API:

```text
GET    /api/v1/agents
POST   /api/v1/agents
POST   /api/v1/agents/{id}/start
POST   /api/v1/agents/{id}/stop
GET    /api/v1/workspaces
POST   /api/v1/grants
```

Reference: `internal/api`, `cmd/dockweaved`, `pkg/sdk`.

---

## 4. Identity and Persistence

Agent identity must be **independent of the container**. Containers are
recreated; identity remains.

Minimal schema:

```sql
CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  type TEXT NOT NULL,
  image TEXT NOT NULL,
  command TEXT,
  state TEXT NOT NULL
);
```

Other tables:

- `workspaces` — allowed host directories
- `grants` — agent × workspace × mode
- `events` — audit
- `agent_mcp` / `agent_skills` — tools per agent
- `agent_type_mcp` / `agent_type_skills` — templates per agent type

Important:

- Stable IDs (`ag_…`); never use container IDs as identity.
- Version migrations.
- No secrets in normal metadata.

Reference: `internal/model/model.go`, `internal/persistence/persistence.go`.

---

## 5. Workspace Grants and Mounts

A grant is a **permission**, not a mount. The mount makes it effective.

Flow:

```text
register workspace
  → create grant (agent, workspace, ro/rw)
    → apply mount
      → container sees /workspace/<name>
```

Watch out:

- **Allowlist**: only registered host paths are mountable.
- **Canonicalize**: `filepath.EvalSymlinks`, then re-check.
- **TOCTOU**: open the source with
  `openat2(RESOLVE_BENEATH|NO_MAGICLINKS)` and mount via `/proc/self/fd/<n>`.
- **Read-only**: private bind → `remount,ro` → move into the shared tree.
  A direct `-o ro` does not reliably propagate into slave namespaces.
- **No `umount -l` by default**: return a structured error on `EBUSY`.
  Lazy unmount only with an explicit force flag and audit.

Reference: `internal/mounts`, `internal/workspaces`,
`docs/security-decisions.md`.

---

## 6. Secrets: Why EnvFiles Are Not Enough

An API key in an env var is visible to:

- the agent and its tools,
- `docker inspect`,
- `/proc/<pid>/environ`,
- accidental logs,
- prompt injection.

Better: **secretless agent**.

```text
Agent ──(session token)──> broker ──(real key)──> provider
```

Vault requirements:

- Argon2id for the passphrase
- AES-256-GCM with a random nonce
- per-entry AAD so ciphertexts cannot be swapped
- atomic writes, `0600`, directory `0700`
- no plaintext in SQLite, logs or Docker metadata

Broker requirements:

- fixed provider destinations (no arbitrary target)
- do not follow redirects
- deny by default
- store session tokens only as hashes
- expiry and immediate revocation
- rate/cost limits per grant

Limits:

- a copied session token is usable until expiry/revocation,
- host root can read process memory,
- budgets are estimates.

Reference: `internal/credentialvault`, `internal/credentialbroker`,
`docs/threat-model-credential-broker.md`.

---

## 7. Egress: A Proxy Is Not a Network Boundary

If the agent can reach the internet directly, it can bypass the broker:

- `offline`: `--network none`
- `broker-only`: internal Docker network, no external route, broker on the host
  gateway
- `restricted-development`: not implemented → fail closed
- `unrestricted-legacy`: explicitly unsafe

Important:

- Network rules must live **outside** the agent.
- Failures must **fail closed**.
- The host gateway remains residual risk; nftables port filtering is a
  separate step.
- Test IPv4/IPv6, DNS, IMDS and private ranges separately.

Reference: `docs/egress-security.md`, `internal/agents`,
`tests/integration/egress_network_test.go`.

---

## 8. Audit and Monitoring

Without audit you cannot investigate incidents.

Minimal:

```go
event := Event{Type: "grant.created", Actor: user, AgentID: a, Message: msg}
event.PrevHash = lastHash
event.Hash = SHA256(prevHash | fields)
```

Also:

- Observe Docker events and process data (procfs).
- Detect write conflicts between agents.
- Never put secrets in events.

Reference: `internal/persistence` (hash chain), `internal/monitor`.

---

## 9. Interfaces: CLI First, Then TUI/Web

Build in this order:

1. **CLI** — fast to test, scriptable, honest.
2. **TUI** — interactive terminal use.
3. **Web** — agent list, chat/terminal, configuration, analysis.

A browser cannot speak a Unix socket. For web you need a gateway:

```text
Browser ──HTTP/WebSocket──> gateway ──Unix socket──> daemon
```

Important:

- Token/cookie for browser access.
- No privileged behavior in the gateway.
- Terminal over a real PTY (e.g. xterm.js + WebSocket).
- Never display secrets in the UI.

Reference: `cmd/dockweave`, `ui/tui`, `ui/web`, `internal/webui`.

---

## 10. MCP and Skills

MCP is JSON-RPC 2.0 (stdio or Streamable HTTP):

- `initialize` describes protocol version and capabilities.
- `tools/list` returns tools with `inputSchema` (JSON Schema).
- An MCP server is **not** one standard JSON document.

For an agent system:

- Store skills/MCP per agent type as templates.
- Copy them into editable per-agent rows when creating an agent.
- Materialize into the memory home before start
  (`opencode.json`, `.claude.json`, generic manifest).
- Store env names only; values belong in the vault.

Reference: `internal/agentconfig`, `internal/agents`, `docs/web-ui.md`.

---

## 11. Tests and Threat Modeling

At minimum:

- Unit tests for policy, vault, paths.
- Race detector.
- Integration tests with real mounts only in an isolated environment.
- Fake providers instead of real API keys.
- Attack tests: secret exfiltration, path traversal, redirects, budget,
  expired session, wrong agent.

Threat-model questions:

1. Who is trusted? (operator, daemon, broker, host)
2. What is untrusted? (agent, prompt injection, network)
3. Which boundary protects which asset?
4. What happens on failure? (fail closed?)
5. Which residual risks remain?

---

## 12. Build Order (Milestones)

| Phase | Goal | Acceptance |
|---|---|---|
| M0 | Concepts | You can explain harness, agent, sandbox, grant |
| M1 | Sandbox | container runs non-root, no caps, no Docker socket |
| M2 | Control plane | CLI → daemon → Docker works |
| M3 | Identity | agent survives container recreation |
| M4 | Workspaces | ro/rw grant works, allowlist enforced |
| M5 | Safe mounts | RO staging, TOCTOU protection, no lazy-unmount default |
| M6 | Secrets | vault + broker; agent sees only a session token |
| M7 | Egress | `offline`/`broker-only` enforced, fail closed |
| M8 | Audit | hash chain, no secrets in events |
| M9 | UI | CLI/TUI/Web with clear permission display |
| M10 | MCP/skills | per-agent configurable and materialized |

Build **one phase at a time**. A half-finished broker without egress is not a
security solution.

---

## 13. Common Mistakes

1. API keys in env vars.
2. Docker socket inside the agent.
3. `--privileged` because it is easier.
4. Mounts without an allowlist.
5. Not resolving symlinks.
6. `umount -l` as default.
7. Selling a proxy as "network security".
8. Storing session tokens in plaintext.
9. Budgets without reservation for concurrent requests.
10. Claiming security that is not enforced.

---

## 14. Security Checklist

- [ ] Agent runs non-root, without caps, without Docker socket.
- [ ] Only registered host paths are mountable.
- [ ] RO mounts are staged correctly.
- [ ] No secrets in env, inspect, SQLite or logs.
- [ ] Broker: deny by default, fixed targets, no redirects.
- [ ] Session tokens hashed, expiring, revocable.
- [ ] Egress: `broker-only` blocks external routes.
- [ ] Failures deny rather than expand permissions.
- [ ] Audit events contain no secrets.
- [ ] Open risks are documented.

---

## 15. Reference Map

| Topic | DockWeave file |
|---|---|
| Agent model | `internal/model/model.go` |
| Persistence/migrations | `internal/persistence` |
| Docker sandbox | `internal/dockerclient` |
| Agent lifecycle | `internal/agents` |
| Workspace grants | `internal/workspaces` |
| Mount security | `internal/mounts` |
| Vault | `internal/credentialvault` |
| Broker | `internal/credentialbroker` |
| Egress | `internal/agents`, `tests/integration/egress_network_test.go` |
| UI gateway | `internal/webui` |
| Angular UI | `ui/web` |
| TUI | `ui/tui` |
| Learning guide | `docs/learning/agent-harness.md` |

---

## 16. Conclusion

A secure agent system is not "start a container". It is about drawing
**trust boundaries** deliberately:

- What may the agent do?
- What must it never see?
- Who decides?
- What happens on failure?
- What is actually enforced — and what is only documented?

If you can answer these questions for your own system, you have understood the
important part. The rest is implementation.
