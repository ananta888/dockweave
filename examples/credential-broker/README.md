# Credential Broker Demo

Runs a **fake** OpenAI-compatible provider and the DockWeave broker locally.
It uses only dummy credentials and proves that:

- the agent/client only ever sees a `dws_…` session token,
- the broker injects the real (dummy) vault value upstream,
- streaming/usage and the secret-free request log work,
- budgets/counters are updated.

```bash
./examples/credential-broker/demo.sh
```

Requirements: Go toolchain, `curl`, `awk`.

Files:

| File | Purpose |
|---|---|
| `fake-provider/main.go` | Local `/chat/completions` upstream that prints the auth header it receives |
| `providers.demo.json` | Points the `openrouter` adapter at the fake provider (http, local only) |
| `demo.sh` | End-to-end demo with temp data dir and cleanup |

The demo uses `--allow-insecure-http` because the fake upstream is plain
HTTP on loopback. Never use that flag with a real provider; the broker
refuses non-HTTPS provider URLs by default.

For the real OpenCode/OpenRouter setup see
[`docs/credential-broker.md`](../../docs/credential-broker.md).
