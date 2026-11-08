# SSH Broker — Design (separate increment)

Status: **design only** (2026-11-08). No SSH credential handling is
implemented in the HTTP broker. SSH private keys must never be sent through an
HTTP gateway.

Language: English · [Deutsch](ssh-broker-design.de.md)

## Problem

An agent needs Git/SSH access without holding a long-lived private key. A
plain `ssh-agent` socket is not a safe boundary on its own: whoever can reach
the socket can request signatures for any key it holds, even without reading
the private key. The OpenSSH agent protocol also does not include the target
host in `SIGN_REQUEST`, so a pure agent socket cannot enforce
destination-constrained keys by itself.

## Goals

1. No private key in the agent container.
2. Short-lived, destination-bound credentials.
3. Per-agent identity and explicit policy (host, user, repo, time).
4. Immediate revocation; session lifetime bounded.
5. No SSH private key ever leaves the broker.
6. FIDO2/hardware-backed keys optional.

## Non-goals

- Replacing SSH host verification.
- Transparent interception of arbitrary SSH traffic without a wrapper.
- Protecting against host root.

## Proposed architecture

```
agent ──> dockweave ssh ... ──(unix socket)──> dockweave-ssh-broker
                                                │
                                                ├── CA key (vault, never exported)
                                                ├── policy: agent → host/user/repo/TTL
                                                ├── short-lived user certificate
                                                └── optional FIDO2 signer
```

### Flow A — short-lived certificate (preferred)

1. The agent invokes `dockweave ssh --dest github.com --user git -- git ...`
   or a Git `core.sshCommand` wrapper.
2. The wrapper sends the *requested destination* (host, port, user, repo
   hint) to the broker over the admin-authenticated unix socket.
3. The broker checks policy (DENY by default) and issues:
   - an OpenSSH user certificate signed by an SSH CA,
   - a short-lived private key held by a per-session `ssh-agent` process,
     or an ephemeral key file in a 0700 dir that is removed on exit.
4. The wrapper execs `ssh -i <key> -o CertificateFile=<cert>`.
5. Certificate options include `permit-pty`/`permit-agent-forwarding` only
   when policy allows; TTL ≤ 15 min by default.

The certificate is bound to the destination by the wrapper invocation, and
the broker only signs for the requested host/user. The key is not reusable
for another host after the certificate expires. For stronger binding, issue
one key per destination request and delete it after the command.

### Flow B — constrained agent signing (optional)

Run a broker-owned `ssh-agent` socket per session and expose it to the agent.
This only works safely with:
- one key per session,
- a wrapper that decides which destination is being contacted before the
  agent socket is exposed,
- immediate socket teardown and `ssh-add -D` on session end,
- no forwarding of the socket to other processes/hosts.

Because `SIGN_REQUEST` lacks the destination, Flow B alone is **not**
destination-constrained. It is only acceptable when the key/cert is already
bound to a single destination and short-lived.

## Policy model

```text
grant:
  agent: opencode-01
  host: github.com
  user: git
  repo: ananta888/dockweave       # optional
  command: git-upload-pack,git-receive-pack
  ttl: 15m
  max_sessions: 2
  require_touch: false
```

Deny by default. No wildcard host unless explicitly set. Revocation is
checked on every signature/issuance.

## SSH CA and key handling

- SSH CA private key lives in the same encrypted vault or a dedicated
  hardware-backed signer; it is never exported to agents.
- Certificates: `ssh-keygen -s ca -I <agent>-<session> -n <user> -V +15m -O ...`.
- Key material for Flow A is generated per session in a `0700` temp dir and
  removed on exit; never logged.
- FIDO2: broker asks the hardware key to sign; no export.
- Host keys are verified normally by the client; the broker never disables
  `StrictHostKeyChecking`.

## Risks and open questions

1. Wrapper bypass: the agent can call `ssh` directly unless egress policy
   blocks port 22/other SSH paths. This is an egress problem, not a broker
   problem.
2. Git may cache/forward credentials; `GIT_SSH_COMMAND` must be enforced.
3. Certificates with `permit-agent-forwarding` widen the boundary; default
   deny.
4. Host key verification must remain enabled.
5. FIDO2 availability/touch prompts in headless agents.
6. A copied session key/cert is usable within its TTL.

## Phases

1. Design + TODO (this document). ✅
2. CA key storage, policy, certificate issuance, wrapper and tests with a
   local SSH server.
3. Git integration (`GIT_SSH_COMMAND`) and short-lived key cleanup.
4. FIDO2 signer and hardware tests.
5. Privileged/e2e tests: direct SSH without wrapper must fail under
   broker-only egress; wrapper SSH succeeds; revoked/expired certs fail.
