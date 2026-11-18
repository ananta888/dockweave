#!/bin/sh
# dockweave-run — paperclip process-adapter entrypoint for DockWeave.
#
# Default mode (MODEL): the task is sent to the DockWeave OpenAI-compatible
# endpoint over the daemon's unix socket. The endpoint drives the live agent
# (native backend where available, TUI otherwise), so paperclip tasks are
# visible in the agent's TUI and return clean structured text.
#
# Orchestration mode (args: ["developer","orchestrate"]): the agent is the
# Lead/CEO. It returns a JSON work breakdown; this wrapper creates child
# issues, assigns them to the role agents, wakes them, and leaves the parent
# in_progress. That is the delegation step Paperclip expects from a CEO agent.
#
# Legacy mode (container starting with "dockweave-"): the old `docker exec`
# one-shot path is kept for backwards compatibility.
#
# Adapter config:
#   command: /path/to/dockweave-run.sh
#   args:    ["demo-opencode"]                     # normal worker
#   or args: ["developer", "orchestrate"]          # Lead/CEO
#   or args: ["dockweave-<cid>", "opencode", "/memory/.local/bin/opencode"]
set -u

MODEL_OR_CONTAINER="${1:-${DOCKWEAVE_MODEL:-demo-opencode}}"
MODE="${2:-}"
HARNESS_BIN="${3:-/memory/.local/bin/codex}"
API_URL="${PAPERCLIP_API_URL:-http://127.0.0.1:3100}"
API_KEY="${PAPERCLIP_API_KEY:-}"
SOCKET="${DOCKWEAVE_SOCKET:-/run/dockweave/dockweaved.sock}"
TIMEOUT="${PAPERCLIP_PROCESS_TIMEOUT:-${DOCKWEAVE_TIMEOUT:-240}}"
if [ "$MODE" = "orchestrate" ] && [ -z "${PAPERCLIP_PROCESS_TIMEOUT:-}" ] && [ -z "${DOCKWEAVE_TIMEOUT:-}" ]; then
  TIMEOUT=540
fi
ROLES_FILE="${DOCKWEAVE_PAPERCLIP_ROLES:-$HOME/.config/dockweave/paperclip-roles.json}"
WORKSPACE="${DOCKWEAVE_PAPERCLIP_WORKSPACE:-/workspace/crystal-run}"
# Absolute CLI path: the process adapter's PATH is not the login shell PATH.
PAPERCLIPAI="${PAPERCLIPAI_BIN:-$(command -v paperclipai 2>/dev/null || echo /home/krusty/.local/bin/paperclipai)}"
export PAPERCLIPAI_BIN="$PAPERCLIPAI"

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
title = it.get("title") or ""
desc = it.get("description") or ""
if not desc and it.get("summary"):
    desc = it.get("summary") or ""
head = (ident + ": " + title) if ident else title
print(head + ("\n\n" + desc if desc else ""))
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
if [ -z "$TASK" ]; then
  if [ -n "${DOCKWEAVE_DEFAULT_TASK:-}" ]; then
    TASK="$DOCKWEAVE_DEFAULT_TASK"
  else
    echo "[dockweave-run] no assigned task; heartbeat no-op" >&2
    exit 0
  fi
fi

echo "[dockweave-run] task: $TASK" >&2

case "$MODEL_OR_CONTAINER" in
  dockweave-*)
    # Legacy one-shot docker exec path.
    case "$MODE" in
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

PROMPT="$TASK"
if [ "$MODE" = "orchestrate" ]; then
  PROMPT=$(python3 - "$TASK" "$WORKSPACE" <<'EOF'
import sys
task, ws = sys.argv[1], sys.argv[2]
print(task + """

Du bist der Lead/CEO des DockWeave-Engineering-Teams. Zerlege das Goal in konkrete Child-Issues fuer die Rollen backend, frontend, reviewer und platform.
Gemeinsames Arbeitsverzeichnis: """ + ws + """

Antworte NUR mit einem JSON-Objekt in einem ```json-Block:
{"tasks":[{"title":"...","description":"...","role":"backend","depends_on":[]}]}
Jede Aufgabe braucht title, description und role. Keine Prosa ausserhalb des JSON-Blocks.
""")
EOF
)
fi

if [ "$MODE" != "orchestrate" ]; then
  PROMPT="$PROMPT

