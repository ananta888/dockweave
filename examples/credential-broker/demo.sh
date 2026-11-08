#!/usr/bin/env bash
# Local credential-broker demo with NO real secrets.
# It starts a fake OpenAI-compatible provider and the broker, stores a dummy
# credential, grants it to an agent, mints a session and calls the gateway.
set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT="$(pwd)"
BIN="$ROOT/bin"
WORK="$(mktemp -d)"
export DOCKWEAVE_VAULT_PASSPHRASE='demo passphrase 123'
export DOCKWEAVE_BROKER_ADMIN_SOCKET="$WORK/data/admin.sock"

cleanup() {
  kill "${BROKER_PID:-}" "${FAKE_PID:-}" 2>/dev/null || true
  wait "${BROKER_PID:-}" "${FAKE_PID:-}" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> building"
make build >/dev/null

echo "==> starting fake provider"
go run ./examples/credential-broker/fake-provider >"$WORK/fake.log" 2>&1 &
FAKE_PID=$!
for _ in $(seq 1 50); do
  curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> starting broker"
"$BIN/dockweave-broker" --init \
  --data-dir "$WORK/data" \
  --providers-file examples/credential-broker/providers.demo.json \
  --allow-insecure-http \
  --listen 127.0.0.1:18081 >"$WORK/broker.log" 2>&1 &
BROKER_PID=$!
for _ in $(seq 1 50); do
  "$BIN/dockweave" broker status >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> storing dummy credential (value only via stdin)"
printf '%s' 'real-secret-only-in-vault' | "$BIN/dockweave" secret put openrouter-main --provider openrouter >/dev/null

echo "==> granting to agent developer"
"$BIN/dockweave" credential grant developer openrouter-main --models '*' \
  --rpm 30 --concurrent 2 --tokens-day 200000 >/dev/null

echo "==> minting session"
TOKEN="$("$BIN/dockweave" broker session create developer openrouter-main --ttl 1h | tail -n1)"

echo "==> calling broker (agent uses only the session token)"
curl -fsS -X POST 'http://127.0.0.1:18081/p/openrouter/chat/completions' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model":"openrouter/auto","messages":[{"role":"user","content":"hi"}]}'
echo

echo "==> broker request log (no secrets)"
"$BIN/dockweave" broker requests
"$BIN/dockweave" credential usage --grant "$("$BIN/dockweave" credential list --agent developer | awk 'NR==2{print $1}')" || true

echo "==> proof: fake provider received the vault value, not the session token"
grep 'upstream request' "$WORK/fake.log"
if grep -q 'dws_' "$WORK/fake.log"; then
  echo "ERROR: session token leaked upstream" >&2
  exit 1
fi
echo "demo ok"
