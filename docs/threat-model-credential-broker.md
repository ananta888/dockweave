# Threat Model — Credential Vault & Broker

Scope: `internal/credentialvault`, `internal/credentialbroker`,
`cmd/dockweave-broker`, and the `secret`/`credential`/`broker` CLI.

## Assets

1. Long-lived provider credentials (OpenRouter, OpenAI, Anthropic, DeepSeek,
   Gemini, GitHub/HTTP tokens).
2. The vault master passphrase and derived keys.
3. Broker session tokens (short-lived capabilities).
4. Operator policy, budget counters and audit/request logs.

## Trust boundaries

```
 untrusted agent │ agent gateway (session token, policy, budget)
                 │
 trusted operator │ admin unix socket (SO_PEERCRED)
                 │
 trusted broker process │ decrypts credentials in memory
                 │
 trusted host kernel / host root
```

The broker is a separate unprivileged process. It is **not** a sandbox: a
host root compromise, ptrace or process-memory access defeats it.

## Adversaries and controls

| Adversary | Attempt | Control | Residual risk |
|---|---|---|---|
| Compromised agent | Read env / Docker inspect | Keys never placed in container env; only session token | Token can be abused within its grant until expiry/revocation |
| Compromised agent | Read `/memory`, `/knowledge`, logs | Vault is outside agent mounts; no secret in logs | Agent-owned files may contain its *own* session token |
| Compromised agent | Call broker without grant | DENY by default, per-agent grant, provider/model checks | A copied valid token works |
| Compromised agent | Change policy/grant | Admin socket is `0600` + SO_PEERCRED EUID allowlist | Host root or same-UID process can administer |
| Compromised agent | Steer broker to attacker host | Fixed provider base URL; path traversal/unknown provider rejected; redirects not followed | Provider-side compromise is out of scope |
| Compromised agent | Exceed budget | Per-grant rate/concurrency/request/token/cost limits, reservations | Estimates may under-count; concurrent edge cases |
| Local unprivileged user | Open vault file | AES-256-GCM + Argon2id; `0600`, `0700`, exclusive lock | Offline passphrase guessing if file is copied |
| Local unprivileged user | Connect to admin socket | peer UID check, 0600 socket | Same-UID processes are trusted |
| Network attacker | Reach proxy socket | Unix socket `0600`; optional TCP must be firewalled | TCP listener is opt-in and warns |
| Operator error | Weak passphrase | Minimum 8 chars only | No strength meter/HSM |
| Logs/audit | Secret leakage | No values in request log, errors, events | Provider error bodies are passed through and may echo prompts |

## Cryptographic design

- AES-256-GCM, random 96-bit nonce per write.
- Argon2id (64 MiB, 3 iterations, 4 lanes, 16-byte salt) by default.
- Per-entry AAD binds ciphertext to the credential name, preventing entry
  swaps.
- Versioned envelope; atomic writes; passphrase not persisted.
- Only the SHA-256 of session tokens is stored.

## Explicit non-goals

- No protection against a compromised host kernel, host root, ptrace or
  malicious broker binary.
- No network isolation. An agent with direct egress can bypass the broker.
- No TLS interception or custom root CA.
- No protection against provider-side logging or billing errors.
- No SSH key brokerage in this increment.
- No multi-tenant isolation between operators sharing one broker process.

## Fail-closed behaviour

- Broker refuses to start without a passphrase or with a corrupt/wrong vault.
- Unknown/expired/revoked session → 401.
- No/expired/foreign grant, disallowed provider/model → 403.
- Rate/concurrency/budget exceeded → 429.
- Upstream failure → 502, reservation rolled back.
- Unknown provider or malformed path → 404.
- Policy/store read failure during matching follows the same deny path.

## Open risks (must be tracked)

1. Egress bypass (highest).
2. Session token replay within grant lifetime.
3. Broker-side cost estimate vs. provider billing.
4. Same-host boundary vs. host root.
5. Agent can spend its allowed budget if compromised.
6. No workload identity binding (token is portability).
7. No TUI/API visibility yet.

## Verification in this increment

- Vault: wrong passphrase, tamper detection, AAD swap, no plaintext on disk,
  permissions, lock, rekey.
- Broker: auth, revocation, model/provider denial, SSRF/path traversal,
  redirect non-following, budget/rate/concurrency, streaming usage,
  no secret in request log or metadata API.
- Not verified: real provider, container egress, host firewall, TUI, REST API.
