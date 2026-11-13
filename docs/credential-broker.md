# Credential Vault & API Broker

DockWeave's credential broker lets an agent call a provider API **without ever
holding the real API key**. Secrets live in an encrypted local vault; the
agent gets a short-lived session token that only works for its explicit
grant.

> **Status of this increment (2026-11-13):** vault, policy, sessions,
> HTTP gateway, budgets, CLI, daemon/TUI metadata, the `offline` /
> `broker-only` egress profiles, **OAuth credential bundles with
> broker-side refresh (Codex ChatGPT)** and the `opencode` / `codex-chatgpt`
> provider adapters are implemented and tested. `broker-only`
> uses an internal Docker network with no external route. Remaining open:
> host-gateway port filtering with nftables, `restricted-development`,
> OpenCode Go's extra session header, and the SSH broker. See
> [egress-security.md](egress-security.md) and
> `todo/credential-vault.json`.

Language: English · [Deutsch](credential-broker.de.md)

## Why

DockWeave already isolates filesystems, mounts and capabilities. API keys were
still passed through `EnvFile`, image env or `/memory`, where a compromised
agent or prompt injection can read them. The broker removes long-lived
credentials from the agent entirely.

## Architecture

```
operator CLI ──(unix, SO_PEERCRED)──> dockweave-broker (unprivileged)
                                          │
                                          ├── encrypted vault (AES-256-GCM)
                                          ├── policy (DENY by default)
                                          ├── sessions (dws_…, hashed)
                                          └── budget/rate limits
                                          │
agent ──(unix socket or restricted TCP)──> /p/{provider}/... ──> provider API
        Authorization: Bearer dws_…                                (real key
                                                                    injected)
```

- `dockweave-broker` is a separate process, never root, no mount/Docker work.
- The agent's provider config points at the broker and uses the session token
  as its dummy API key.
- The broker replaces the auth header with the real credential only for the
  fixed provider destination that policy allowed.

## Components

| Package | Responsibility |
|---|---|
| `internal/credentialvault` | Encrypted file vault: Argon2id + AES-256-GCM, AAD per entry, atomic writes, lock |
| `internal/credentialbroker` | Policy, sessions, budgets, HTTP gateway, admin API, request log |
| `internal/brokerclient` | CLI client over the admin unix socket |
| `cmd/dockweave-broker` | Broker process and listeners |
| `cmd/dockweave` | `secret`, `credential`, `broker`, `egress` commands |

## Quick start (local, no real secret)

1. Build:

   ```bash
   make build
   ```

2. Create the vault. The passphrase is read from the environment and never
   written to disk or passed on the command line:

   ```bash
   export DOCKWEAVE_VAULT_PASSPHRASE='a long local passphrase'
   ./bin/dockweave-broker --init
   ```

3. In another shell, store a credential (stdin only):

   ```bash
   printf '%s' 'sk-or-dummy' | ./bin/dockweave secret put openrouter-main --provider openrouter
   ./bin/dockweave secret list
   ```

4. Grant it to an agent and mint a session:

   ```bash
   ./bin/dockweave credential grant developer openrouter-main --models '*' \
     --rpm 30 --concurrent 2 --tokens-day 200000
   ./bin/dockweave broker session create developer openrouter-main --ttl 1h
   # prints: dws_… (only once)
   ```

5. Use the session token as the agent's API key. For an OpenAI-compatible
   harness:

   ```bash
   export OPENAI_BASE_URL='http://127.0.0.1:8787/p/openrouter'
   export OPENAI_API_KEY='dws_…'
   ```

   Start the broker with a TCP listener for this (unix socket stays enabled):

   ```bash
   ./bin/dockweave-broker --listen 127.0.0.1:8787
   ```

6. Inspect status and usage without exposing secrets:

   ```bash
   ./bin/dockweave broker status
   ./bin/dockweave broker requests
   ./bin/dockweave credential usage --grant cg_…
   ```

### Fake provider demo

A fully local demo with a fake OpenAI-compatible upstream is in
`examples/credential-broker/`. It uses only dummy credentials and proves that
the agent never sees the real value.

## OpenCode + OpenRouter tutorial

The real OpenRouter key stays in the vault. OpenCode only knows a session
token.

1. Start the broker on a Docker-reachable address (or a unix socket your
   container can mount). Example for a bridge gateway:

   ```bash
   ./bin/dockweave-broker --listen 172.30.0.1:8787
   ```

2. Store and grant:

   ```bash
   printf '%s' "$OPENROUTER_API_KEY" | ./bin/dockweave secret put openrouter-main --provider openrouter
   ./bin/dockweave credential grant opencode-01 openrouter-main \
     --models 'openrouter/auto,anthropic/claude-*' --rpm 30 --concurrent 2 --usd-day 5
   ./bin/dockweave broker session create opencode-01 openrouter-main --ttl 2h
   ```

