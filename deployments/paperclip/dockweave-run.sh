#!/bin/sh
# dockweave-run — paperclip process-adapter entrypoint for DockWeave.
#
# Default mode (MODEL): the task is sent to the DockWeave OpenAI-compatible
# endpoint over the daemon's unix socket. The endpoint drives the live agent
# (native backend where available, TUI otherwise), so paperclip tasks are
# visible in the agent's TUI and return clean structured text.
#
# Legacy mode (container starting with "dockweave-"): the old `docker exec`
# one-shot path is kept for backwards compatibility.
#
# paperclip's `process` adapter injects PAPERCLIP_* (agent/company/run id,
# API URL + key) but does not pass the task; this wrapper fetches the first
# assigned inbox item.
#
# Adapter config:
#   command: /path/to/dockweave-run.sh
#   args:    ["demo-opencode"]                     # DockWeave model/agent
#   or args: ["dockweave-<cid>", "opencode", "/memory/.local/bin/opencode"]
set -u

MODEL_OR_CONTAINER="${1:-${DOCKWEAVE_MODEL:-demo-opencode}}"
KIND="${2:-codex}"
HARNESS_BIN="${3:-/memory/.local/bin/codex}"
API_URL="${PAPERCLIP_API_URL:-http://127.0.0.1:3100}"
API_KEY="${PAPERCLIP_API_KEY:-}"
SOCKET="${DOCKWEAVE_SOCKET:-/run/dockweave/dockweaved.sock}"
TIMEOUT="${PAPERCLIP_PROCESS_TIMEOUT:-${DOCKWEAVE_TIMEOUT:-240}}"

# 1. Fetch the assigned task (first inbox item via the heartbeat inbox).
TASK=""
ISSUE_ID=""
if [ -n "$API_KEY" ]; then
  INBOX=$(curl -s --max-time 15 -H "Authorization: Bearer $API_KEY" \
    "$API_URL/api/agents/me/inbox-lite" 2>/dev/null || true)
  TASK=$(printf '%s' "$INBOX" | python3 -c 'import json,sys
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
  ISSUE_ID=$(printf '%s' "$INBOX" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = d.get("items") if isinstance(d, dict) else d
if items:
    print(items[0].get("id") or "")
' 2>/dev/null)
fi
[ -z "$TASK" ] && TASK="${DOCKWEAVE_DEFAULT_TASK:-(no task assigned)}"

echo "[dockweave-run] task: $TASK" >&2

case "$MODEL_OR_CONTAINER" in
  dockweave-*)
    # Legacy one-shot docker exec path.
    case "$KIND" in
      codex)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" "$HARNESS_BIN" exec \
          --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox
        ;;
      opencode)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" "$HARNESS_BIN" run
        ;;
      *)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" \
          sh -c 'read p; echo "harness@$(hostname) processed: $p"'
        ;;
    esac
    exit $?
    ;;
esac

# 2. Send the task through the DockWeave endpoint (unix socket, no token).
BODY=$(python3 -c 'import json,sys; print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role": "user", "content": sys.argv[2]}]
}))' "$MODEL_OR_CONTAINER" "$TASK") || exit 1

RESP=$(curl -sS --max-time "$TIMEOUT" --unix-socket "$SOCKET" \
  -X POST http://localhost/v1/chat/completions \
  -H 'Content-Type: application/json' -d "$BODY" 2>&1)
STATUS=$?
if [ "$STATUS" != 0 ]; then
  echo "[dockweave-run] endpoint request failed (curl $STATUS): $RESP" >&2
  exit 1
fi

CONTENT=$(printf '%s' "$RESP" | python3 -c 'import json,sys
raw = sys.stdin.read()
try:
    d = json.loads(raw)
    print(d["choices"][0]["message"]["content"])
except Exception as e:
    print("[dockweave-run] invalid endpoint response: %s" % e, file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
') || exit 1
printf '%s\n' "$CONTENT"

# Record the answer AND change the source state. Paperclip marks an issue
# blocked ("recovery needed") when a successful run leaves the source state
# unchanged, so the update is part of a successful run.
if [ -n "$ISSUE_ID" ] && command -v paperclipai >/dev/null 2>&1; then
  STATUS="${DOCKWEAVE_PAPERCLIP_STATUS:-done}"
  if ! paperclipai issue update "$ISSUE_ID" --status "$STATUS" --comment "$CONTENT" --json >/dev/null 2>&1; then
    echo "[dockweave-run] could not update paperclip issue $ISSUE_ID" >&2
  fi
fi
