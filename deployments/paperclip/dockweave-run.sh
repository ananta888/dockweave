#!/bin/sh
# dockweave-run — paperclip process-adapter entrypoint for DockWeave.
#
# paperclip's `process` adapter is low-level: it launches this command with
# the PAPERCLIP_* context injected (agent/company/run id, API URL + key), but
# does NOT pass the task via env or stdin. This wrapper therefore:
#
#   1. fetches the agent's assigned task (first inbox issue) from the
#      paperclip API using the injected agent key, then
#   2. runs the harness INSIDE the DockWeave container (`docker exec -i`),
#      which stays the security boundary.
#
# Usage (set as adapterConfig):
#   command: /path/to/dockweave-run.sh
#   args:    ["<dockweave-container-name-or-id>"]
#
# The harness below is a deterministic placeholder (echoes the task plus the
# container hostname). Replace it with a real harness CLI, e.g.:
#   printf '%s' "$TASK" | docker exec -i "$CONTAINER" \
#     /memory/.local/bin/codex exec --skip-git-repo-check "$TASK"
set -u

CONTAINER="${1:-${DOCKWEAVE_CONTAINER:-}}"
API_URL="${PAPERCLIP_API_URL:-http://127.0.0.1:3100}"
API_KEY="${PAPERCLIP_API_KEY:-}"

# 1. Fetch the assigned task (first inbox item via the heartbeat inbox).
TASK=""
if [ -n "$API_KEY" ]; then
  TASK=$(curl -s --max-time 15 -H "Authorization: Bearer $API_KEY" \
    "$API_URL/api/agents/me/inbox-lite" \
    | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = d.get("items") if isinstance(d, dict) else d
if not items:
    sys.exit(0)
it = items[0]
ident = it.get("identifier") or ""
title = it.get("title") or it.get("description") or ""
print((ident + ": " + title) if ident else title)
' 2>/dev/null)
fi
[ -z "$TASK" ] && TASK="${DOCKWEAVE_DEFAULT_TASK:-(no task assigned)}"

echo "[dockweave-run] task: $TASK" >&2

# 2. Run the harness inside the DockWeave container.
if [ -n "$CONTAINER" ]; then
  printf '%s' "$TASK" | docker exec -i "$CONTAINER" \
    sh -c 'read p; echo "harness@$(hostname) processed: $p"'
else
  echo "harness@host processed: $TASK"
fi