3. In the agent's OpenCode config (`~/.config/opencode/opencode.json` or
   project `opencode.json`), define an OpenAI-compatible provider whose
   base URL points at the broker:

   ```json
   {
     "$schema": "https://opencode.ai/config.json",
     "provider": {
       "dockweave-openrouter": {
         "npm": "@ai-sdk/openai-compatible",
         "name": "OpenRouter via DockWeave",
         "options": {
           "baseURL": "http://172.30.0.1:8787/p/openrouter",
           "apiKey": "dws_REPLACE_WITH_SESSION_TOKEN"
         },
         "models": {
           "openrouter/auto": { "name": "OpenRouter Auto" }
         }
       }
     }
   }
   ```

4. Run OpenCode normally. Requests go to
   `http://172.30.0.1:8787/p/openrouter/chat/completions`; the broker checks
   the session, grant, model and budget, then injects the real key upstream.

5. Verify the real key is not in the container:

   ```bash
   docker inspect <container> | grep -i sk-or   # no output
   docker exec <container> env | grep -i api_key # only dws_… (or nothing)
   ```

Notes:

- Streaming, tool calling, cancellation and provider errors pass through.
  The broker does not rewrite response bodies.
- Redirects are **not** followed, so the injected credential cannot leak to
  a redirect target. The 3xx is returned to the client.
- Model allowlists are exact strings; use `*` to allow any model.
- Cost budgets use `--prompt-usd-per-1k` / `--completion-usd-per-1k` and the
  provider's usage metadata. They are broker-side estimates, not a provider
  billing guarantee. Configure hard limits at the provider as well.

## OAuth credentials (Codex ChatGPT)

Codex uses a ChatGPT OAuth login, not an API key. The broker stores the OAuth
bundle in the vault and refreshes the access token before injecting it. The
agent only ever holds a `dws_…` session token.

1. Import the existing login (the source file is left untouched):

   ```bash
   ./bin/dockweave secret import-codex codex-main \
     --auth-file ~/.codex/auth.json \
     --grant --agent developer --models '*'
   ```

   The bundle contains `access_token`, `refresh_token`, `id_token` and
   `account_id`; none of it is printed.

2. Mint a session and point Codex at the broker (`~/.codex/config.toml` of the
   agent):

   ```toml
   model = "gpt-6.1-sol"
   model_provider = "dockweave"

   [model_providers.dockweave]
   name = "DockWeave"
   base_url = "http://<broker>:8787/p/codex-chatgpt"
   env_key = "DOCKWEAVE_TOKEN"
   wire_api = "responses"
   ```

   ```bash
   export DOCKWEAVE_TOKEN="$(dockweave broker session create developer codex-main --ttl 8h | tail -1)"
   ```

3. The broker maps `/p/codex-chatgpt/responses` to
   `https://chatgpt.com/backend-api/codex/responses`, refreshes the token when
   it is within 120 s of expiry, persists the rotated refresh token, and adds
   `chatgpt-account-id` plus the fixed `OpenAI-Beta`/`originator` headers.

Notes:

- Refresh tokens rotate. If the same login is also used by a client outside the
  broker, both sides invalidate each other. Use a separate login for direct
  terminal use, or let the broker own it.
- The provider `headers` map is operator-defined and cannot be set by an agent.
- `secret import-opencode NAME --auth-file …` imports an OpenCode Zen key
  (provider `opencode`).
- The ChatGPT Codex backend requires `stream: true` and `store: false`; the
  Codex CLI already sends both.

## Operator CLI

```bash
dockweave secret put NAME --provider PROVIDER    # value from stdin
dockweave secret list
dockweave secret rotate NAME                     # value from stdin
dockweave secret revoke NAME

dockweave secret migrate --agent AGENT --env-file FILE          # explicit file only
dockweave secret migrate --agent AGENT --env-file FILE --dry-run
dockweave secret migrate --agent AGENT --env-file FILE --yes --grant --models '*'
dockweave secret migrate --agent AGENT --env-file FILE --yes --disable-env-file

dockweave credential grant AGENT CREDENTIAL [flags]
dockweave credential list --agent AGENT
dockweave credential revoke AGENT CREDENTIAL
dockweave credential usage --grant GRANT_ID

dockweave broker status
dockweave broker health
dockweave broker session create AGENT CREDENTIAL --ttl 1h
dockweave broker session list --agent AGENT
dockweave broker session revoke ID
dockweave broker requests

dockweave egress status AGENT
dockweave egress profile AGENT broker-only
```

`secret migrate` reads only the explicitly given env file, never scans
directories, never prints values, and only clears the agent's `env_file`
reference plus renames the file after every store succeeded. Without `--yes`
it requires an interactive confirmation.

Admin socket: `$DOCKWEAVE_BROKER_ADMIN_SOCKET`, default
`~/.local/share/dockweave/broker/admin.sock`, mode `0600`, `SO_PEERCRED`
checked against the process EUID (or `--admin-uids`).

## Egress profiles

| Profile | Enforcement |
|---|---|
| `offline` | Docker `none` (no network) |
| `broker-only` | Internal Docker network, no external route; host gateway reachable for the broker |
| `restricted-development` | Not implemented; start is refused |
| `unrestricted-legacy` / empty | Legacy bridge; explicitly unsafe |

