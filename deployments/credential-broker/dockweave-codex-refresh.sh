#!/bin/bash
# Renew a DockWeave broker session token for a Codex agent and write it into the
# agent's /memory so Codex's model provider auth.command can read it. The token
# is never logged.
#
# Usage: dockweave-codex-refresh <agent-id> <broker-agent> <credential> [ttl]
set -euo pipefail
AGENT_ID="${1:?agent id required}"
BROKER_AGENT="${2:?broker agent required}"
CREDENTIAL="${3:?credential required}"
TTL="${4:-8h}"
TOKEN_PATH="/memory/.config/dockweave/codex-token"

export DOCKWEAVE_BROKER_ADMIN_SOCKET="${DOCKWEAVE_BROKER_ADMIN_SOCKET:-$HOME/.local/share/dockweave/broker/admin.sock}"
DOCKWEAVE_BIN="${DOCKWEAVE_BIN:-$HOME/dockweave/bin/dockweave}"

# At boot the agent container may start after this timer. Wait up to 5 min so a
# reboot does not leave an expired token until the next hourly run.
CID=""
for _ in $(seq 1 30); do
  CID=$(docker ps -q --filter "label=dockweave.agent.id=$AGENT_ID" || true)
  [ -n "$CID" ] && break
  sleep 10
done
if [ -z "$CID" ]; then
  echo "agent $AGENT_ID is not running; nothing to refresh"
  exit 0
fi

TOKEN=$("$DOCKWEAVE_BIN" broker session create "$BROKER_AGENT" "$CREDENTIAL" --ttl "$TTL" | tail -n1)
if [ -z "$TOKEN" ]; then
  echo "broker returned no session token" >&2
  exit 1
fi

printf '%s' "$TOKEN" | docker exec -i "$CID" sh -c "umask 077; mkdir -p ${TOKEN_PATH%/*}; cat > $TOKEN_PATH"
echo "refreshed broker session for $BROKER_AGENT/$CREDENTIAL at $(date -Is)"