Abschluss-Protokoll: Pruefe deine Arbeit (Build/Tests, falls vorhanden) und beende
deine Antwort mit genau einer Statuszeile:
  STATUS: DONE        - Aufgabe vollstaendig umgesetzt und geprueft
  STATUS: BLOCKED     - du brauchst eine Entscheidung oder fehlende Rechte
  STATUS: IN_PROGRESS - Teilarbeit erledigt, Rest offen
Ohne diese Zeile gilt die Aufgabe als IN_PROGRESS."
fi

# 2. Send the task through the DockWeave endpoint (unix socket, no token).
BODY=$(python3 -c 'import json,sys; print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role": "user", "content": sys.argv[2]}]
}))' "$MODEL_OR_CONTAINER" "$PROMPT") || exit 1

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

HELPER_DIR="${DOCKWEAVE_PAPERCLIP_HELPER_DIR:-$(dirname "$0")}"
export PAPERCLIPAI_BIN="$PAPERCLIPAI"

if [ "$MODE" = "orchestrate" ] && [ -n "$ISSUE_ID" ] && [ -f "$ROLES_FILE" ]; then
  # Extract the JSON work breakdown from the answer and create child issues.
  PLAN=$(printf '%s' "$CONTENT" | python3 "$HELPER_DIR/plan-extract.py" 2>/dev/null || true)
  if [ -n "$PLAN" ]; then
    ISSUE_JSON=$("$PAPERCLIPAI" issue get "$ISSUE_ID" --json 2>/dev/null || true)
    META=$(printf '%s' "$ISSUE_JSON" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("\t\t"); sys.exit(0)
print("%s\t%s\t%s" % (d.get("companyId") or "", d.get("goalId") or "", d.get("projectId") or ""))' 2>/dev/null)
    COMPANY=$(printf '%s' "$META" | cut -f1)
    GOAL=$(printf '%s' "$META" | cut -f2)
    PROJECT=$(printf '%s' "$META" | cut -f3)
    [ -n "$COMPANY" ] || COMPANY="${PAPERCLIP_COMPANY_ID:-}"

    CHILDREN=$(printf '%s' "$PLAN" | python3 "$HELPER_DIR/plan-delegate.py" \
      "$ROLES_FILE" "$COMPANY" "$ISSUE_ID" "$GOAL" "$PROJECT" "$WORKSPACE" 2>/dev/null || true)

    if printf '%s' "$CHILDREN" | grep -q '^CHILD'; then
      SUMMARY=$(printf 'Lead-Plan erstellt. Child-Issues:\n%s\n\n%s' "$CHILDREN" "$CONTENT")
      "$PAPERCLIPAI" issue update "$ISSUE_ID" --status in_progress --comment "$SUMMARY" --json >/dev/null 2>&1 || true
      echo "[dockweave-run] delegated child issues:" >&2
      printf '%s\n' "$CHILDREN" >&2
      exit 0
    fi
    echo "[dockweave-run] plan found but no child issue could be created" >&2
    printf '%s\n' "$CHILDREN" >&2
  else
    echo "[dockweave-run] no JSON plan in the Lead answer" >&2
  fi
fi

# Record the answer AND change the source state. Paperclip marks an issue
# blocked ("recovery needed") when a successful run leaves the source state
# unchanged, so the update is part of a successful run.
if [ -n "$ISSUE_ID" ] && [ -x "$PAPERCLIPAI" ]; then
  STATUS="${DOCKWEAVE_PAPERCLIP_STATUS:-}"
  if [ -z "$STATUS" ]; then
    STATUS=$(printf '%s' "$CONTENT" | python3 -c 'import re,sys
raw = sys.stdin.read().upper()
hits = re.findall(r"STATUS:\s*(DONE|BLOCKED|IN_PROGRESS|IN PROGRESS)", raw)
mapping = {"DONE": "done", "BLOCKED": "blocked",
           "IN_PROGRESS": "in_progress", "IN PROGRESS": "in_progress"}
print(mapping.get(hits[-1], "in_progress") if hits else "in_progress")' 2>/dev/null)
    [ -n "$STATUS" ] || STATUS=in_progress
  fi
  if ! "$PAPERCLIPAI" issue update "$ISSUE_ID" --status "$STATUS" --comment "$CONTENT" --json >/dev/null 2>&1; then
    echo "[dockweave-run] could not update paperclip issue $ISSUE_ID" >&2
  fi
  # Close the parent Lead issue when every child is finished.
  python3 "$HELPER_DIR/parent-close.py" "$ISSUE_ID" 2>/dev/null || true
fi
