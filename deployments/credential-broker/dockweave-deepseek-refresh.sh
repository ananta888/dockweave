#!/bin/sh
# Refresh the DeepSeek provider session in an opencode agent.
# Usage: dockweave-deepseek-refresh <agent-id> [credential] [model] [ttl]
set -eu
AGENT_ID="${1:?agent id required}"
CREDENTIAL="${2:-deepseek-main}"
MODEL="${3:-deepseek-flash}"
TTL="${4:-8h}"
SOCKET="${DOCKWEAVE_SOCKET:-/run/dockweave/dockweaved.sock}"
curl -sS --max-time 30 --unix-socket "$SOCKET" \
  -X POST "http://localhost/api/v1/agents/$AGENT_ID/provider/deepseek" \
  -H 'Content-Type: application/json' \
  -d "{\"credential\":\"$CREDENTIAL\",\"model\":\"$MODEL\",\"ttl\":\"$TTL\"}" >/dev/null
echo "refreshed deepseek provider for $AGENT_ID at $(date -Is)"