`dockweave egress status AGENT` reports profile, enforcement flag, network,
gateway and broker base URL. In broker-only agents the daemon sets
`DOCKWEAVE_BROKER_BASE_URL=http://<gateway>:8787`; bind the broker there.
See [egress-security.md](egress-security.md) for the host-gateway residual
risk.

## REST API and TUI

The daemon exposes read-only broker metadata (no secrets, admin/monitor GET)
when `BROKER_ADMIN_SOCKET` is configured:

| Method | Path |
|---|---|
| GET | `/api/v1/broker/status` |
| GET | `/api/v1/broker/grants?agent=` |
| GET | `/api/v1/broker/sessions?agent=` |
| GET | `/api/v1/broker/requests` |
| GET | `/api/v1/broker/usage?grant=` |
| GET | `/api/v1/agents/{id}/egress` |
| PUT | `/api/v1/agents/{id}/network-profile` |
| PUT | `/api/v1/agents/{id}/env-file` |

The TUI shows the broker status on the Dashboard and the agent's egress
profile plus credential grants in the agent details. Secrets are never shown.

The broker's own admin socket remains the only mutation path for secrets and
grants.

## Admin API

Unix socket, JSON, peer-UID authorized. Secret values are accepted in `PUT`
bodies only and never returned.

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/status` | health, counts, providers, egress status |
| GET | `/v1/secrets` | metadata only |
| PUT | `/v1/secrets/{name}` | `{provider, value}` |
| DELETE | `/v1/secrets/{name}` | delete |
| GET | `/v1/grants?agent=` | list grants |
| POST | `/v1/grants` | create/replace grant |
| DELETE | `/v1/grants/{id}` | revoke |
| GET | `/v1/sessions?agent=` | list sessions (no tokens/hashes) |
| POST | `/v1/sessions` | mint `{agent, credential|grant_id, ttl}` |
| DELETE | `/v1/sessions/{id}` | revoke immediately |
| GET | `/v1/requests` | secret-free request log |
| GET | `/v1/usage?grant=` | budget snapshot |

Agent gateway: `/p/{provider}/{upstream-path}` with
`Authorization: Bearer dws_…` (or `X-Dockweave-Session`, `X-Api-Key`,
`X-Goog-Api-Key`).

## Storage and unlock

- File: `<data-dir>/vault.json`, `0600`, directory `0700`.
- Master passphrase: `DOCKWEAVE_VAULT_PASSPHRASE`, minimum 8 characters.
  There is no passphrase in config files, args or the vault.
- Argon2id parameters are stored in the envelope; AES-256-GCM with a random
  nonce per write and the credential name as AAD.
- Atomic replace (`temp + fsync + rename`), plus an exclusive `flock` so two
  broker processes cannot write the same vault.
- `Store.RotatePassphrase()` re-encrypts all values under a new salt/key.
- **Backup:** copy `vault.json` while the broker is stopped. Restoring
  requires the same passphrase. Backups are ciphertext only. Back up policy,
  sessions and budget files too if you want grants/limits preserved; they
  contain no secret values.

Unattended startup: the broker refuses to start without
`DOCKWEAVE_VAULT_PASSPHRASE`. Any automated unlock helper (systemd
`LoadCredential`, TPM, OS keyring) is an operator decision and is **not**
implemented here. Do not put the passphrase in a world-readable unit file.

## What is guaranteed

- No real provider credential in container env, Docker inspect, SQLite,
  `/memory`, `/knowledge`, logs or the agent's provider config.
- DENY by default: no grant, wrong agent, wrong provider, wrong model, expired
  grant or revoked/expired session is rejected.
- Secret values are never returned by the admin API or the request log.
- Redirects are not followed with the injected credential.
- Fixed destination per provider; agents cannot choose an arbitrary host
  through the gateway path (traversal and unknown providers are rejected).

## What is NOT guaranteed (open blockers)

1. **Host-gateway filtering is incomplete.** `broker-only` blocks external
   egress with an internal Docker network, but the agent can still reach
   host services bound to the gateway address. nftables rules are still open
   (`egress-003`).
2. **No automatic broker socket mounting.** The operator must provide the
   broker address (the daemon sets `DOCKWEAVE_BROKER_BASE_URL` in
   broker-only agents) and configure the harness.
3. **`restricted-development` is not implemented**; starting an agent with it
   fails closed.
4. **Session token is a bearer capability.** If copied, it is usable within
   its grant until expiry/revocation. There is no workload-bound auth yet.
5. **Budget is an estimate.** Provider usage may differ; no provider billing
   ceiling is enforced.
6. **Same-host broker is not a strong boundary** against host root. A root
   compromise can read process memory.
7. **SSH keys** are not handled; they are not HTTP credentials.
8. **Migrations from EnvFile** are operator-driven (`secret migrate`), not a
   background scanner.

See `docs/threat-model-credential-broker.md` for the full model.
